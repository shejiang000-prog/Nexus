package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apernet/quic-go/http3"
	"github.com/shejiang000-prog/Nexus/v2/udp"
)

func TestFirstDNSCapsule(t *testing.T) {
	query := []byte("synthetic DNS query")
	frame, err := udp.DatagramCapsule(query)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"h2", "h3"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				got := make([]byte, len(frame))
				if _, err := io.ReadFull(r.Body, got); err != nil || !bytes.Equal(got, frame) {
					t.Errorf("first frame before response = %x, %v", got, err)
					return
				}
				if n == 2 {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if n == 1 {
					w.Header().Set("Proxy-Authentication-Info", "Nexus2")
				}
				w.Header().Set("Capsule-Protocol", "?1")
				w.WriteHeader(http.StatusOK)
				w.Write(frame)
			})
			srv := httptest.NewUnstartedServer(handler)
			srv.EnableHTTP2 = true
			srv.StartTLS()
			defer srv.Close()
			addr := srv.Listener.Addr().String()
			if mode == "h3" {
				sock, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer sock.Close()
				h3 := &http3.Server{TLSConfig: srv.TLS.Clone(), Handler: handler}
				go h3.Serve(sock)
				defer h3.Close()
				addr = sock.LocalAddr().String()
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			pin := sha256.Sum256(srv.Certificate().RawSubjectPublicKeyInfo)
			c, err := StartPinned(ctx, Config{Mode: mode, Authority: addr, Sign: func([]byte) ([]byte, error) { return make([]byte, 64), nil }}, pin[:])
			if err != nil {
				t.Fatal(err)
			}
			defer c.Stop()
			for _, want := range []Code{0, Destination, Authentication} {
				a, err := c.openUDP(ctx, "dns.test:53", frame)
				if want != 0 {
					var typed *Error
					if a != nil || !errors.As(err, &typed) || typed.Code != want {
						t.Fatalf("want rejection %d, got %v", want, err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				got, err := a.ReadPacket()
				a.Close()
				if err != nil || !bytes.Equal(got, query) {
					t.Fatalf("reply = %x, %v", got, err)
				}
			}
			if calls.Load() != 3 {
				t.Fatal("unexpected CONNECT replay")
			}
		})
	}
}
