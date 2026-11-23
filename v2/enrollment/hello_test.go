package enrollment

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"slices"
	"testing"
	"time"

	quic "github.com/apernet/quic-go"
	"github.com/shejiang000-prog/Nexus/v2/transport"
)

func helloValues[T ~uint16](fields []T) []uint16 {
	var values []uint16
	for _, f := range fields {
		n := uint16(f)
		// GREASE values are randomized per handshake (RFC 8701).
		if n>>8 != n&255 || n&15 != 10 {
			values = append(values, n)
		}
	}
	return values
}

func TestRedemptionMatchesH2ClientHello(t *testing.T) {
	for _, host := range []string{"node.test", "127.0.0.1"} {
		t.Run(host, func(t *testing.T) {
			wantSNI := host
			if net.ParseIP(host) != nil {
				wantSNI = ""
			}
			stop := errors.New("sample recorded")
			fps := make(chan string, 2)
			cfg := &tls.Config{GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
				exts := helloValues(h.Extensions)
				slices.Sort(exts) // Chrome shuffles extension order.
				if h.ServerName != wantSNI || slices.Contains(exts, uint16(0)) != (wantSNI != "") || !slices.Equal(h.SupportedProtos, []string{"h2", "http/1.1"}) {
					t.Errorf("client hello: SNI %q ALPN %v", h.ServerName, h.SupportedProtos)
				}
				fps <- fmt.Sprint(helloValues(h.CipherSuites), exts, helloValues(h.SupportedCurves), helloValues(h.SignatureSchemes), h.SupportedProtos, helloValues(h.SupportedVersions))
				return nil, stop
			}}
			ln, err := tls.Listen("tcp4", "127.0.0.1:0", cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			go func() {
				for range 2 {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					conn.SetDeadline(time.Now().Add(time.Second))
					conn.(*tls.Conn).Handshake()
					conn.Close()
				}
			}()
			i := &Invitation{host: host, authority: net.JoinHostPort(host, "443"), token: make([]byte, 32), pin: make([]byte, 32)}
			Redeem(i, ln.Addr().String(), nil, make([]byte, 16), make([]byte, 33), testKey{})
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			transport.StartPinned(ctx, transport.Config{Authority: i.authority, Endpoint: ln.Addr().String(), Mode: "h2", Sign: func([]byte) ([]byte, error) { return nil, stop }}, i.pin)
			var samples []string
			for range 2 {
				select {
				case s := <-fps:
					samples = append(samples, s)
				case <-ctx.Done():
					t.Fatal("missing ClientHello")
				}
			}
			if samples[0] != samples[1] {
				t.Fatal("redemption and business H2 ClientHello differ")
			}
		})
	}
}

func TestIPHTTP3OmitsSNI(t *testing.T) {
	result := make(chan bool, 1)
	ln, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{NextProtos: []string{"h3"}, GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
		select {
		case result <- (h.ServerName == "" && !slices.Contains(h.Extensions, uint16(0))):
		default:
		}
		return nil, errors.New("SNI recorded")
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	transport.StartPinned(ctx, transport.Config{Authority: ln.Addr().String(), Mode: "h3", Sign: func([]byte) ([]byte, error) { return nil, nil }}, make([]byte, 32))
	select {
	case omitted := <-result:
		if !omitted {
			t.Fatal("IP H3 sent SNI")
		}
	case <-ctx.Done():
		t.Fatal("H3 ClientHello missing")
	}
}
