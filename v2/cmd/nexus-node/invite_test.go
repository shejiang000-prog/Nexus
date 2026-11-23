package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstalledInviteLinks(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"node.example", "192.0.2.42", "2001:db8::42"} {
		t.Run(host, func(t *testing.T) {
			root := t.TempDir()
			cfg := filepath.Join(root, "etc/nexus-v2")
			state := filepath.Join(root, "var/lib/nexus-v2")
			for _, dir := range []string{cfg, state} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			db := filepath.Join(state, "ledger.db")
			var out bytes.Buffer
			if err := admin([]string{db, "create-user"}, &out); err != nil {
				t.Fatal(err)
			}
			user := strings.TrimSpace(out.String())
			opts := compatDeployment{Domain: host, Ports: map[string]int{"v2": 8443}}
			wantPin := ""
			if ip := net.ParseIP(host); ip != nil {
				opts.Domain, opts.PublicHost = "", host
				cert := &x509.Certificate{SerialNumber: big.NewInt(5), IPAddresses: []net.IP{ip}}
				der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(state, "ip-cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
					t.Fatal(err)
				}
				leaf, err := x509.ParseCertificate(der)
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
				wantPin = base64.RawURLEncoding.EncodeToString(sum[:])
			}
			data, err := json.Marshal(opts)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(cfg, "deployment.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", root) // Optional QR renderer absent.
			out.Reset()
			until := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
			if err = admin([]string{db, "invite", "--qr", user, until, "--root", root}, &out); err != nil {
				t.Fatal(err)
			}
			link := strings.TrimSpace(out.String())
			raw, ok := strings.CutPrefix(link, "nexus://invite/v2#")
			if !ok {
				t.Fatal("missing share link")
			}
			blob, err := base64.RawURLEncoding.DecodeString(raw)
			if err != nil {
				t.Fatal("invalid share encoding")
			}
			var got shareInvite
			if err = json.Unmarshal(blob, &got); err != nil || got.Version != 2 || got.Host != host || got.Port != 8443 || got.Pin != wantPin {
				t.Fatal("incorrect invite metadata")
			}
			token, err := base64.RawURLEncoding.DecodeString(got.Invite)
			if err != nil || len(token) != 32 {
				t.Fatal("incorrect invitation size")
			}
			if err = admin([]string{filepath.Join(root, "other.db"), "invite", user, until, "--link", "--root", root}, &out); err == nil {
				t.Fatal("accepted different ledger")
			}
		})
	}
}

func TestInviteQRPassesBearerOnlyOnStdin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	script := []byte("#!/bin/sh\n[ \"$#\" = 2 ] && [ \"$1\" = -t ] && [ \"$2\" = UTF8 ] || exit 1\nIFS= read -r link || :\ncase \"$link\" in nexus://invite/v2#*) echo qr-from-stdin ;; *) exit 1 ;; esac\n")
	if err := os.WriteFile(filepath.Join(dir, "qrencode"), script, 0700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := (&shareInvite{Version: 2, Host: "node.example", Port: 443}).write(&out, make([]byte, 32), true); err != nil || !strings.HasSuffix(out.String(), "\nqr-from-stdin\n") {
		t.Fatal("failed optional terminal output")
	}
}
