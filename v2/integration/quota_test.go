package integration

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"testing"
	"time"
)

func TestQuotaAcrossDevices(t *testing.T) {
	for _, mode := range []string{"h2", "h3"} {
		t.Run(mode, func(t *testing.T) {
			testNode(t, true, false, func(t *testing.T, l *nodeLab) {
				ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
				defer cancel()
				const limit = 16001
				user := hex.EncodeToString(l.user[:])
				l.admin(t, "set-quota", user, strconv.Itoa(limit))
				_, id, key := l.enroll(t, mode)
				c := l.connect(t, mode, id, key)
				_, other := l.client(t, mode)
				tcp1, err := c.DialTCP(ctx, l.tcp, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tcp1.Close()
				tcp2, err := other.DialTCP(ctx, l.tcp, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tcp2.Close()
				association, err := c.OpenUDP(ctx, l.udp)
				if err != nil {
					t.Fatal(err)
				}
				defer association.Close()
				probe := []byte("quota datagram")
				if err := association.WritePacket(probe); err != nil {
					t.Fatal(err)
				}
				echo, err := association.ReadPacket()
				if err != nil || !bytes.Equal(echo, probe) {
					t.Fatalf("UDP echo: %v", err)
				}
				ended := make(chan struct{}, 3)
				go func() { association.ReadPacket(); ended <- struct{}{} }()
				for _, stream := range []io.ReadWriteCloser{tcp1, tcp2} {
					go func() { io.Copy(io.Discard, stream); ended <- struct{}{} }()
				}
				payload := bytes.Repeat([]byte{42}, limit*2)
				start := time.Now()
				go tcp1.Write(payload)
				go tcp2.Write(payload)
				timer := time.NewTimer(time.Second)
				defer timer.Stop()
				for range 3 {
					select {
					case <-ended:
					case <-timer.C:
						t.Fatal("TCP/UDP survived quota exhaustion for one second")
					}
				}
				t.Logf("two devices, TCP and UDP closed in %v", time.Since(start))
				// Reauthenticate over a fresh carrier, exercising admission after depletion.
				fresh := l.connect(t, mode, id, key)
				if s, err := fresh.DialTCP(ctx, l.tcp, nil); err == nil {
					s.Close()
					t.Fatal("new stream admitted after depletion")
				}
				var usage struct {
					Used int64 `json:"used_bytes"`
				}
				deadline := time.Now().Add(time.Second)
				for time.Now().Before(deadline) {
					if err := json.Unmarshal(l.admin(t, "users"), &usage); err != nil {
						t.Fatal(err)
					}
					if usage.Used == limit {
						break
					}
					time.Sleep(15 * time.Millisecond)
				}
				if usage.Used != limit {
					t.Fatalf("used=%d want %d", usage.Used, limit)
				}
				l.admin(t, "set-quota", user, "unlimited")
				again := l.connect(t, mode, id, key)
				s, err := again.DialTCP(ctx, l.tcp, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				a, err := again.OpenUDP(ctx, l.udp)
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
				go func() { io.Copy(io.Discard, s); ended <- struct{}{} }()
				go func() { a.ReadPacket(); ended <- struct{}{} }()
				start = time.Now()
				l.admin(t, "set-quota", user, "0")
				timer.Reset(time.Second - time.Since(start))
				for range 2 {
					select {
					case <-ended:
					case <-timer.C:
						t.Fatal("cross-process quota change did not close streams within one second")
					}
				}
				t.Logf("cross-process zero quota closed streams in %v", time.Since(start))
			})
		})
	}
}
