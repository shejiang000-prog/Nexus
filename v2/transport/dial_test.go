package transport

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	quic "github.com/apernet/quic-go"
)

// Receiving keys after ServerHello is useful for scheduling, even when the
// handshake subsequently fails pin verification. Only a completed dial may win.
func TestRealColdHandshakeProgress(t *testing.T) {
	fixture := httptest.NewTLSServer(nil)
	pin := sha256.Sum256(fixture.Certificate().RawSubjectPublicKeyInfo)
	listener, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{Certificates: fixture.TLS.Certificates, NextProtos: []string{"h3"}}, nil)
	fixture.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peer := listener.Addr().(*net.UDPAddr)
	for _, valid := range []bool{true, false} {
		leaf := pin
		if !valid {
			leaf[0] ^= 0xff
		}
		var progress atomic.Bool
		ctx := context.WithValue(t.Context(), coldH3ProgressKey{}, &progress)
		_, closeAll, _, err := dialQUIC(ctx, peer, "127.0.0.1", nil, nil, leaf[:])
		if closeAll != nil {
			closeAll()
		}
		if valid && (!progress.Load() || err != nil) {
			t.Fatalf("no verified QUIC progress: %v", err)
		}
		if !valid {
			var failure *Error
			if !errors.As(err, &failure) || failure.Code != Certificate {
				t.Fatalf("invalid peer accepted: %v", err)
			}
		}
	}
}

func TestProtectBeforeAttempts(t *testing.T) {
	var calls atomic.Int32
	cfg := Config{Authority: "nexus.test:443", Endpoint: "127.0.0.1:443", Protect: func(fd int) bool {
		if fd < 0 {
			t.Error("invalid fd")
		}
		calls.Add(1)
		return false
	}}
	s := selector{ctx: t.Context(), dial: func(ctx context.Context, mode string) (*transportLane, error) { return dialLane(ctx, cfg, mode, nil) }}
	for _, mode := range []string{"h2", "h3"} {
		s.mode = mode
		_, err := s.initial()
		var e *Error
		if !errors.As(err, &e) || e.Code != Configuration {
			t.Fatalf("%s: %v", mode, err)
		}
	}
	ready, cancel := s.prebuildH2()
	defer cancel()
	if <-ready != nil {
		t.Fatal("unprotected H2")
	}
	_, err := s.probeH3(s.ctx)
	var e *Error
	if !errors.As(err, &e) || e.Code != Configuration || calls.Load() != 4 {
		t.Fatalf("probe %v, calls %d", err, calls.Load())
	}
}

func TestEndpointResolver(t *testing.T) {
	ips := []netip.Addr{netip.IPv6Loopback(), netip.MustParseAddr("127.0.0.1")}
	cfg := Config{Endpoint: "node.invalid:443", Resolve: func(ctx context.Context, host string) ([]netip.Addr, error) {
		if host != "node.invalid" {
			t.Fatal(host)
		}
		return ips, nil
	}}
	var count int
	_, err := dialEndpoint(t.Context(), cfg, func(ctx context.Context, p netip.AddrPort) (*transportLane, error) {
		if p.Addr() != ips[count] || p.Port() != 443 {
			t.Fatal(p)
		}
		count++
		if count == 1 {
			return nil, errors.New("no IPv6 route")
		}
		return &transportLane{}, nil
	})
	if err != nil || count != 2 {
		t.Fatalf("attempts %d: %v", count, err)
	}
	for _, mode := range []string{"h2", "h3"} {
		cfg.Authority = "example.test:443"
		cfg.Protect = func(int) bool { return false }
		_, err := dialLane(t.Context(), cfg, mode, nil)
		var e *Error
		if !errors.As(err, &e) || e.Code != Configuration {
			t.Fatalf("%s failed before protected socket: %v", mode, err)
		}
		for _, failure := range []error{nil, errors.New("DNS unavailable")} {
			invalid := cfg
			invalid.Resolve = func(context.Context, string) ([]netip.Addr, error) { return nil, failure }
			_, err := dialLane(t.Context(), invalid, mode, nil)
			if !errors.As(err, &e) || e.Code != Interrupted {
				t.Fatalf("%s resolve: %v", mode, err)
			}
		}
	}
}
func TestOptimisticFailure(t *testing.T) {
	for status, code := range map[int]Code{0: OutcomeUnknown, 200: Authentication, 403: Destination, 429: Limit, 502: Destination} {
		lane := &transportLane{usable: func() bool { return true }, round: func(r *http.Request) (*http.Response, error) {
			if ua, ok := r.Header["User-Agent"]; !ok || len(ua) != 1 || ua[0] != "" {
				t.Error("automatic User-Agent enabled")
			}
			if status == 0 {
				return nil, io.EOF
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
		}}
		c := Client{s: &selector{ctx: t.Context(), lane: lane}}
		_, err := c.DialTCP(t.Context(), "destination.test:443", []byte("test only"))
		var e *Error
		if !errors.As(err, &e) || e.Code != code {
			t.Fatalf("%d: %v", status, err)
		}
	}
}
