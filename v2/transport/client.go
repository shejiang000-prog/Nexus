// Package transport multiplexes device-authenticated TCP and UDP flows.
package transport

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	quic "github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/udp"
	"golang.org/x/net/http2"
)

type Config struct {
	Authority, Endpoint, Mode, HealthPath string
	Roots                                 *x509.CertPool
	ClientHello                           []byte
	Device                                proof.ID
	Sign                                  proof.Sign
	OnState                               func(string)
	Protect                               func(fd int) bool
	Resolve                               func(context.Context, string) ([]netip.Addr, error)
}
type Code uint8

const (
	Configuration Code = iota + 1
	Certificate
	Authentication
	Limit
	Destination
	Interrupted
	OutcomeUnknown
)

type Error struct {
	Code  Code
	Cause error
}

func (e *Error) Error() string { return fmt.Sprintf("Nexus transport %d: %v", e.Code, e.Cause) }
func (e *Error) Unwrap() error { return e.Cause }

type Client struct {
	s      *selector
	cancel context.CancelCauseFunc
	expiry *atomic.Int64
}

func Start(parent context.Context, c Config) (*Client, error) { return StartPinned(parent, c, nil) }

// StartPinned enforces a SHA-256 leaf SPKI pin on initial, replacement and recovery connections.
func StartPinned(parent context.Context, c Config, pin []byte) (*Client, error) {
	if len(pin) != 0 && len(pin) != 32 {
		return nil, &Error{Certificate, errors.New("invalid certificate pin")}
	}
	pin = append([]byte(nil), pin...)
	a, e := proof.Authority(c.Authority)
	if e != nil || c.Sign == nil {
		return nil, &Error{Configuration, errors.New("authority and signer required")}
	}
	c.Authority = a
	host, _, _ := net.SplitHostPort(a)
	if net.ParseIP(host) != nil && len(pin) != 32 {
		return nil, &Error{Certificate, errors.New("IP authority requires a certificate pin")}
	}
	if c.Endpoint == "" {
		c.Endpoint = a
	}
	if _, e = proof.Authority(c.Endpoint); e != nil {
		return nil, &Error{Configuration, e}
	}
	if c.Mode == "" {
		c.Mode = "auto"
	}
	if c.Mode != "auto" && c.Mode != "h2" && c.Mode != "h3" {
		return nil, &Error{Configuration, errors.New("unknown carrier")}
	}
	if c.HealthPath == "" {
		c.HealthPath = "/health.bin"
	}
	u, e := url.ParseRequestURI(c.HealthPath)
	if e != nil || !strings.HasPrefix(c.HealthPath, "/") || strings.HasPrefix(c.HealthPath, "//") || u.IsAbs() {
		return nil, &Error{Configuration, errors.New("invalid health path")}
	}
	ctx, cancel := context.WithCancelCause(parent)
	s := &selector{ctx: ctx, mode: c.Mode, androidVPN: runtime.GOOS == "android" && c.Protect != nil, events: make(chan string, 2)}
	if s.androidVPN {
		s.wake = make(chan struct{}, 1)
	}
	expiry := new(atomic.Int64)
	expiry.Store(-1)
	s.dial = func(ctx context.Context, kind string) (*transportLane, error) {
		l, err := dialLane(ctx, c, kind, pin)
		if err != nil {
			var typed *Error
			if errors.As(networkError(err), &typed) && typed.Code == Certificate {
				cancel(typed)
			}
		} else {
			l.wake = s.wake
			l.expiry = expiry
		}
		return l, err
	}
	l, e := s.initial()
	if cause := context.Cause(ctx); cause != nil {
		cancel(cause)
		if l != nil {
			l.close()
		}
		return nil, networkError(cause)
	}
	if e != nil {
		cancel(e)
		return nil, networkError(e)
	}
	s.lane = l
	s.events <- l.kind
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case kind := <-s.events:
				if c.OnState != nil {
					c.OnState(kind)
				}
			}
		}
	}()
	go func() { <-ctx.Done(); s.current().close() }()
	if c.Mode == "auto" {
		go s.watch()
	}
	return &Client{s: s, cancel: cancel, expiry: expiry}, nil
}
func (c *Client) Stop() { c.cancel(nil); c.s.current().close() }

