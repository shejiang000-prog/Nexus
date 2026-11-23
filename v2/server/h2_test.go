package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/udp"
)

func TestH2UDPMarkerAfterAuthentication(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	id := proof.ID{}
	devices := MemoryDevices{id: &key.PublicKey}
	header, err := proof.Create(secret, id, func(msg []byte) ([]byte, error) {
		h := sha256.Sum256(msg)
		r, s, e := ecdsa.Sign(rand.Reader, key, h[:])
		if e != nil {
			return nil, e
		}
		return append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{devices: devices}
	// The same rejected marker is opaque to an unauthenticated request.
	for _, authenticated := range []bool{false, true} {
		sessions := new(sync.Map)
		if authenticated {
			b := new(sessionBinding)
			if !b.Accept(header, devices, func() ([]byte, error) { return secret, nil }) {
				t.Fatal("setup")
			}
			sessions.Store(&h, b)
		}
		req := httptest.NewRequest(http.MethodConnect, "example.test:443", nil)
		req.ProtoMajor = 2
		req.TLS = &tls.ConnectionState{HandshakeComplete: true, Version: tls.VersionTLS13}
		req = req.WithContext(context.WithValue(req.Context(), bindingKey{}, sessions))
		req.Header[udp.H2FlowHeader] = []string{"udp", "udp"}
		req.Header.Set("Capsule-Protocol", "?1")
		passed := false
		err := h.ServeHTTP(httptest.NewRecorder(), req, caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error { passed = true; return nil }))
		if authenticated {
			he, ok := err.(caddyhttp.HandlerError)
			if passed || !ok || he.StatusCode != 400 {
				t.Fatalf("malformed authenticated marker: %v", err)
			}
		} else if !passed || err != nil {
			t.Fatal("public handler bypassed")
		}
	}
}
