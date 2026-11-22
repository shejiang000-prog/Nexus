package transport

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

// Chrome 157 loopback reference. JA4 excludes GREASE and sorts cipher/extension IDs.
func helloCodes[T ~uint16](ids []T) []string {
	var values []string
	for _, id := range ids {
		n := uint16(id)
		if n>>8 == n&255 && n&15 == 10 {
			continue
		}
		values = append(values, fmt.Sprintf("%04x", n))
	}
	return values
}

func TestDefaultH2ClientHello(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := dialTCP(ctx, ln.Addr().String(), "node.test", nil, nil, nil); done <- err }()
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var seen bool
	end := errors.New("reference recorded")
	server := tls.Server(conn, &tls.Config{GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
		seen = true
		ciphers, exts := helloCodes(h.CipherSuites), helloCodes(h.Extensions)
		sigs, groups := strings.Join(helloCodes(h.SignatureSchemes), ","), strings.Join(helloCodes(h.SupportedCurves), ",")
		slices.Sort(ciphers)
		slices.Sort(exts)
		if groups != "11ec,001d,0017,0018" || sigs != "0904,0905,0906,0403,0804,0401,0503,0805,0501,0806,0601" {
			t.Errorf("groups %s signatures %s", groups, sigs)
		}
		if expected := "0000,0005,000a,000b,000d,0010,0012,0017,001b,0023,002b,002d,0033,44cd,ca34,fe0d,ff01"; strings.Join(exts, ",") != expected {
			t.Errorf("extensions %v", exts)
		}
		if !slices.Contains(h.SupportedVersions, uint16(tls.VersionTLS13)) || h.ServerName != "node.test" || len(h.SupportedProtos) == 0 || h.SupportedProtos[0] != "h2" {
			t.Error("TLS version, SNI or ALPN mismatch")
			return nil, end
		}
		prefix := fmt.Sprintf("t13d%02d%02d%s", len(ciphers), len(exts), h.SupportedProtos[0])
		exts = slices.DeleteFunc(exts, func(id string) bool { return id == "0000" || id == "0010" })
		a := sha256.Sum256([]byte(strings.Join(ciphers, ",")))
		b := sha256.Sum256([]byte(strings.Join(exts, ",") + "_" + sigs))
		got := fmt.Sprintf("%s_%x_%x", prefix, a[:6], b[:6])
		if got != "t13d1517h2_8daaf6152771_cb7bf5808d99" {
			t.Error(got)
		}
		return nil, end
	}})
	if err := server.HandshakeContext(ctx); !errors.Is(err, end) || !seen {
		t.Fatalf("capture: %v", err)
	}
	<-done
}