// SetRecoveryAllowed controls background H3 recovery for this client.
func (c *Client) SetRecoveryAllowed(allowed bool) { c.s.setRecoveryAllowed(allowed) }
func (c *Client) State() string {
	if c.s.ctx.Err() != nil {
		return "stopped"
	}
	return c.s.current().kind
}
func (c *Client) acquire(business bool) (*transportLane, error) {
	s := c.s
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, networkError(context.Cause(s.ctx))
	}
	l := s.lane
	changed := false
	if !l.usable() {
		var next *transportLane
		var err error
		if s.mode == "auto" && l.kind == "h2" && time.Since(time.Unix(0, l.last.Load())) < 10*time.Minute {
			ctx, cancel := context.WithTimeout(s.ctx, 1500*time.Millisecond)
			next, err = s.dial(ctx, "h2")
			cancel()
		}
		if next == nil && (err == nil || !terminalDialError(err)) {
			next, err = s.initial()
		}
		if err != nil {
			s.mu.Unlock()
			if cause := context.Cause(s.ctx); cause != nil {
				return nil, networkError(cause)
			}
			return nil, networkError(err)
		}
		// A peer can send GOAWAY while earlier streams are still draining.
		go s.retire(l)
		l = next
		s.lane = l
		changed = true
	}
	if business {
		l.active.Add(1)
		l.opening.Add(1)
		l.business()
	}
	s.mu.Unlock()
	if changed {
		select {
		case s.events <- l.kind:
		case <-s.ctx.Done():
			if business {
				l.active.Add(-1)
				l.opening.Add(-1)
			}
			return nil, networkError(context.Cause(s.ctx))
		}
	}
	return l, nil
}
func networkError(e error) error {
	var a x509.UnknownAuthorityError
	var h x509.HostnameError
	var inv x509.CertificateInvalidError
	if errors.As(e, &a) || errors.As(e, &h) || errors.As(e, &inv) {
		return &Error{Certificate, e}
	}
	var typed *Error
	if errors.As(e, &typed) {
		return e
	}
	return &Error{Interrupted, e}
}
func dialLane(ctx context.Context, c Config, kind string, pin []byte) (*transportLane, error) {
	return dialEndpoint(ctx, c, func(ctx context.Context, peer netip.AddrPort) (*transportLane, error) {
		return dialAddress(ctx, c, kind, peer, pin)
	})
}
func dialAddress(ctx context.Context, c Config, kind string, peer netip.AddrPort, pin []byte) (_ *transportLane, err error) {
	name, _, _ := net.SplitHostPort(c.Authority)
	l := &transportLane{authority: c.Authority, kind: kind, health: "https://" + c.Authority + c.HealthPath}
	var export proof.Exporter
	defer func() {
		if err != nil && l.close != nil {
			l.close()
		}
	}()
	if kind == "h2" {
		sock, e := dialTCP(ctx, peer.String(), name, c.ClientHello, c.Roots, c.Protect, pin)
		if e != nil {
			return nil, e
		}
		l.close = sync.OnceFunc(func() { sock.Close() })
		state := sock.ConnectionState()
		export = state.ExportKeyingMaterial
		tr, e := http2.ConfigureTransports(&http.Transport{MaxResponseHeaderBytes: 262144, HTTP2: &http.HTTP2Config{MaxEncoderHeaderTableSize: 1, MaxDecoderHeaderTableSize: 65536, MaxReceiveBufferPerStream: 6291456, MaxReceiveBufferPerConnection: 15663105}})
		if e != nil {
			return nil, e
		}
		h, e := tr.NewClientConn(sock)
		if e != nil {
			return nil, e
		}
		l.usable = h.CanTakeNewRequest
		l.round = h.RoundTrip
	} else {
		q, closeAll, packets, e := dialQUIC(ctx, net.UDPAddrFromAddrPort(peer), name, c.Roots, c.Protect, pin)
		if e != nil {
			return nil, e
		}
		l.close = sync.OnceFunc(closeAll)
		l.dead = q.Context().Done()
		l.usable = func() bool { return q.Context().Err() == nil }
		l.stats = func() uint64 { return q.ConnectionStats().BytesReceived }
		l.packets = packets
		export = q.ExportKeyingMaterial
		l.h3 = (&http3.Transport{MaxResponseHeaderBytes: 262144, EnableDatagrams: true}).NewClientConn(q)
		l.round = l.h3.RoundTrip
	}
	secret, e := proof.Secret(c.Authority, export)
	if e != nil {
		return nil, e
	}
	l.proof, e = proof.Create(secret, c.Device, c.Sign)
	if e != nil {
		return nil, &Error{Authentication, e}
	}
	return l, nil
}
func (l *transportLane) headers(r *http.Request) {
	r.Header = make(http.Header)
	r.Header.Set("User-Agent", "")
	if !l.ack.Load() {
		r.Header.Set("Proxy-Authorization", l.proof)
	}
}

