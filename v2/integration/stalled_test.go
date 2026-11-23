package integration

import (
	"bytes"
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoppedReaderDoesNotBlockOtherDevice(t *testing.T) {
	for _, mode := range []string{"h2", "h3"} {
		t.Run(mode, func(t *testing.T) {
			testNode(t, true, false, func(t *testing.T, l *nodeLab) {
				_, slow := l.client(t, mode)
				_, fast := l.client(t, mode)
				// The echo origin turns uploads into downloads. Never read the first download.
				stalled, err := slow.DialTCP(t.Context(), l.tcp, nil)
				if err != nil {
					t.Fatal(err)
				}
				var sent atomic.Int64
				done := make(chan struct{})
				go func() {
					defer close(done)
					block := make([]byte, 32<<10)
					for {
						n, err := stalled.Write(block)
						sent.Add(int64(n))
						if err != nil {
							return
						}
					}
				}()
				defer func() { stalled.Close(); <-done }()
				// Wait for observed backpressure, rather than assuming a carrier window size.
				deadline := time.Now().Add(8 * time.Second)
				for {
					before := sent.Load()
					time.Sleep(200 * time.Millisecond)
					select {
					case <-done:
						t.Fatal("stalled stream ended early")
					default:
					}
					if before > 0 && sent.Load() == before {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("first stream never backpressured")
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				const maxGap = 2 * time.Second
				timer := time.AfterFunc(maxGap, cancel)
				defer timer.Stop()
				stream, err := fast.DialTCP(ctx, l.tcp, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				payload := bytes.Repeat([]byte{0x5a}, 128<<10)
				echo := make([]byte, len(payload))
				var longest time.Duration
				for total := 0; total < 20<<20; total += len(payload) {
					began := time.Now()
					if n, err := stream.Write(payload); err != nil || n != len(payload) {
						t.Fatalf("write after %d bytes: %d %v", total, n, err)
					}
					if _, err := io.ReadFull(stream, echo); err != nil || !bytes.Equal(echo, payload) {
						t.Fatalf("echo after %d bytes: %v", total, err)
					}
					longest = max(longest, time.Since(began))
					if !timer.Reset(maxGap) {
						t.Fatal("second device stalled")
					}
				}
				select {
				case <-done:
					t.Fatal("first device disconnected during transfer")
				default:
				}
				t.Logf("second device echoed 20 MiB while first download unread; longest 128 KiB round trip %s", longest)
			})
		})
	}
}
