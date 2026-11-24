package transport

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apernet/quic-go/http3"
)

type transportLane struct {
	authority    string
	h3           *http3.ClientConn
	proof        string
	kind         string
	round        func(*http.Request) (*http.Response, error)
	close        func()
	stats        func() uint64
	packets      func() packetCounters
	dead         <-chan struct{}
	usable       func() bool
	active       atomic.Int64
	opening      atomic.Int64
	wake         chan<- struct{}
	stalledReads atomic.Uint64
	delivered    atomic.Uint64
	last         atomic.Int64
	ack          atomic.Bool
	expiry       *atomic.Int64
	health       string
}

type selector struct {
	ctx             context.Context
	events          chan string
	mu              sync.Mutex
	lane            *transportLane
	mode            string
	androidVPN      bool
	wake            chan struct{}
	recoveryMu      sync.Mutex
	recoveryBlocked atomic.Bool
	cancelRecovery  context.CancelFunc
	dial            func(context.Context, string) (*transportLane, error)
}

func signal(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (l *transportLane) business() {
	l.last.Store(time.Now().UnixNano())
	signal(l.wake)
}

func (s *selector) setRecoveryAllowed(allowed bool) {
	s.recoveryMu.Lock()
	if s.recoveryBlocked.Swap(!allowed) != !allowed {
		if !allowed && s.cancelRecovery != nil {
			s.cancelRecovery()
		}
		signal(s.wake)
	}
	s.recoveryMu.Unlock()
}

func (s *selector) current() *transportLane { s.mu.Lock(); defer s.mu.Unlock(); return s.lane }
func (s *selector) set(old, l *transportLane) bool {
	s.mu.Lock()
	if s.lane != old || s.ctx.Err() != nil {
		s.mu.Unlock()
		l.close()
		return false
	}
	s.lane = l
	trace("selected", l.kind, "")
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
	return s.capacityBatch(l, 1, nil)
}

func (s *selector) capacityContext(ctx context.Context, l *transportLane, measured *packetCounters) time.Duration {
	req, _ := http.NewRequestWithContext(ctx, "GET", l.health, nil)
	l.healthHeaders(req)
	start := time.Now()
	resp, e := l.round(req)
	if e != nil {
		trace("health_fail", l.kind, "")
		return 0
	}
	defer resp.Body.Close()
	n, e := io.Copy(io.Discard, io.LimitReader(resp.Body, 32769))
	if e != nil || resp.StatusCode != 200 || n != 32768 || l.accepted(resp) != nil {
		trace("health_fail", l.kind, "")
		return 0
	}
	if measured != nil {
		*measured = responseCounters(resp)
	}
	trace("health_ok", l.kind, "")
	// Some Android monotonic clocks have a coarser tick than a LAN health GET.
	// An authenticated TLS response with the exact body length is still success.
	return max(time.Since(start), time.Nanosecond)
}

type packetCounters struct{ sent, lost uint64 }

func responseCounters(resp *http.Response) packetCounters {
	sent, err1 := strconv.ParseUint(resp.Header.Get("Nexus-QUIC-Sent"), 10, 64)
	lost, err2 := strconv.ParseUint(resp.Header.Get("Nexus-QUIC-Lost"), 10, 64)
	if err1 != nil || err2 != nil || lost > sent {
		return packetCounters{}
	}
	return packetCounters{sent, lost}
}

func (l *transportLane) localCounters() packetCounters {
	if l.packets != nil {
		return l.packets()
	}
	return packetCounters{}
}

func (p packetCounters) impairedSince(start packetCounters) bool {
	if start.sent == 0 || p.sent <= start.sent || p.lost < start.lost {
		return false
	}
	sent, lost := p.sent-start.sent, p.lost-start.lost
	// Three losses and at least 5% of sent packets distinguish sustained
	// congestion from occasional retransmission on an otherwise fast path.
	return lost >= 3 && lost >= 1+(sent-1)/20
}

type lossWindow struct {
	local, remote  packetCounters
	measured, next time.Time
}

func (w *lossWindow) impaired(ctx context.Context, l *transportLane) bool {
	if time.Now().Before(w.next) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 850*time.Millisecond)
	defer cancel()
	var remote packetCounters
	if resp, err := l.healthHead(ctx); err == nil && l.accepted(resp) == nil {
		remote = responseCounters(resp)
	}
	local := l.localCounters()
	now := time.Now()
	recent := now.Sub(w.measured) <= 6*time.Second
	impaired := recent && (remote.impairedSince(w.remote) || local.impairedSince(w.local))
	w.local, w.remote, w.measured = local, remote, now
	if recent && !impaired {
		w.next = now.Add(3 * time.Second)
	}
	return impaired
}

// Several equal, consecutive transfers put fast LAN measurements above coarse
// clock resolution and average out the latency of a single lost packet.
func (s *selector) capacityBatch(l *transportLane, count int, measured *packetCounters) time.Duration {
	ctx, cancel := context.WithTimeout(s.ctx, 850*time.Millisecond)
	defer cancel()
	if count == 1 {
		return s.capacityContext(ctx, l, measured)
	}
	start := time.Now()
	for i := range count {
		var snapshot *packetCounters
		if i == count-1 {
			snapshot = measured
		}
		if s.capacityContext(ctx, l, snapshot) == 0 {
			return 0
		}
	}
	return max(time.Since(start), time.Nanosecond)
}