// AccountExpiry is -1 until an authenticated response supplies a deadline; zero means unlimited.
func (c *Client) AccountExpiry() int64 { return c.expiry.Load() }

// Confirm verifies the current device on demand without marking the carrier as busy.
func (c *Client) Confirm() error {
	l, err := c.acquire(false)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.s.ctx, 3*time.Second)
	defer cancel()
	resp, err := l.healthHead(ctx)
	if err != nil {
		return networkError(err)
	}
	if resp.StatusCode == 200 && resp.Header.Get("Proxy-Authentication-Info") == "" {
		// Older nodes serve a public file. Only an acknowledged CONNECT proves identity.
		a, err := l.openUDP(ctx, "1.1.1.1:53", func() {})
		if err != nil {
			return err
		}
		return a.Close()
	}
	return l.accepted(resp)
}
func (l *transportLane) healthHead(ctx context.Context) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, l.health, nil)
	if err != nil {
		return nil, err
	}
	l.healthHeaders(req)
	resp, err := l.round(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	return resp, nil
}
func (l *transportLane) healthHeaders(r *http.Request) {
	l.headers(r)
	// Ordinary GETs on this same connection still belong to the website.
	r.Header.Set("Proxy-Authorization", l.proof)
}
func (l *transportLane) accepted(r *http.Response) error {
	if r.StatusCode >= 200 && r.StatusCode < 300 && r.Header.Get("Proxy-Authentication-Info") == "Nexus2" {
		if value := r.Header.Get("Nexus-Account-Expires"); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 {
				return &Error{Authentication, errors.New("invalid authenticated account expiry")}
			}
			l.expiry.Store(n)
		}
		l.ack.Store(true)
		return nil
	}
	code := Authentication
	switch r.StatusCode {
	case 403, 502, 504:
		code = Destination
	case 429, 503:
		code = Limit
	}
	return &Error{code, errors.New("request rejected (" + strconv.Itoa(r.StatusCode) + ")")}
}

type stream struct {
	in    io.ReadCloser
	out   *io.PipeWriter
	close func()
	l     *transportLane
}

func (s *stream) Read(p []byte) (int, error) {
	var start time.Time
	if s.l.kind == "h3" && s.l.wake != nil {
		start = time.Now()
	}
	n, e := s.in.Read(p)
	if e != nil {
		s.out.CloseWithError(io.ErrClosedPipe)
	}
	if n > 0 {
		if !start.IsZero() && time.Since(start) >= 10*time.Millisecond {
			s.l.stalledReads.Add(1)
		}
		s.l.business()
	}
	return n, e
}
func (s *stream) Write(p []byte) (int, error) {
	n, e := s.out.Write(p)
	if n > 0 {
		s.l.business()
	}
	return n, e
}
func (s *stream) CloseWrite() error { return s.out.Close() }
func (s *stream) Close() error      { s.close(); return nil }

