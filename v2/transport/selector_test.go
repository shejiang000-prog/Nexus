package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
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

func TestAutoBlockedAfterHealthyProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var blocked atomic.Bool
		var held atomic.Pointer[transportLane]
		var h2Closed atomic.Bool
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
				candidate := &transportLane{kind: "h2", health: h3.health, round: health}
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

func TestAndroidSlowRequiresTwoComparisons(t *testing.T) {
	t.Run("throttled", func(t *testing.T) { testAndroidSlow(t, 25000, false) })
	t.Run("bursty-download", func(t *testing.T) { testAndroidSlow(t, 1<<20, true) })
}

func testAndroidSlow(t *testing.T, bytesPerTick uint64, bursty bool) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var received atomic.Uint64
		var checks atomic.Int32
		answer := func() *http.Response {
			return &http.Response{StatusCode: 200, Header: http.Header{"Proxy-Authentication-Info": {"Nexus2"}}, Body: io.NopCloser(bytes.NewReader(make([]byte, 32768)))}
		}
		h3 := &transportLane{kind: "h3", health: "https://node.test/health.bin", stats: received.Load, close: func() {}}
		h3.round = func(*http.Request) (*http.Response, error) {
			if checks.Add(1) == 2 {
				time.Sleep(time.Millisecond)
			} else {
				time.Sleep(5 * time.Millisecond)
			}
			return answer(), nil
		}
		h2 := &transportLane{kind: "h2", health: h3.health, close: func() {}, usable: func() bool { return true }, round: func(*http.Request) (*http.Response, error) {
			time.Sleep(500 * time.Microsecond)
			return answer(), nil
		}}
		s := &selector{ctx: ctx, androidVPN: true, lane: h3, events: make(chan string, 1), dial: func(context.Context, string) (*transportLane, error) { return h2, nil }}
		h3.active.Store(1)
		r, w := io.Pipe()
		defer r.Close()
		defer w.Close()
		if bursty {
			h3.wake = make(chan struct{}, 1)
			go io.Copy(io.Discard, &stream{in: r, l: h3})
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
			if kind != "h2" || checks.Load() != 4 {
				t.Fatalf("switched to %s after %d measurements", kind, checks.Load())
			}
		case <-time.After(10 * time.Second):
			t.Fatal("weak business progress did not switch with two slow health samples")
		}
		h3.active.Store(0)
		time.Sleep(time.Second)
	})
}
