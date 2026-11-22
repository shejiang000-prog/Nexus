package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	quic "github.com/apernet/quic-go"
)

func TestProtectBeforeAttempts(t *testing.T) {
	var calls atomic.Int32
	cfg := Config{Authority: "nexus.test:443", Endpoint: "127.0.0.1:443", Protect: func(fd int) bool {
		if fd < 0 {
			t.Error("invalid fd")
		}
		calls.Add(1)
		return false
	}}
	s := selector{ctx: t.Context(), dial: func(ctx context.Context, mode string) (*transportLane, error) { return dialLane(ctx, cfg, mode) }}
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
	_, err := s.probeH3()
	var e *Error
	if !errors.As(err, &e) || e.Code != Configuration || calls.Load() != 4 {
		t.Fatalf("probe %v, calls %d", err, calls.Load())
	}
}

func TestQUICAddressFallback(t *testing.T) {
	ips := []netip.Addr{netip.IPv6Loopback(), netip.MustParseAddr("127.0.0.1")}
	count := 0
	_, _, e := dialQUICAddresses(t.Context(), ips, 443, func(ctx context.Context, p *net.UDPAddr) (*quic.Conn, func(), error) {
		count++
		if p.IP.To4() == nil {
			return nil, nil, errors.New("no IPv6 route")
		}
		return new(quic.Conn), func() {}, nil
	})
	if e != nil || count != 2 {
		t.Fatalf("attempts %d: %v", count, e)
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
