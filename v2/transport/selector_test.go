package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestAutoColdStart(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		s := selector{ctx: t.Context(), mode: "auto", dial: func(ctx context.Context, kind string) (*transportLane, error) {
			if blocked && kind == "h3" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &transportLane{kind: kind, close: func() {}}, nil
		}}
		start := time.Now()
		lane, err := s.initial()
		if err != nil || (lane.kind == "h2") != blocked || time.Since(start) > 3*time.Second {
			t.Fatalf("blocked=%t selected=%v err=%v", blocked, lane, err)
		}
	}
}

func TestAndroidOneWayH3Failure(t *testing.T) {
	for _, scenario := range []string{"control-only", "second-head-succeeds", "business-reply"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				answer := func() *http.Response {
					return &http.Response{StatusCode: 200, Header: http.Header{"Proxy-Authentication-Info": {"Nexus2"}}, Body: http.NoBody}
				}
				var receipts atomic.Uint64
				h3 := &transportLane{kind: "h3", health: "https://node.test/health.bin", stats: receipts.Load, close: func() {}}
				h3.active.Store(1)
				h3.opening.Store(1)
				var attempts atomic.Int32
				h3.round = func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodHead {
						t.Error("expected a new authenticated round trip")
					}
					if attempts.Add(1) == 2 {
						switch scenario {
						case "second-head-succeeds":
							return answer(), nil
						case "business-reply":
							h3.delivered.Add(1)
						}
					}
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				go func() {
					for {
						select {
						case <-ctx.Done():
							return
						case <-time.After(200 * time.Millisecond):
							receipts.Add(80) // Old server packets still cross the one-way block.
						}
					}
				}()
				discarded := make(chan struct{}, 1)
				var dials atomic.Int32
				s := selector{ctx: ctx, androidVPN: true, lane: h3, events: make(chan string, 1)}
				s.dial = func(ctx context.Context, kind string) (*transportLane, error) {
					dials.Add(1)
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(1150 * time.Millisecond):
					}
					return &transportLane{kind: kind, health: h3.health, usable: func() bool { return true },
						round: func(*http.Request) (*http.Response, error) { return answer(), nil },
						close: func() { discarded <- struct{}{} }}, nil
				}
				watched := make(chan struct{})
				go func() { s.watch(); close(watched) }()
				select {
				case <-s.events:
					if scenario != "control-only" {
						t.Fatalf("downgraded %s", scenario)
					}
				case <-discarded:
					if scenario == "control-only" {
						t.Fatal("discarded working H2 on blocked H3")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("no selection decision within five seconds")
				}
				if dials.Load() != 1 || attempts.Load() != 2 {
					t.Fatalf("H2 dials=%d, H3 challenges=%d", dials.Load(), attempts.Load())
				}
				h3.active.Store(0)
				cancel()
				<-watched
				time.Sleep(time.Second) // let the retiring lane observe the closed flow
			})
		})
	}
}

func TestCertificateFailureStopsColdRace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var h2 atomic.Int32
		s := selector{ctx: t.Context(), mode: "auto", dial: func(_ context.Context, kind string) (*transportLane, error) {
			if kind == "h2" {
				h2.Add(1)
				return &transportLane{kind: kind, close: func() {}}, nil
			}
			return nil, &Error{Certificate, errors.New("pin mismatch")}
		}}
		lane, err := s.initial()
		var typed *Error
		if lane != nil || !errors.As(err, &typed) || typed.Code != Certificate {
			t.Fatalf("wrong result: %v", err)
		}
		time.Sleep(time.Second)
		if h2.Load() != 0 {
			t.Fatal("started H2 after H3 certificate failure")
		}
	})
}

func TestAutoBlockedAfterHealthyProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var blocked atomic.Bool
		var held atomic.Pointer[transportLane]
		var h2Closed atomic.Bool
		var acknowledge atomic.Bool
		var h2Dials, h3Dials atomic.Int32
		var received atomic.Uint64
		health := func(r *http.Request) (*http.Response, error) {
			time.Sleep(time.Millisecond)
			return &http.Response{StatusCode: 200, Header: http.Header{"Proxy-Authentication-Info": {"Nexus2"}}, Body: io.NopCloser(bytes.NewReader(make([]byte, 32768)))}, nil
		}
		h3 := &transportLane{kind: "h3", health: "https://node.test/health.bin", stats: received.Load, close: func() {}}
		h3.round = func(r *http.Request) (*http.Response, error) {
			if blocked.Load() {
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			received.Add(32768)
			return health(r)
		}
		h3.active.Store(1)
		h3.opening.Store(1)
		s := selector{ctx: ctx, androidVPN: true, lane: h3, events: make(chan string, 4), dial: func(ctx context.Context, kind string) (*transportLane, error) {
			if kind == "h2" {
				h2Dials.Add(1)
				// A speculative candidate may close independently of the selected one.
				candidate := &transportLane{kind: "h2", health: h3.health, round: func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodGet {
						// A reachable H2 can still miss the 32 KiB capacity deadline.
						<-r.Context().Done()
						return nil, r.Context().Err()
					}
					resp, err := health(r)
					if !acknowledge.Load() {
						resp.Header.Del("Proxy-Authentication-Info")
					}
					return resp, err
				}}
				candidate.close = func() {
					if held.Load() == candidate {
						h2Closed.Store(true)
					}
				}
				return candidate, nil
			}
			h3Dials.Add(1)
			if blocked.Load() {
				return nil, errors.New("UDP blocked")
			}
			return h3, nil
		}}
		go s.watch()
		time.Sleep(2 * time.Second)
		if h2Dials.Load() == 0 || s.current() != h3 {
			t.Fatal("healthy small stream was downgraded or was never checked")
		}

		// The very next request stalls, within the previous prebuild's cooldown.
		// It is still waiting for its CONNECT headers even if last I/O is old.
		blocked.Store(true)
		time.Sleep(3 * time.Second)
		if s.current() != h3 {
			t.Fatal("unauthenticated H2 health selected")
		}
		acknowledge.Store(true)
		select {
		case kind := <-s.events:
			if kind != "h2" {
				t.Fatalf("blocked path selected %s", kind)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("blocked path waited for speculative cooldown")
		}
		liveH2 := s.current()
		held.Store(liveH2)
		// No completed business on the new H2 lane, so its dwell must expire quietly.
		time.Sleep(31 * time.Second)
		if h3Dials.Load() != 0 {
			t.Fatalf("idle H2 probes = %d", h3Dials.Load())
		}
		blocked.Store(false)
		liveH2.business()
		liveH2.active.Store(1) // keep an old stream across recovery
		select {
		case kind := <-s.events:
			if kind != "h3" {
				t.Fatalf("recovered path selected %s", kind)
			}
		case <-time.After(31 * time.Second):
			t.Fatal("restored UDP did not recover on the next background probe")
		}
		time.Sleep(time.Second)
		if h2Closed.Load() {
			t.Fatal("held H2 stream interrupted")
		}
		liveH2.active.Store(0)
		time.Sleep(time.Second)
		if !h2Closed.Load() {
			t.Fatal("drained H2 lane left open")
		}
	})
}

func TestSlowDownloadAndIdleRecoveryOutsideAndroidVPN(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var received atomic.Uint64
		var ended atomic.Bool
		response := func(delay time.Duration) func(*http.Request) (*http.Response, error) {
			return func(*http.Request) (*http.Response, error) {
				time.Sleep(delay)
				return &http.Response{StatusCode: 200, Header: http.Header{"Proxy-Authentication-Info": {"Nexus2"}}, Body: io.NopCloser(bytes.NewReader(make([]byte, 32768)))}, nil
			}
		}
		h3 := &transportLane{kind: "h3", health: "https://node.test/health.bin", stats: received.Load,
			close: func() { ended.Store(true) }, round: response(250 * time.Millisecond)}
		h3.active.Store(1)
		h3.last.Store(time.Now().UnixNano())
		h2 := &transportLane{kind: "h2", health: h3.health, close: func() {}, usable: func() bool { return true }, round: response(time.Millisecond)}
		var h3Dials atomic.Int32
		s := selector{ctx: ctx, lane: h3, events: make(chan string, 1), dial: func(ctx context.Context, kind string) (*transportLane, error) {
			if kind == "h3" {
				h3Dials.Add(1)
				return nil, errors.New("unexpected idle probe")
			}
			return h2, nil
		}}
		go func() {
			tick := time.NewTicker(100 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					received.Add(1250) // 100 kbit/s of continuous download
					h3.last.Store(time.Now().UnixNano())
				}
			}
		}()
		go s.watch()
		select {
		case kind := <-s.events:
			if kind != "h2" {
				t.Fatalf("slow download selected %s", kind)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("slow UDP with positive receive progress never switched")
		}
		time.Sleep(90 * time.Second)
		if ended.Load() || h3Dials.Load() != 0 {
			t.Fatal("live slow H3 download closed or idle H2 probed")
		}
		h3.active.Store(0)
		time.Sleep(time.Second)
		if !ended.Load() {
			t.Fatal("drained H3 did not close")
		}
	})
}

func TestHealthWithinOneClockTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := selector{ctx: t.Context()}
		for _, tc := range []struct {
			status, size int
			ok           bool
		}{
			{200, 32768, true}, {200, 32767, false}, {404, 32768, false},
		} {
			lane := &transportLane{health: "https://node.test/health.bin", round: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Proxy-Authentication-Info": {"Nexus2"}}, Body: io.NopCloser(bytes.NewReader(make([]byte, tc.size)))}, nil
			}}
			if elapsed := s.capacity(lane); (elapsed > 0) != tc.ok {
				t.Fatalf("status=%d bytes=%d elapsed=%v", tc.status, tc.size, elapsed)
			}
		}
	})
}

func TestSelection(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var closed atomic.Bool
	l := &transportLane{kind: "h3", stats: func() uint64 { return 1 }}
	alt := &transportLane{kind: "h2", close: func() { closed.Store(true) }}
	s := selector{ctx: ctx, lane: l, events: make(chan string, 1), dial: func(context.Context, string) (*transportLane, error) { return alt, nil }}
	if silentForTwoWindows(l, 0, time.Now().Add(-time.Minute)) || silentForTwoWindows(l, 1, time.Now()) || !silentForTwoWindows(l, 1, time.Now().Add(-time.Minute)) {
		t.Fatal("slow versus silent")
	}
	ready, discard := s.prebuildH2()
	defer discard()
	if next := <-ready; !s.set(l, next) || s.current() != alt || <-s.events != "h2" {
		t.Fatal("handover")
	}
	if s.set(l, alt) || !closed.Load() {
		t.Fatal("late handshake")
	}
	for status, code := range map[int]Code{200: Authentication, 403: Destination, 429: Limit, 502: Destination} {
		var e *Error
		if !errors.As(l.accepted(&http.Response{StatusCode: status}), &e) || e.Code != code {
			t.Fatal(status)
		}
	}
}

func TestAndroidRecoveryActivityPowerAndBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		s := &selector{ctx: ctx, androidVPN: true, wake: make(chan struct{}, 1), events: make(chan string, 1)}
		l := &transportLane{kind: "h2", wake: s.wake}
		s.lane = l
		c := &Client{s: s}
		tries := make(chan time.Time, 32)
		var hold atomic.Bool
		canceled := make(chan struct{})
		s.dial = func(ctx context.Context, kind string) (*transportLane, error) {
			tries <- time.Now()
			if hold.Load() {
				<-ctx.Done()
				close(canceled)
			}
			return nil, errors.New("blocked")
		}
		go s.watch()
		time.Sleep(10 * time.Minute)
		if len(tries) != 0 {
			t.Fatal("idle connection dialed")
		}
		c.SetRecoveryAllowed(false)
		for range 8 {
			l.business()
			time.Sleep(5 * time.Second)
		}
		if len(tries) != 0 {
			t.Fatal("screen-off connection dialed")
		}
		c.SetRecoveryAllowed(true)
		// Continuous demand makes each pending retry eligible.
		for range 206 {
			l.business()
			time.Sleep(5 * time.Second)
		}
		if len(tries) != 6 {
			t.Fatalf("dial count %d", len(tries))
		}
		previous := <-tries
		for i, gap := range []time.Duration{60, 120, 240, 300, 300} {
			current := <-tries
			delta := current.Sub(previous)
			previous = current
			if delta < gap*time.Second || delta > gap*time.Second+time.Second {
				t.Fatalf("retry %d after %v", i+1, delta)
			}
		}
		time.Sleep(10 * time.Minute)
		if len(tries) != 0 {
			t.Fatal("stale demand kept probing")
		}
		hold.Store(true)
		l.business()
		time.Sleep(600 * time.Millisecond)
		if len(tries) != 1 {
			t.Fatal("new activity did not wake the selector")
		}
		c.SetRecoveryAllowed(false)
		synctest.Wait()
		select {
		case <-canceled:
		default:
			t.Fatal("in-flight background handshake not canceled")
		}
	})
}

func TestRecoveryAllowsQUICRetransmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := &transportLane{health: "https://node.test/health.bin", kind: "h3"}
		var reads int
		l.round = func(r *http.Request) (*http.Response, error) {
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-time.After(time.Second):
			}
			reads++
			return &http.Response{StatusCode: 200, Header: http.Header{"Proxy-Authentication-Info": {"Nexus2"}}, Body: io.NopCloser(bytes.NewReader(make([]byte, 32768)))}, nil
		}
		s := selector{ctx: t.Context(), androidVPN: true, dial: func(ctx context.Context, _ string) (*transportLane, error) {
			select {
			case <-time.After(1200 * time.Millisecond):
				return l, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}
		if lane, ok := s.recoverH3(); lane != l || !ok || reads != 2 {
			t.Fatal("recovery abandoned a healthy H3 handshake")
		}
	})
}

func TestAndroidFinalH3CheckAfterBusinessWentQuiet(t *testing.T) {
	for _, healthy := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s := selector{ctx: ctx, androidVPN: true}
			old := &transportLane{health: "https://node.test/health.bin", kind: "h3", stats: func() uint64 { return 1024 }}
			old.active.Store(1)
			old.last.Store(time.Now().Add(-2500 * time.Millisecond).UnixNano())
			var checks atomic.Int32
			var closed atomic.Bool
			old.close = func() { closed.Store(true) }
			old.round = func(req *http.Request) (*http.Response, error) {
				checks.Add(1)
				if !healthy {
					<-req.Context().Done()
					return nil, req.Context().Err()
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Proxy-Authentication-Info": {"Nexus2"}}, Body: io.NopCloser(bytes.NewReader(make([]byte, 32768)))}, nil
			}
			done := make(chan struct{})
			go func() {
				s.drainH3(old, &transportLane{usable: func() bool { return true }})
				close(done)
			}()
			time.Sleep(5 * time.Second)
			if closed.Load() == healthy || checks.Load() != 1 {
				t.Fatalf("healthy=%t, closed=%t, final checks=%d", healthy, closed.Load(), checks.Load())
			}
			old.active.Store(0)
			<-done
		})
	}
}

func TestLossWindowIgnoresUnverifiedCounters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, tc := range []struct{ proof, sent, lost string }{
			{"", "200", "100"}, {"Nexus2", "", ""}, {"Nexus2", "200", "invalid"}, {"Nexus2", "200", "300"},
		} {
			l := &transportLane{health: "https://node.test/health.bin", round: func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodHead {
					t.Fatalf("fast path requested %s", r.Method)
				}
				h := make(http.Header)
				h.Set("Proxy-Authentication-Info", tc.proof)
				h.Set("Nexus-QUIC-Sent", tc.sent)
				h.Set("Nexus-QUIC-Lost", tc.lost)
				return &http.Response{StatusCode: 200, Header: h, Body: http.NoBody}, nil
			}}
			window := lossWindow{remote: packetCounters{100, 1}, measured: time.Now()}
			if window.impaired(t.Context(), l) {
				t.Fatal("accepted unverified counters", tc)
			}
		}
	})
}

func TestLossEvidenceIsBoundToTheSamplingWindow(t *testing.T) {
	for _, tc := range []struct {
		first, last packetCounters
		want        bool
	}{
		{packetCounters{}, packetCounters{1000, 200}, false},
		{packetCounters{100, 1}, packetCounters{}, false},
		{packetCounters{100, 9}, packetCounters{120, 8}, false},
		{packetCounters{100, 1}, packetCounters{101, 2}, false},
		{packetCounters{100, 1}, packetCounters{1100, 50}, false},
		{packetCounters{100, 1}, packetCounters{1100, 51}, true},
	} {
		if got := tc.last.impairedSince(tc.first); got != tc.want {
			t.Errorf("%v to %v: impaired %t, want %t", tc.first, tc.last, got, tc.want)
		}
	}
}

func TestFastDownloadComparesUnloadedCarriers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var closed atomic.Bool
		var calls atomic.Int32
		round := func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			time.Sleep(time.Millisecond)
			return &http.Response{StatusCode: 200, Header: http.Header{"Proxy-Authentication-Info": {"Nexus2"}}, Body: io.NopCloser(bytes.NewReader(make([]byte, 32768)))}, nil
		}
		h2 := &transportLane{kind: "h2", health: "https://node.test/health.bin", round: round}
		fresh := &transportLane{kind: "h3", health: h2.health, close: func() { closed.Store(true) }, round: func(r *http.Request) (*http.Response, error) {
			time.Sleep(5 * time.Millisecond)
			resp, err := round(r)
			resp.Header.Set("Nexus-QUIC-Sent", strconv.Itoa(int(calls.Load())*100))
			resp.Header.Set("Nexus-QUIC-Lost", "1")
			return resp, err
		}}
		s := selector{ctx: t.Context(), lane: &transportLane{kind: "h3", round: func(*http.Request) (*http.Response, error) {
			t.Error("sampled saturated lane")
			return nil, errors.New("busy")
		}},
			dial: func(context.Context, string) (*transportLane, error) { return fresh, nil }}
		if slow, lossy := s.slowerOnFreshH3(h2); slow || lossy || calls.Load() != 2*(1+8) || !closed.Load() {
			t.Fatal("healthy path downgraded or temporary carrier was left open")
		}
	})
}