// Compare equally loaded carriers. An idle H2 probe must not win solely
// because the old H3 connection is already serving a saturated download.
func (s *selector) slowerOnFreshH3(h2 *transportLane) (slow, lossy bool) {
	h3, err := s.probeH3(s.ctx)
	if err != nil {
		return false, false
	}
	defer h3.close()
	var lastLocal, lastRemote packetCounters
	// Authenticate and warm both connections with the identical object.
	for _, count := range [...]int{1, 8, 8} {
		var remote packetCounters
		done := make(chan time.Duration, 1)
		go func() { done <- s.capacityBatch(h3, count, &remote) }()
		h2time := s.capacityBatch(h2, count, nil)
		h3time := <-done
		if h2time == 0 || h3time == 0 {
			return false, false
		}
		local := h3.localCounters()
		if count > 1 {
			// A saturated business connection may drop packets locally while
			// concurrent UDP connections still make normal progress.
			lossy = remote.impairedSince(lastRemote) || local.impairedSince(lastLocal)
			// A whole batch inside a clock tick has no measured ratio.
			if !lossy || h2time == time.Nanosecond || !slowAndroidSample(h3time, h2time) {
				return false, lossy
			}
		}
		lastLocal, lastRemote = local, remote
	}
	return true, true
}

func (s *selector) probeH3(parent context.Context) (*transportLane, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	return s.dial(ctx, "h3")
}

func authenticatedHead(parent context.Context, l *transportLane, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	resp, err := l.healthHead(ctx)
	ok := err == nil && resp.StatusCode == http.StatusOK && l.accepted(resp) == nil
	if ok {
		trace("head_ok", l.kind, "")
	} else {
		trace("head_fail", l.kind, "")
	}
	return ok
}

