package transport

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestReuseAuthenticatedH2WithoutReusingAuthorization(t *testing.T) {
	for _, scenario := range []string{"authorized", "revoked", "idle", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			var blocked atomic.Bool
			var heads atomic.Int32
			remotes := make(chan string, 4)
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead || r.ProtoMajor != 2 || r.Header.Get("Proxy-Authorization") == "" {
					t.Error("expected fresh H2 request with device proof")
				}
				remotes <- r.RemoteAddr
				if heads.Add(1) > 1 && scenario == "revoked" {
					w.WriteHeader(http.StatusProxyAuthRequired)
					return
				}
				w.Header().Set("Proxy-Authentication-Info", "Nexus2")
			}))
			srv.EnableHTTP2 = true
			srv.StartTLS()
			defer srv.Close()
			pin := sha256.Sum256(srv.Certificate().RawSubjectPublicKeyInfo)
			cfg := Config{Authority: srv.Listener.Addr().String(), Endpoint: srv.Listener.Addr().String(),
				HealthPath: "/health.bin", Sign: func([]byte) ([]byte, error) { return make([]byte, 64), nil }}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			var downloading atomic.Bool
			started := time.Now()
			h3 := &transportLane{kind: "h3", health: "https://node.test/health.bin", stats: func() uint64 {
				if downloading.Load() {
					return uint64(time.Since(started).Nanoseconds() / 100)
				}
				return 0
			}, close: func() {}}
			h3.active.Store(1)
			h3.opening.Store(1)
			healthy := make(chan struct{}, 1)
			h3.round = func(r *http.Request) (*http.Response, error) {
				if blocked.Load() {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				select {
				case healthy <- struct{}{}:
				default:
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Proxy-Authentication-Info": {"Nexus2"}}, Body: http.NoBody}, nil
			}
			var dials atomic.Int32
			closed := make(chan struct{}, 1)
			s := selector{ctx: ctx, lane: h3, events: make(chan string, 1), androidVPN: true}
			s.dial = func(ctx context.Context, kind string) (*transportLane, error) {
				dials.Add(1)
				l, err := dialLane(ctx, cfg, kind, pin[:])
				if err == nil {
					closeConn := l.close
					l.close = func() {
						closeConn()
						select {
						case closed <- struct{}{}:
						default:
						}
					}
				}
				return l, err
			}
			done := make(chan struct{})
			go func() { s.watch(); close(done) }()
			defer func() { cancel(); <-done; s.current().close() }()
			var remote string
			select {
			case remote = <-remotes:
			case <-ctx.Done():
				t.Fatal("initial authenticated H2 comparison missing")
			}
			select {
			case <-healthy:
			case <-ctx.Done():
				t.Fatal("initial H3 comparison missing")
			}
			checked := time.Now()
			blocked.Store(true)
			if scenario == "idle" {
				h3.active.Store(0)
			} else if scenario == "expired" {
				downloading.Store(true)
			}
			select {
			case <-s.events:
				if scenario != "authorized" {
					t.Fatal("selected H2 with stale authorization or no demand")
				}
			case <-closed:
				if scenario == "authorized" {
					t.Fatal("discarded usable H2")
				}
			case <-ctx.Done():
				t.Fatal("no turnover decision")
			}
			if scenario == "expired" && time.Since(checked) < 5*time.Second {
				t.Fatal("usable H2 closed before its short standby expired")
			}
			if scenario == "authorized" || scenario == "revoked" {
				select {
				case second := <-remotes:
					if second != remote {
						t.Fatal("did not reuse the same TLS connection")
					}
				default:
					t.Fatal("selected or rejected H2 without a fresh request")
				}
			}
			if dials.Load() != 1 || ((scenario == "idle" || scenario == "expired") && heads.Load() != 1) {
				t.Fatalf("unexpected dials=%d heads=%d", dials.Load(), heads.Load())
			}
		})
	}
}
