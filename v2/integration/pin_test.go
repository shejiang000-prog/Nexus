package integration

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/enrollment"
	"github.com/shejiang000-prog/Nexus/v2/mobile"
	"github.com/shejiang000-prog/Nexus/v2/transport"
)

func TestIPInvitationAndCarrierPins(t *testing.T) {
	testNode(t, true, false, func(t *testing.T, l *nodeLab) {
		ca, err := os.ReadFile(filepath.Join(l.dir, "server.pem"))
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(ca)
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
		wrong := bytes.Clone(digest[:])
		wrong[0] ^= 1
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		id := make([]byte, 16)
		start := func(host, mode string, roots, pin []byte) (*mobile.Client, error) {
			return mobile.StartPinned(host, l.endpoint, mode, roots, id, nil, deviceSigner{key}, nil, nil, nil, pin)
		}
		refused := func(t *testing.T, authority, mode string, roots, pin []byte) {
			t.Helper()
			c, e := start(authority, mode, roots, pin)
			if c != nil {
				c.Stop()
			}
			if mobile.ErrorCode(e) != int32(transport.Certificate) {
				t.Fatalf("certificate rejection code %d, error %v", mobile.ErrorCode(e), e)
			}
		}
		// A DNS pin is an additional restriction on the normal certificate chain.
		for _, mode := range []string{"h2", "h3"} {
			c, e := start(l.authority, mode, ca, digest[:])
			if e != nil {
				t.Fatal(e)
			}
			c.Stop()
			refused(t, l.authority, mode, nil, digest[:])
			refused(t, l.authority, mode, ca, wrong)
		}
		l.stop(t, os.Kill)
		cfg, err := os.ReadFile(l.config)
		if err != nil {
			t.Fatal(err)
		}
		cfg = bytes.ReplaceAll(cfg, []byte(l.authority), []byte(l.endpoint))
		cfg = bytes.Replace(cfg, []byte(`"http":{`), []byte(`"http":{"grace_period":"100ms",`), 1)
		if err = os.WriteFile(l.config, cfg, 0600); err != nil {
			t.Fatal(err)
		}
		l.authority = l.endpoint
		l.start(t)
		token, err := l.ledger.Invite(t.Context(), l.user, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		host, port, _ := net.SplitHostPort(l.authority)
		n, _ := strconv.Atoi(port)
		invite := func(pin []byte) *enrollment.Invitation {
			t.Helper()
			enc := base64.RawURLEncoding.EncodeToString
			body, err := json.Marshal(map[string]any{"v": 2, "host": host, "port": n, "invite": enc(token), "pin": enc(pin)})
			if err != nil {
				t.Fatal(err)
			}
			i, err := enrollment.Parse("nexus://invite/v2#" + enc(body))
			if err != nil {
				t.Fatal(err)
			}
			return i
		}
		request := make([]byte, 16)
		rand.Read(request)
		pub := elliptic.MarshalCompressed(key.Curve, key.X, key.Y)
		if _, err := enrollment.Redeem(invite(wrong), l.endpoint, nil, request, pub, deviceSigner{key}); mobile.ErrorCode(err) != int32(transport.Certificate) {
			t.Fatalf("pinned redemption rejection code %d", mobile.ErrorCode(err))
		}
		receipt, err := enrollment.Redeem(invite(digest[:]), l.endpoint, nil, request, pub, deviceSigner{key})
		if err != nil {
			t.Fatal(err)
		}
		id, err = hex.DecodeString(receipt.DeviceID)
		if err != nil {
			t.Fatal(err)
		}
		// An existing device can still prove possession after a node restart.
		l.stop(t, os.Kill)
		l.start(t)
		for _, mode := range []string{"h2", "h3"} {
			t.Run(mode, func(t *testing.T) {
				legacy, e := mobile.Start(l.authority, l.endpoint, mode, ca, id, nil, deviceSigner{key}, nil, nil, nil)
				if e != nil {
					t.Fatal("legacy IP CA:", e)
				}
				legacy.Stop()
				if legacy, e = mobile.Start(l.authority, l.endpoint, mode, nil, id, nil, deviceSigner{key}, nil, nil, nil); mobile.ErrorCode(e) != int32(transport.Certificate) {
					if legacy != nil {
						legacy.Stop()
					}
					t.Fatalf("legacy accepted untrusted IP; error %v", e)
				}
				refused(t, l.authority, mode, nil, nil)
				refused(t, l.authority, mode, nil, wrong)
				refused(t, net.JoinHostPort("127.0.0.2", port), mode, nil, digest[:])
				c, e := start(l.authority, mode, nil, digest[:])
				if e != nil {
					t.Fatal(e)
				}
				defer c.Stop()
				timer := time.AfterFunc(5*time.Second, c.Stop)
				defer timer.Stop()
				if err := c.Confirm(); err != nil {
					t.Fatal(err)
				}
				s, err := c.DialTCP(l.tcp, []byte("echo"))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				var got []byte
				for len(got) < 4 {
					b, e := s.Read(4 - len(got))
					if e != nil {
						t.Fatal(e)
					}
					got = append(got, b...)
				}
				if string(got) != "echo" {
					t.Fatal("invalid echo")
				}
			})
		}
		t.Run("auto-stops-after-peer-certificate-changes", func(t *testing.T) {
			refused(t, l.authority, "auto", nil, wrong)
			c, err := start(l.authority, "auto", nil, digest[:])
			if err != nil {
				t.Fatal(err)
			}
			defer c.Stop()
			if err = c.Confirm(); err != nil {
				t.Fatal(err)
			}
			l.stop(t, os.Interrupt)
			newKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			newCert := &x509.Certificate{SerialNumber: cert.SerialNumber, IPAddresses: cert.IPAddresses, NotBefore: cert.NotBefore, NotAfter: cert.NotAfter}
			der, err := x509.CreateCertificate(rand.Reader, newCert, newCert, &newKey.PublicKey, newKey)
			if err != nil {
				t.Fatal(err)
			}
			pk, err := x509.MarshalPKCS8PrivateKey(newKey)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(l.dir, "server.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(l.dir, "server.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), 0600); err != nil {
				t.Fatal(err)
			}
			l.start(t)
			until := time.Now().Add(8 * time.Second)
			for time.Now().Before(until) {
				err = c.Confirm()
				if mobile.ErrorCode(err) == int32(transport.Certificate) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if mobile.ErrorCode(err) != int32(transport.Certificate) || c.State() != "stopped" {
				t.Fatalf("reconnect returned code %d, state %s", mobile.ErrorCode(err), c.State())
			}
			// With the listener gone, any accidental redial would now be a network
			// failure. Every subsequent operation must retain the certificate cause.
			l.stop(t, os.Kill)
			_, tcpErr := c.DialTCP(l.tcp, nil)
			_, udpErr := c.OpenUDP(l.udp)
			for _, e := range []error{c.Confirm(), tcpErr, udpErr} {
				if mobile.ErrorCode(e) != int32(transport.Certificate) {
					t.Fatalf("lost terminal certificate error: %v", e)
				}
			}
		})
	})
}