// initial is optional optimistic data. No automatic replay is performed.
func (c *Client) DialTCP(ctx context.Context, target string, initial []byte) (io.ReadWriteCloser, error) {
	a, e := proof.Authority(target)
	if e != nil || len(initial) > 16384 {
		return nil, &Error{Configuration, errors.New("invalid target or initial length")}
	}
	l, e := c.acquire(true)
	if e != nil {
		return nil, e
	}
	defer l.opening.Add(-1)
	ctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	req := (&http.Request{Method: "CONNECT", Host: a, URL: &url.URL{Scheme: "https", Host: a}, Body: pr, ContentLength: -1}).WithContext(ctx)
	l.headers(req)
	done := make(chan error, 1)
	if len(initial) > 0 {
		data := append([]byte(nil), initial...)
		go func() { _, e := pw.Write(data); done <- e }()
	}
	resp, e := l.round(req)
	if e == nil {
		e = l.accepted(resp)
	}
	if e != nil {
		cancel()
		pr.Close()
		pw.Close()
		if resp != nil {
			resp.Body.Close()
		}
		l.active.Add(-1)
		if len(initial) > 0 && resp == nil {
			return nil, &Error{OutcomeUnknown, e}
		}
		return nil, networkError(e)
	}
	s := &stream{in: resp.Body, out: pw, l: l}
	s.close = sync.OnceFunc(func() { cancel(); pw.Close(); pr.Close(); resp.Body.Close(); l.active.Add(-1) })
	if len(initial) > 0 {
		select {
		case e = <-done:
		case <-ctx.Done():
			e = ctx.Err()
		}
		if e != nil {
			s.Close()
			return nil, networkError(e)
		}
	}
	return s, nil
}

type Association struct {
	a *udp.Association
	l *transportLane
}

func (a *Association) ReadPacket() ([]byte, error) {
	p, e := a.a.ReadPacket()
	if e == nil {
		a.l.business()
	}
	return p, e
}
func (a *Association) WritePacket(p []byte) error {
	e := a.a.WritePacket(p)
	if e == nil {
		a.l.business()
	}
	return e
}
func (a *Association) Close() error { return a.a.Close() }
func (c *Client) OpenUDP(ctx context.Context, target string) (*Association, error) {
	a, e := proof.Authority(target)
	if e != nil {
		return nil, &Error{Configuration, e}
	}
	l, e := c.acquire(true)
	if e != nil {
		return nil, e
	}
	defer l.opening.Add(-1)
	return l.openUDP(ctx, a, func() { l.active.Add(-1) })
}
func (l *transportLane) openUDP(ctx context.Context, a string, release func()) (*Association, error) {
	ctx, cancel := context.WithCancel(ctx)
	h, p, _ := net.SplitHostPort(a)
	authority := l.authority
	req, e := http.NewRequestWithContext(ctx, "CONNECT", "https://"+authority+udp.Path+url.PathEscape(h)+"/"+p+"/", nil)
	if e != nil {
		cancel()
		release()
		return nil, e
	}
	l.headers(req)
	req.Header.Set("Capsule-Protocol", "?1")
	var r io.Reader
	var w io.Writer
	var d udp.Datagrams
	var resp *http.Response
	closeStream := func() {}
	if l.h3 != nil {
		select {
		case <-ctx.Done():
			e = ctx.Err()
		case <-l.h3.ReceivedSettings():
		}
		if e == nil && !l.h3.Settings().EnableExtendedConnect {
			e = errors.New("H3 extended CONNECT unavailable")
		}
		if e == nil {
			var s *http3.RequestStream
			s, e = l.h3.OpenRequestStream(ctx)
			if e == nil {
				closeStream = func() { s.CancelRead(0); s.Close() }
				req.Proto = "connect-udp"
				e = s.SendRequestHeader(req)
				if e == nil {
					resp, e = s.ReadResponse()
				}
				r = s
				w = s
				if l.h3.Settings().EnableDatagrams {
					d = s
				}
			}
		}
	} else {
		pr, pw := io.Pipe()
		req.Body = pr
		req.ContentLength = -1
		req.Host = a
		req.URL = &url.URL{Scheme: "https", Host: a}
		req.Header.Set(udp.H2FlowHeader, "udp")
		resp, e = l.round(req)
		closeStream = func() {
			pw.Close()
			pr.Close()
			if resp != nil {
				resp.Body.Close()
			}
		}
		w = pw
		if resp != nil {
			r = resp.Body
		}
	}
	if e == nil {
		e = l.accepted(resp)
		if e == nil && resp.Header.Get("Capsule-Protocol") != "?1" {
			e = errors.New("capsule negotiation failed")
		}
	}
	if e != nil {
		cancel()
		closeStream()
		release()
		return nil, networkError(e)
	}
	channel := udp.New(ctx, r, w, d, func(e error) bool { var x *quic.DatagramTooLargeError; return errors.As(e, &x) }, func() { cancel(); closeStream(); release() })
	return &Association{channel, l}, nil
}
