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
	"strconv"
	"strings"
	"sync"
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
	cancel context.CancelFunc
}

func Start(parent context.Context, c Config) (*Client, error) {
	a, e := proof.Authority(c.Authority)
	if e != nil || c.Sign == nil {
		return nil, &Error{Configuration, errors.New("authority and signer required")}
	}
	c.Authority = a
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
	ctx, cancel := context.WithCancel(parent)
	s := &selector{ctx: ctx, mode: c.Mode, events: make(chan string, 2)}
	s.dial = func(ctx context.Context, kind string) (*transportLane, error) { return dialLane(ctx, c, kind) }
	l, e := s.initial()
	if e != nil {
		cancel()
		return nil, networkError(e)
	}
	s.lane = l
	l.last.Store(time.Now().UnixNano())
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
	return &Client{s: s, cancel: cancel}, nil
}
func (c *Client) Stop() { c.cancel(); c.s.current().close() }
func (c *Client) State() string {
	if c.s.ctx.Err() != nil {
		return "stopped"
	}
	return c.s.current().kind
}
func (c *Client) acquire() (*transportLane, error) {
	s := c.s
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, &Error{Interrupted, s.ctx.Err()}
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
			return nil, networkError(err)
		}
		// A peer can send GOAWAY while earlier streams are still draining.
		go s.retire(l)
		l = next
		s.lane = l
		changed = true
	}
	l.active.Add(1)
	l.opening.Add(1)
	l.last.Store(time.Now().UnixNano())
	s.mu.Unlock()
	if changed {
		select {
		case s.events <- l.kind:
		case <-s.ctx.Done():
			l.active.Add(-1)
			l.opening.Add(-1)
			return nil, &Error{Interrupted, s.ctx.Err()}
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
func dialLane(ctx context.Context, c Config, kind string) (*transportLane, error) {
	return dialEndpoint(ctx, c, func(ctx context.Context, peer netip.AddrPort) (*transportLane, error) {
		return dialAddress(ctx, c, kind, peer)
	})
}
func dialAddress(ctx context.Context, c Config, kind string, peer netip.AddrPort) (_ *transportLane, err error) {
	name, _, _ := net.SplitHostPort(c.Authority)
	l := &transportLane{authority: c.Authority, kind: kind, health: "https://" + c.Authority + c.HealthPath}
	var export proof.Exporter
	defer func() {
		if err != nil && l.close != nil {
			l.close()
		}
	}()
	if kind == "h2" {
		sock, e := dialTCP(ctx, peer.String(), name, c.ClientHello, c.Roots, c.Protect)
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
		q, closeAll, e := dialQUIC(ctx, net.UDPAddrFromAddrPort(peer), name, c.Roots, c.Protect)
		if e != nil {
			return nil, e
		}
		l.close = sync.OnceFunc(closeAll)
		l.dead = q.Context().Done()
		l.usable = func() bool { return q.Context().Err() == nil }
		l.stats = func() uint64 { return q.ConnectionStats().BytesReceived }
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
func (l *transportLane) accepted(r *http.Response) error {
	if r.StatusCode >= 200 && r.StatusCode < 300 && r.Header.Get("Proxy-Authentication-Info") == "Nexus2" {
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
	return &Error{code, errors.New("CONNECT rejected (" + strconv.Itoa(r.StatusCode) + ")")}
}

type stream struct {
	in    io.ReadCloser
	out   *io.PipeWriter
	close func()
	l     *transportLane
}

func (s *stream) Read(p []byte) (int, error) {
	n, e := s.in.Read(p)
	if n > 0 {
		s.l.last.Store(time.Now().UnixNano())
	}
	return n, e
}
func (s *stream) Write(p []byte) (int, error) {
	n, e := s.out.Write(p)
	if n > 0 {
		s.l.last.Store(time.Now().UnixNano())
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
	l, e := c.acquire()
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
		a.l.last.Store(time.Now().UnixNano())
	}
	return p, e
}
func (a *Association) WritePacket(p []byte) error {
	e := a.a.WritePacket(p)
	if e == nil {
		a.l.last.Store(time.Now().UnixNano())
	}
	return e
}
func (a *Association) Close() error { return a.a.Close() }
func (c *Client) OpenUDP(ctx context.Context, target string) (*Association, error) {
	a, e := proof.Authority(target)
	if e != nil {
		return nil, &Error{Configuration, e}
	}
	l, e := c.acquire()
	if e != nil {
		return nil, e
	}
	defer l.opening.Add(-1)
	ctx, cancel := context.WithCancel(ctx)
	h, p, _ := net.SplitHostPort(a)
	authority := l.authority
	req, e := http.NewRequestWithContext(ctx, "CONNECT", "https://"+authority+udp.Path+url.PathEscape(h)+"/"+p+"/", nil)
	if e != nil {
		cancel()
		l.active.Add(-1)
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
		l.active.Add(-1)
		return nil, networkError(e)
	}
	channel := udp.New(ctx, r, w, d, func(e error) bool { var x *quic.DatagramTooLargeError; return errors.As(e, &x) }, func() { cancel(); closeStream(); l.active.Add(-1) })
	return &Association{channel, l}, nil
}
