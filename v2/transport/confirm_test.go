package transport

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// A public file on an older node cannot authenticate an event-driven check.
func TestConfirmLegacy(t *testing.T) {
	for _, tc := range []struct {
		status  int
		ack, ok bool
	}{
		{200, true, true}, {200, false, false}, {403, false, false},
	} {
		expiry := new(atomic.Int64)
		expiry.Store(-1)
		calls := 0
		l := &transportLane{kind: "h2", authority: "nexus.test:443", health: "https://nexus.test/health.bin", proof: "device-proof", usable: func() bool { return true }, expiry: expiry}
		l.round = func(req *http.Request) (*http.Response, error) {
			calls++
			if req.Header.Get("Proxy-Authorization") != l.proof {
				t.Fatal("proof missing")
			}
			resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}
			switch req.Method {
			case "HEAD":
				if req.URL.Path != "/health.bin" {
					t.Fatal("wrong confirm URL")
				}
			case "CONNECT":
				if req.Host != "1.1.1.1:53" {
					t.Fatal("wrong legacy destination")
				}
				resp.StatusCode = tc.status
				resp.Header.Set("Capsule-Protocol", "?1")
				if tc.ack {
					resp.Header.Set("Proxy-Authentication-Info", "Nexus2")
				}
			default:
				t.Fatal("unexpected request")
			}
			return resp, nil
		}
		c := &Client{s: &selector{ctx: t.Context(), lane: l}, expiry: expiry}
		err := c.Confirm()
		if (err == nil) != tc.ok || calls != 2 || c.AccountExpiry() != -1 || l.last.Load() != 0 || l.opening.Load() != 0 || l.active.Load() != 0 {
			t.Fatalf("legacy %v: %v; calls %d", tc, err, calls)
		}
		if tc.status == 403 {
			var e *Error
			if !errors.As(err, &e) || e.Code != Destination {
				t.Fatalf("expected policy rejection: %v", err)
			}
		}
	}
}
