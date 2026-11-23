package transport

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apernet/quic-go/http3"
)

type transportLane struct {
	authority string
	h3        *http3.ClientConn
	proof     string
	kind      string
	round     func(*http.Request) (*http.Response, error)
	close     func()
	stats     func() uint64
	dead      <-chan struct{}
	usable    func() bool
	active    atomic.Int64
	opening   atomic.Int64
	last      atomic.Int64
	ack       atomic.Bool
	health    string
}

type selector struct {
	ctx        context.Context
	events     chan string
	mu         sync.Mutex
	lane       *transportLane
	mode       string
	androidVPN bool // The protected app VPN confirms a blackhole and probes recovery even when idle.
	dial       func(context.Context, string) (*transportLane, error)
}

func (s *selector) current() *transportLane { s.mu.Lock(); defer s.mu.Unlock(); return s.lane }
func (s *selector) set(old, l *transportLane) bool {
	s.mu.Lock()
	if s.lane != old || s.ctx.Err() != nil {
		s.mu.Unlock()
		l.close()
		return false
	}
	l.last.Store(time.Now().UnixNano())
	s.lane = l
	s.mu.Unlock()
	select {
	case s.events <- l.kind:
	case <-s.ctx.Done():
	}
	return true
}
func (s *selector) initial() (*transportLane, error) {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	if s.mode != "auto" {
		return s.dial(ctx, s.mode)
	}
	type result struct {
		l *transportLane
		e error
	}
	ch := make(chan result)
	attempt := func(kind string, delay time.Duration) {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		l, e := s.dial(ctx, kind)
		select {
		case ch <- result{l, e}:
		case <-ctx.Done():
			if l != nil {
				l.close()
			}
		}
	}
	go attempt("h3", 0)
	go attempt("h2", 250*time.Millisecond)
	var last error
	for range 2 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r := <-ch:
			if r.e == nil {
				return r.l, nil
			}
			last = r.e
			if terminalDialError(last) {
				return nil, last
			}
		}
	}
	return nil, last
}

