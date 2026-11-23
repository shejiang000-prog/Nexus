package transport

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
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
