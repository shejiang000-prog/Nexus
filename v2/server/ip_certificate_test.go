package server

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"github.com/caddyserver/caddy/v2"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIPCertificateRenewal(t *testing.T) {
	for _, ip := range []string{"192.0.2.4", "2001:db8::4"} {
		t.Run(ip, func(t *testing.T) {
			m := ipCertificate{IP: ip, Directory: t.TempDir()}
			if err := m.Provision(caddy.Context{}); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			a, err := m.certificate(now)
			if err != nil {
				t.Fatal(err)
			}
			if a.Leaf.VerifyHostname(ip) != nil || a.Leaf.CheckSignature(a.Leaf.SignatureAlgorithm, a.Leaf.RawTBSCertificate, a.Leaf.Signature) != nil || len(a.Leaf.DNSNames) != 0 || a.Leaf.PublicKey.(*ecdsa.PublicKey).Curve != elliptic.P256() {
				t.Fatal("invalid IP leaf")
			}
			b, err := m.certificate(a.Leaf.NotAfter.Add(-20 * 24 * time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(a.Leaf.Raw, b.Leaf.Raw) || !bytes.Equal(a.Leaf.RawSubjectPublicKeyInfo, b.Leaf.RawSubjectPublicKeyInfo) {
				t.Fatal("renewal changed public key or retained old certificate")
			}
			reloaded := ipCertificate{IP: ip, Directory: m.Directory}
			if err := reloaded.Provision(caddy.Context{}); err != nil {
				t.Fatal(err)
			}
			got, err := reloaded.GetCertificate(t.Context(), nil)
			if err != nil || !bytes.Equal(got.Leaf.RawSubjectPublicKeyInfo, a.Leaf.RawSubjectPublicKeyInfo) {
				t.Fatal("reload lost node identity")
			}
			for _, f := range []string{"ip-key.pem", "ip-cert.pem"} {
				s, err := os.Stat(filepath.Join(m.Directory, f))
				if err != nil || s.Mode().Perm() != 0600 {
					t.Fatal("identity permissions", f)
				}
			}
			if err := os.Remove(filepath.Join(m.Directory, "ip-key.pem")); err != nil {
				t.Fatal(err)
			}
			if err := reloaded.Provision(caddy.Context{}); err == nil {
				t.Fatal("missing private key silently replaced")
			}
		})
	}
}