func (s *selector) recoverH3() (*transportLane, bool) {
	s.recoveryMu.Lock()
	if s.recoveryBlocked.Load() {
		s.recoveryMu.Unlock()
		return nil, false
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancelRecovery = cancel
	s.recoveryMu.Unlock()
	defer func() {
		s.recoveryMu.Lock()
		cancel()
		s.cancelRecovery = nil
		s.recoveryMu.Unlock()
	}()
	// H2 is still serving traffic. Give the replacement the same handshake
	// budget as a cold dial, including a lost initial QUIC packet.
	handshake, finish := context.WithTimeout(ctx, 5*time.Second)
	q, err := s.dial(handshake, "h3")
	finish()
	if err != nil {
		return nil, false
	}
	// Active H2 remains available while both full authenticated objects arrive.
	check, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	return q, s.capacityContext(check, q, nil) > 0 && (!s.androidVPN || s.capacityContext(check, q, nil) > 0) && check.Err() == nil
}

func (s *selector) prebuildH2() (<-chan *transportLane, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
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
	var prev, previousStalls, previousDelivered uint64
	lastProgress := time.Now()
	var weak, success int
	var observed *transportLane
	var losses lossWindow
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
		l := s.current()
		if s.androidVPN && s.wake != nil {
			last := time.Unix(0, l.last.Load())
			sleep := l.active.Load() == 0 || l.opening.Load() == 0 && time.Since(last) > 3*time.Second
			if l.kind == "h2" {
				sleep = s.recoveryBlocked.Load() || time.Since(last) > 30*time.Second || nextProbe.After(last.Add(30*time.Second))
			}
			if sleep {
				discard()
				weak = 0
				tick.Stop()
				select {
				case <-s.ctx.Done():
					return
				case <-s.wake:
				}
				tick.Reset(500 * time.Millisecond)
				continue
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		}
		l = s.current()
		if observed != l {
			weak = 0
			prev = 0
			previousStalls = l.stalledReads.Load()
			previousDelivered = l.delivered.Load()
			lastProgress = time.Now()
			observed = l
			losses = lossWindow{}
		}
		if owner != l {
			discard()
		}
		if l.kind == "h2" {
			age := time.Since(time.Unix(0, l.last.Load()))
			if time.Now().Before(nextProbe) || s.recoveryBlocked.Load() || (s.androidVPN && age > 30*time.Second) || (!s.androidVPN && (l.active.Load() == 0 || age > 3*time.Second)) {
				continue
			}
			trace("recovery_start", "h3", "")
			q, ok := s.recoverH3()
			if ok && !s.recoveryBlocked.Load() {
				trace("recovery_ok", "h3", "")
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
				trace("recovery_fail", "h3", "")
				success = 0
				if !s.recoveryBlocked.Load() {
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
		delivered := l.delivered.Load()
		businessStalled := s.androidVPN && delivered == previousDelivered && now-prev < 32768
		previousDelivered = delivered
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
			// Two weak half-second windows trigger measurement below 16 Mbit/s.
			// QUIC totals include acknowledgements and retransmissions.
			// A busy downstream can deliver bursts at high average speed while
			// waiting for missing stream data. Compare paths when the application
			// actually waited for data in both sampling windows.
			stalls := l.stalledReads.Load()
			progress = delta >= 1<<20 && stalls == previousStalls
			previousStalls = stalls
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
		bursty := s.androidVPN && delta >= 1<<20
		if bursty && !losses.impaired(s.ctx, l) {
			continue
		}
		sample := make(chan time.Duration, 1)
		var reachable chan bool
		if businessStalled {
			// A one-way block can still deliver QUIC ACKs and retransmissions.
			// Test new round trips while the authenticated fallback is being built.
			reachable = make(chan bool, 1)
			go func() {
				ok := authenticatedHead(s.ctx, l, 850*time.Millisecond)
				if !ok && l.active.Load() > 0 && l.delivered.Load() == delivered {
					ok = authenticatedHead(s.ctx, l, 1700*time.Millisecond)
				}
				reachable <- ok
			}()
		} else if !bursty {
			go func() { sample <- s.capacity(l) }()
		}
		var alt *transportLane
		if pending != nil {
			alt = <-pending
			discard()
		} else if s.androidVPN || time.Since(lastH2) >= 10*time.Second || gone {
			// On Android, cooldown applies to speculative dials. Two silent windows while
			// an application needs the path must be able to recover immediately.
			lastH2 = time.Now()
			ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
			alt, _ = s.dial(ctx, "h2")
			cancel()
		}
		if alt == nil {
			continue
		}
		var degraded bool
		if businessStalled {
			verified := authenticatedHead(s.ctx, alt, 2*time.Second)
			responsive := <-reachable
			degraded = verified && !responsive && l.active.Load() > 0 && l.delivered.Load() == delivered
		} else if bursty {
			var lossy bool
			degraded, lossy = s.slowerOnFreshH3(alt)
			if !lossy {
				losses.next = time.Now().Add(3 * time.Second)
			}
		} else {
			h2time := s.capacity(alt)
			h3time := <-sample
			degraded = h2time > 0 && (h3time == 0 || h3time > 4*h2time)
			if s.androidVPN {
				degraded = slowAndroidSample(h3time, h2time)
				if degraded && !(h3time == 0 && l.stats() == now) {
					go func() { sample <- s.capacity(l) }()
					h2time = s.capacity(alt)
					h3time = <-sample
					degraded = slowAndroidSample(h3time, h2time)
				}
			}
		}
		if degraded {
			if labTrace {
				if businessStalled || l.stats() == now {
					trace("h3_failed", l.kind, "")
				} else {
					trace("h3_degraded", l.kind, "")
				}
			}
			if !s.set(l, alt) {
				continue
			}
			probeInterval = 30 * time.Second
			nextProbe = time.Now().Add(probeInterval)
			success = 0
			// A late QUIC packet cannot revive two failed round trips. Preserve
			// an old flow only if business data actually arrived during selection.
			closeOld := silentForTwoWindows(l, now, lastProgress)
			if businessStalled {
				closeOld = l.delivered.Load() == delivered
			}
			if closeOld {
				l.close()
				trace("old_closed", l.kind, "")
			} else {
				go s.drainH3(l, alt)
			}
		} else {
			alt.close()
		}
	}
}
func slowAndroidSample(h3, h2 time.Duration) bool {
	if h2 == time.Nanosecond {
		// capacity uses 1 ns for success within a single monotonic clock tick.
		// Only this unmeasurable sample needs a conservative timing floor.
		h2 = 2 * time.Millisecond
	}
	return h2 > 0 && (h3 == 0 || h3 > 4*h2)
}

func (s *selector) retire(l *transportLane) {
	for s.ctx.Err() == nil && l.active.Load() > 0 {
		time.Sleep(250 * time.Millisecond)
	}
	l.close()
	trace("old_closed", l.kind, "")
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
	return errors.As(e, &a) || errors.As(e, &h) || errors.As(e, &v) || (errors.As(e, &typed) && (typed.Code == Certificate || typed.Code == Authentication || typed.Code == Configuration))
}
func (s *selector) drainH3(old, backup *transportLane) {
	prev, since := old.stats(), time.Now()
	firstCheck := true
	for s.ctx.Err() == nil && old.active.Load() > 0 {
		time.Sleep(500 * time.Millisecond)
		if n := old.stats(); n != prev {
			prev, since = n, time.Now()
			continue
		}
		if time.Since(since) >= time.Second && backup.usable() && (firstCheck || !s.androidVPN || time.Since(time.Unix(0, old.last.Load())) <= 3*time.Second || old.opening.Load() > 0) {
			// A blocked response can outlive the three-second activity window.
			// Verify once even then. A healthy idle flow needs no repeated probes.
			firstCheck = false
			if s.capacity(old) == 0 && silentForTwoWindows(old, prev, since) && backup.usable() {
				old.close()
				trace("old_closed", old.kind, "")
				return
			}
			prev, since = old.stats(), time.Now()
		}
	}
	old.close()
	trace("old_closed", old.kind, "")
}
