package transport

import (
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestFallbackAfterCapacityBodyTimeout(t *testing.T) {
	for _, scenario := range []string{"verified-headers", "missing-proof", "no-headers", "switch-revoked", "h3-answered"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				start := time.Now()
				var h3Heads, h2Heads, dials atomic.Int32
				h3 := &transportLane{kind: "h3", health: "https://node.test/health.bin",
					stats: func() uint64 { return uint64(time.Since(start).Milliseconds()) * 100 }, close: func() {}}
				h3.active.Store(1)
				h3.opening.Store(1)
				h3.round = func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodHead {
						h3Heads.Add(1)
					} else if scenario == "h3-answered" {
						reader, writer := io.Pipe()
						go func() { <-r.Context().Done(); writer.CloseWithError(r.Context().Err()) }()
						return &http.Response{StatusCode: 200, Header: http.Header{"Proxy-Authentication-Info": {"Nexus2"}}, Body: reader}, nil
					}
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				closed := make(chan struct{}, 1)
				kept := make(chan struct{}, 1)
				s := selector{ctx: ctx, lane: h3, events: make(chan string, 1), androidVPN: true}
				s.dial = func(_ context.Context, kind string) (*transportLane, error) {
					dials.Add(1)
					l := &transportLane{kind: kind, health: h3.health, usable: func() bool {
						select {
						case kept <- struct{}{}:
						default:
						}
						return true
					}, close: func() { closed <- struct{}{} }}
					l.round = func(r *http.Request) (*http.Response, error) {
						resp := &http.Response{StatusCode: 200, Header: http.Header{"Proxy-Authentication-Info": {"Nexus2"}}, Body: http.NoBody}
						if r.Method == http.MethodHead {
							h2Heads.Add(1)
							if scenario == "switch-revoked" {
								resp.StatusCode = http.StatusProxyAuthRequired
							}
							return resp, nil
						}
						switch scenario {
						case "no-headers":
							<-r.Context().Done()
							return nil, r.Context().Err()
						case "missing-proof":
							resp.Header.Del("Proxy-Authentication-Info")
						default:
							reader, writer := io.Pipe()
							resp.Body = reader
							go func() { <-r.Context().Done(); writer.CloseWithError(r.Context().Err()) }()
						}
						return resp, nil
					}
					return l, nil
				}
				done := make(chan struct{})
				go func() { s.watch(); close(done) }()
				select {
				case <-s.events:
					if scenario != "verified-headers" {
						t.Fatal("selected H2 without valid current authentication")
					}
				case <-closed:
					if scenario == "verified-headers" || scenario == "h3-answered" {
						t.Fatal("discarded H2 on its capacity read deadline")
					}
				case <-kept:
					if scenario != "h3-answered" || h3Heads.Load() != 0 {
						t.Fatal("unexpected fallback probe or retention")
					}
				case <-time.After(6 * time.Second):
					t.Fatal("no fallback decision")
				}
				cancel()
				<-done
				if dials.Load() != 1 {
					t.Fatalf("H2 dials %d", dials.Load())
				}
				if scenario == "verified-headers" || scenario == "switch-revoked" {
					if h3Heads.Load() != 2 || h2Heads.Load() != 1 {
						t.Fatal("fresh liveness checks missing")
					}
				} else if h2Heads.Load() != 0 {
					t.Fatal("retained candidate without authenticated GET headers")
				}
			})
		})
	}
}

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