func TestAndroidSlowRequiresTwoComparisons(t *testing.T) {
	t.Run("throttled", func(t *testing.T) { testAndroidSlow(t, 25000, false, false) })
	t.Run("bursty-download", func(t *testing.T) { testAndroidSlow(t, 1<<20, true, true) })
	t.Run("busy-lossless-download", func(t *testing.T) { testAndroidSlow(t, 1<<20, true, false) })
}

func testAndroidSlow(t *testing.T, bytesPerTick uint64, bursty, loss bool) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var received atomic.Uint64
		var checks, h2Checks atomic.Int32
		answer := func() *http.Response {
			return &http.Response{StatusCode: 200, Header: http.Header{"Proxy-Authentication-Info": {"Nexus2"}}, Body: io.NopCloser(bytes.NewReader(make([]byte, 32768)))}
		}
		h3 := &transportLane{kind: "h3", health: "https://node.test/health.bin", stats: received.Load, close: func() {}}
		objects := int32(1)
		if bursty {
			objects = 8
		}
		var heads atomic.Int32
		h3.round = func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodHead {
				n := int64(heads.Add(1))
				res := answer()
				res.Header.Set("Nexus-QUIC-Sent", strconv.FormatInt(n*1024, 10))
				if loss {
					n *= 128
				}
				res.Header.Set("Nexus-QUIC-Lost", strconv.FormatInt(n, 10))
				return res, nil
			}
			if (checks.Add(1)-1)/objects == 1 {
				time.Sleep(time.Millisecond)
			} else {
				time.Sleep(5 * time.Millisecond)
			}
			return answer(), nil
		}
		h2 := &transportLane{kind: "h2", health: h3.health, close: func() {}, usable: func() bool { return true }, round: func(*http.Request) (*http.Response, error) {
			h2Checks.Add(1)
			time.Sleep(500 * time.Microsecond)
			return answer(), nil
		}}
		var fresh atomic.Int32
		s := &selector{ctx: ctx, androidVPN: true, lane: h3, events: make(chan string, 1), dial: func(_ context.Context, kind string) (*transportLane, error) {
			if kind == "h2" {
				return h2, nil
			}
			epoch := fresh.Add(1)
			seen := 0
			return &transportLane{kind: "h3", health: h3.health, close: func() {}, round: func(*http.Request) (*http.Response, error) {
				checks.Add(1)
				seen++
				if epoch == 1 && seen > 1+int(objects) {
					time.Sleep(time.Millisecond)
				} else {
					time.Sleep(5 * time.Millisecond)
				}
				res := answer()
				res.Header.Set("Nexus-QUIC-Sent", strconv.Itoa(100*seen))
				if loss {
					res.Header.Set("Nexus-QUIC-Lost", strconv.Itoa(12*seen))
				}
				return res, nil
			}}, nil
		}}
		h3.active.Store(1)
		r, w := io.Pipe()
		defer r.Close()
		defer w.Close()
		if bursty {
			h3.wake = make(chan struct{}, 1)
			_, out := io.Pipe()
			defer out.Close()
			go io.Copy(io.Discard, &stream{in: r, out: out, l: h3})
		}
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
					received.Add(bytesPerTick)
					if bursty {
						if _, err := w.Write([]byte{1}); err != nil {
							return
						}
					} else {
						h3.business()
					}
				}
			}
		}()
		go s.watch()
		select {
		case kind := <-s.events:
			if bursty && !loss || kind != "h2" || checks.Load() != 4*objects+fresh.Load() || checks.Load() != h2Checks.Load() {
				t.Fatalf("switched to %s after %d H3 and %d H2 objects", kind, checks.Load(), h2Checks.Load())
			}
		case <-time.After(10 * time.Second):
			if !bursty || loss || checks.Load() != 0 || h2Checks.Load() != 0 || heads.Load() < 2 || heads.Load() > 5 {
				t.Fatalf("H3 retained after %d/%d objects, bursty=%t loss=%t", checks.Load(), h2Checks.Load(), bursty, loss)
			}
		}
		h3.active.Store(0)
		time.Sleep(time.Second)
	})
}
