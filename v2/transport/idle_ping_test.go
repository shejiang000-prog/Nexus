package transport

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	quic "github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/apernet/quic-go/qlog"
	"github.com/apernet/quic-go/qlogwriter"
)

// Count only decoded frames received by the server. Never persist a trace.
type incomingFrames struct {
	pings, streamFIN atomic.Int64
}

func (f *incomingFrames) AddProducer() qlogwriter.Recorder { return f }
func (*incomingFrames) Close() error                       { return nil }
func (f *incomingFrames) RecordEvent(ev qlogwriter.Event) {
	p, ok := ev.(qlog.PacketReceived)
	if !ok {
		return
	}
	for _, frame := range p.Frames {
		switch v := frame.Frame.(type) {
		case *qlog.PingFrame:
			f.pings.Add(1)
		case *qlog.StreamFrame:
			if v.Fin && v.StreamID == 0 { // first client bidirectional stream
				f.streamFIN.Add(1)
			}
		}
	}
}

func TestH3NoPingAfterLastTunnel(t *testing.T) {
	certs := httptest.NewTLSServer(nil)
	certs.Close()
	sock, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sock.Close()
	var seen incomingFrames
	server := &http3.Server{
		TLSConfig: &tls.Config{Certificates: certs.TLS.Certificates},
		QUICConfig: &quic.Config{Tracer: func(context.Context, bool, quic.ConnectionID) qlogwriter.Trace {
			return &seen
		}},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Proxy-Authentication-Info", "Nexus2")
			w.WriteHeader(http.StatusOK)
			flush := http.NewResponseController(w).Flush
			if flush() != nil {
				return
			}
			var b [1]byte
			for {
				if _, err := io.ReadFull(r.Body, b[:]); err != nil {
					return
				}
				if _, err := w.Write(b[:]); err != nil || flush() != nil {
					return
				}
			}
		}),
	}
	go server.Serve(sock)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	pin := sha256.Sum256(certs.Certificate().RawSubjectPublicKeyInfo)
	client, err := StartPinned(ctx, Config{Authority: sock.LocalAddr().String(), Mode: "h3",
		Sign: func([]byte) ([]byte, error) { return make([]byte, 64), nil },
	}, pin[:])
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	stream, err := client.DialTCP(ctx, "echo.test:443", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	// Exchange data while the handshake and initial path-MTU probes settle.
	for i := byte(0); i < 5; i++ {
		if _, err := stream.Write([]byte{i}); err != nil {
			t.Fatal(err)
		}
		var got [1]byte
		if _, err := io.ReadFull(stream, got[:]); err != nil || got[0] != i {
			t.Fatalf("echo %d: %v, %v", i, got, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := stream.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if n, err := io.Copy(io.Discard, stream); err != nil || n != 0 {
		t.Fatalf("response FIN: read %d trailing bytes, %v", n, err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if seen.streamFIN.Load() == 0 {
		t.Fatal("frame recorder missed the business stream FIN")
	}
	before := seen.pings.Load()
	time.Sleep(30 * time.Second)
	if n := seen.pings.Load() - before; n != 0 {
		t.Fatalf("received %d client PING frames in 30s with no business streams", n)
	}
	t.Logf("client PING frames before closing the last stream: %d; in the following 30s: 0", before)
}