// The controlled site serves a bounded zero-filled object independent of the target app.
func (s *selector) capacity(l *transportLane) time.Duration {
	ctx, cancel := context.WithTimeout(s.ctx, 850*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", l.health, nil)
	req.Header.Set("User-Agent", "")
	start := time.Now()
	resp, e := l.round(req)
	if e != nil {
		return 0
	}
	defer resp.Body.Close()
	n, e := io.Copy(io.Discard, io.LimitReader(resp.Body, 32769))
	if e != nil || resp.StatusCode != 200 || n != 32768 {
		return 0
	}
	return time.Since(start)
}
func (s *selector) probeH3() (*transportLane, error) {
	ctx, cancel := context.WithTimeout(s.ctx, time.Second)
	defer cancel()
	return s.dial(ctx, "h3")
}

func (s *selector) prebuildH2() (<-chan *transportLane, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(s.ctx, 1500*time.Millisecond)
	ready := make(chan *transportLane)

	go func() {
		alt, _ := s.dial(ctx, "h2")
		select {
		case ready <- alt:
		case <-ctx.Done():
			if alt != nil {
				alt.close()
			}
		}
		close(ready)
	}()
	return ready, cancel
}

func (s *selector) watch() {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	var prev uint64
	lastProgress := time.Now()
	var weak, success int
	probeInterval := 30 * time.Second
	var lastH2 time.Time
	nextProbe := time.Now().Add(30 * time.Second)
	// Only watch owns the pending dial. Cancel also closes a completed, unused handshake.
	var pending <-chan *transportLane
	var abandon context.CancelFunc
	var owner *transportLane
	discard := func() {
		if pending != nil {
			abandon()
			pending = nil
		}
	}
	defer discard()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		}
		l := s.current()
		if owner != l {
			discard()
		}
		if l.kind == "h2" {
			if time.Now().Before(nextProbe) || (!s.androidVPN && (l.active.Load() == 0 || time.Since(time.Unix(0, l.last.Load())) > 3*time.Second)) {
				continue
			}
			q, e := s.probeH3()
			ok := e == nil && s.capacity(q) > 0
			if s.androidVPN && ok {
				ok = s.capacity(q) > 0
			}
			if ok {
				success++
				if s.androidVPN || success == 2 {
					if s.set(l, q) {
						prev = q.stats()
						lastProgress = time.Now()
						weak, success = 0, 0
						go s.retire(l)
					}
					continue
				}
			} else {
				success = 0
				if !s.androidVPN {
					probeInterval = min(5*time.Minute, 2*probeInterval)
				}
			}
			if q != nil {
				q.close()
			}
			nextProbe = time.Now().Add(probeInterval)
			continue
		}
		now := l.stats()
		delta := now - prev
		prev = now
		if delta != 0 || l.active.Load() == 0 {
			lastProgress = time.Now()
		}
		gone := false
		select {
		case <-l.dead:
			gone = true
		default:
		}
		progress := delta > 32768
		idle := time.Since(time.Unix(0, l.last.Load())) > 3*time.Second
		if s.androidVPN {
			progress = delta != 0
			idle = idle && l.opening.Load() == 0
		}
		if l.active.Load() == 0 || (!gone && (progress || idle)) {
			discard()
			weak = 0
			continue
		}
		weak++
		if weak < 2 {
			if delta == 0 && time.Since(lastH2) >= 10*time.Second {
				lastH2 = time.Now()
				pending, abandon = s.prebuildH2()
				owner = l
			}
			continue
		}
		weak = 0
		sample := make(chan time.Duration, 1)
		go func() { sample <- s.capacity(l) }()
		var alt *transportLane
		if pending != nil {
			alt = <-pending
			discard()
		} else if s.androidVPN || time.Since(lastH2) >= 10*time.Second || gone {
			// On Android, cooldown applies to speculative dials. Two silent windows while
			// an application needs the path must be able to recover immediately.
			lastH2 = time.Now()
			ctx, cancel := context.WithTimeout(s.ctx, time.Second)
			alt, _ = s.dial(ctx, "h2")
			cancel()
		}
		if alt == nil {
			continue
		}
		h2time := s.capacity(alt)
		h3time := <-sample
		degraded := h3time == 0 || h3time > 4*h2time
		if s.androidVPN {
			degraded = h3time == 0 && l.stats() == now
		}
		if h2time > 0 && degraded {
			if !s.set(l, alt) {
				continue
			}
			probeInterval = 30 * time.Second
			nextProbe = time.Now().Add(probeInterval)
			success = 0
			if silentForTwoWindows(l, now, lastProgress) {
				l.close()
			} else {
				go s.drainH3(l, alt)
			}
		} else {
			alt.close()
		}
	}
}
func (s *selector) retire(l *transportLane) {
	for s.ctx.Err() == nil && l.active.Load() > 0 {
		time.Sleep(250 * time.Millisecond)
	}
	l.close()
}

// Positive H2 health is established by the caller. Count all QUIC receive bytes,
// including ACKs, and re-read the counter after the active health check.
func silentForTwoWindows(l *transportLane, initial uint64, since time.Time) bool {
	return time.Since(since) >= time.Second && l.stats() == initial
}

func terminalDialError(e error) bool {
	var a x509.UnknownAuthorityError
	var h x509.HostnameError
	var v x509.CertificateInvalidError
	var typed *Error
	return errors.As(e, &a) || errors.As(e, &h) || errors.As(e, &v) || (errors.As(e, &typed) && (typed.Code == Authentication || typed.Code == Configuration))
}
func (s *selector) drainH3(old, backup *transportLane) {
	prev, since := old.stats(), time.Now()
	for s.ctx.Err() == nil && old.active.Load() > 0 {
		time.Sleep(500 * time.Millisecond)
		if n := old.stats(); n != prev {
			prev, since = n, time.Now()
			continue
		}
		if time.Since(since) >= time.Second && backup.usable() {
			// Idle applications can still have an open stream. Elicit traffic before closing it.
			s.capacity(old)
			if silentForTwoWindows(old, prev, since) && backup.usable() {
				old.close()
				return
			}
			prev, since = old.stats(), time.Now()
		}
	}
	old.close()
}
