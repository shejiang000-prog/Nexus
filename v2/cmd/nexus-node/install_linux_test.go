package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeploymentInputAndPEM(t *testing.T) {
	base := []string{"--non-interactive", "--domain", "nexus.test", "--cert", "/input/lab.pem", "--key", "/input/lab.key"}
	defaults, err := deploymentOptions("install", base, strings.NewReader(""), io.Discard)
	if err != nil || len(defaults.Ports) != 1 {
		t.Fatalf("default V2: %v", err)
	}
	if _, ok := defaults.Ports["v2"]; !ok {
		t.Fatal("V2 missing")
	}
	for _, args := range [][]string{{"--domain", "127.0.0.1"}, {"--protocols", "v2,v2"}, {"--protocols", "reality"}, {"--reality-port", "443"}, {"--v2-port", "65536"}, {"--interactive"}} {
		if _, e := deploymentOptions("install", append(append([]string{}, base...), args...), strings.NewReader(""), io.Discard); e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	o, err := deploymentOptions("install", []string{"--interactive"}, strings.NewReader("Nexus.Test\nv2,hysteria2\n24443\n24444\n/input/lab.pem\n/input/lab.key\n"), io.Discard)
	if err != nil || o.Domain != "nexus.test" || o.Ports["v2"] != 24443 || o.Ports["hysteria2"] != 24444 {
		t.Fatalf("interactive options: %v", err)
	}
	input := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := x509.Certificate{SerialNumber: big.NewInt(7), DNSNames: []string{"nexus.test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, &cert, &cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	o.CertFile = filepath.Join(input, "c")
	o.KeyFile = filepath.Join(input, "k")
	if err = os.WriteFile(o.CertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(o.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: secret}), 0600); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	if err = renderDeployment(t.Context(), o, stage); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"deployment.json", "v2.json", "cert.pem", "key.pem"} {
		info, err := os.Stat(filepath.Join(stage, f))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("staged permissions for %s: %v", f, err)
		}
	}
	conf, err := os.ReadFile(filepath.Join(stage, "v2.json"))
	if err != nil || !json.Valid(conf) || !strings.Contains(string(conf), "nexus.test:24443") {
		t.Fatal("V2 authority missing")
	}
	if strings.Contains(string(conf), `"/health.bin"`) || strings.Contains(string(conf), strings.Repeat("0", 32768)) {
		t.Fatal("public site exposes the authenticated health object")
	}
	o.Domain = "other.test"
	if err = renderDeployment(t.Context(), o, t.TempDir()); err == nil {
		t.Fatal("accepted wrong certificate SAN")
	}
	o.Domain = "nexus.test"
	o.KeyFile = o.CertFile
	if err = renderDeployment(t.Context(), o, t.TempDir()); err == nil {
		t.Fatal("accepted invalid private key")
	}
}

func TestACMECallbackConfirmation(t *testing.T) {
	for _, tc := range []struct {
		answer       string
		want, failed bool
	}{{"yes\n", true, false}, {"\n", false, false}, {"no\n", false, false}, {"", false, true}} {
		var output strings.Builder
		o, err := deploymentOptions("install", []string{"--interactive"}, strings.NewReader("nexus.test\nv2\n0\n\n"+tc.answer), &output)
		if err != nil {
			t.Fatal(err)
		}
		if o.ConfirmDNSFailure == nil {
			t.Fatal("missing confirmation")
		}
		got, err := o.ConfirmDNSFailure()
		if tc.failed != (err != nil) || got != tc.want || !strings.Contains(output.String(), "Continue with ACME") {
			t.Fatal(got, err)
		}
	}
	o, err := deploymentOptions("install", []string{"--non-interactive", "--domain", "nexus.test", "--skip-dns-check"}, strings.NewReader(""), io.Discard)
	if err != nil || !o.SkipDNSCheck || o.ConfirmDNSFailure != nil {
		t.Fatal(err)
	}
}

func TestRealityTargetHandshake(t *testing.T) {
	for _, tc := range []struct {
		name        string
		version     uint16
		curve       tls.CurveID
		sni         string
		trust, pass bool
	}{
		{"compatible", tls.VersionTLS13, tls.X25519, "example.com", true, true},
		{"tls12", tls.VersionTLS12, tls.X25519, "example.com", true, false},
		{"different group", tls.VersionTLS13, tls.CurveP256, "example.com", true, false},
		{"untrusted", tls.VersionTLS13, tls.X25519, "example.com", false, false},
		{"second name mismatch", tls.VersionTLS13, tls.X25519, "example.com,invalid.test", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewUnstartedServer(http.NotFoundHandler())
			s.TLS = &tls.Config{MaxVersion: tc.version, CurvePreferences: []tls.CurveID{tc.curve}}
			s.StartTLS()
			defer s.Close()
			roots := x509.NewCertPool()
			if tc.trust {
				roots.AddCert(s.Certificate())
			}
			_, err := checkReality(s.Listener.Addr().String(), tc.sni, roots)
			if (err == nil) != tc.pass {
				t.Fatalf("unexpected TLS preflight result: %v", err)
			}
		})
	}
}
