package enrollment

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shejiang000-prog/Nexus/v2/proof"
)

func TestInvitation(t *testing.T) {
	payload := `{"v":2,"host":"example.test","port":443,"invite":"` + b64.EncodeToString(make([]byte, 32)) + `","name":"Home"}`
	link := func(s string) string { return prefix + b64.EncodeToString([]byte(s)) }
	i, err := Parse(link(payload))
	if err != nil || i.Authority() != "example.test:443" || i.Host() != "example.test" || i.Name() != "Home" {
		t.Fatal("valid invite rejected")
	}
	pinned := link(strings.Replace(payload, `"v":2`, `"v":2,"pin":"fixture"`, 1))
	if _, err := Parse(pinned); err == nil || err.Error() != "此版本尚不支持带证书固定值的邀请" {
		t.Fatal("pin must be refused explicitly before enrollment")
	}
	for _, bad := range []string{
		link(strings.Replace(payload, `"v":2`, `"v":2,"v":2`, 1)), link(strings.Replace(payload, `"v":2`, `"v":3`, 1)),
		link(strings.Replace(payload, "443", "0", 1)), link(strings.Replace(payload, "443", "65536", 1)), link(strings.Replace(payload, "example.test", "example.test/path", 1)),
		link(strings.Replace(payload, "example.test", "127.0.0.1", 1)), link(strings.Replace(payload, `"invite":"`, `"invite":"X`, 1)), link(strings.Replace(payload, "Home", "\xff", 1)),
		link(payload + payload), link(strings.Repeat(" ", 4097)), link(payload) + "=", strings.Replace(link(payload), "#", "?", 1),
		link(strings.Replace(payload, `"v":2`, `"v":2,"transport":"unknown"`, 1)),
	} {
		if _, err := Parse(bad); err == nil {
			t.Fatal("invalid invitation accepted")
		}
	}
}

type testKey struct{ *ecdsa.PrivateKey }

func (k testKey) Sign(msg []byte) ([]byte, error) {
	h := sha256.Sum256(msg)
	return ecdsa.SignASN1(rand.Reader, k.PrivateKey, h[:])
}

func TestTLSBoundRedemption(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token, req := make([]byte, 32), make([]byte, 16)
	rand.Read(token)
	rand.Read(req)
	device := make([]byte, 16)
	rand.Read(device)
	pub := elliptic.MarshalCompressed(key.Curve, key.X, key.Y)
	for _, tc := range []struct {
		field string
		known bool
		until int64
	}{{"", false, 0}, {`,"expires_at":null`, true, 0}, {`,"expires_at":1812345678`, true, 1812345678}} {
		node := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var f map[string]string
			if r.ProtoMajor != 2 || r.URL.Path != "/.well-known/nexus/v2/redeem" || r.Method != "POST" || json.NewDecoder(r.Body).Decode(&f) != nil {
				w.WriteHeader(400)
				return
			}
			secret, err := proof.Secret(r.Host, r.TLS.ExportKeyingMaterial)
			msg, _ := proof.RedeemMessage(secret, proof.ID(req), sha256.Sum256(token), pub)
			sig, _ := from64(f["proof"])
			if err != nil || !validRedemption(msg, sig, key) || f["invite"] != b64.EncodeToString(token) || f["request_id"] != b64.EncodeToString(req) || f["public_key"] != b64.EncodeToString(pub) {
				w.WriteHeader(403)
				return
			}
			fmt.Fprintf(w, `{"v":2,"device_id":"%s"%s}`, b64.EncodeToString(device), tc.field)
		}))
		node.EnableHTTP2 = true
		node.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
		node.StartTLS()
		cert := node.Certificate()
		i := &Invitation{host: cert.DNSNames[0], authority: cert.DNSNames[0] + ":443", token: token}
		got, err := Redeem(i, node.Listener.Addr().String(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), req, pub, testKey{key})
		node.Close()
		if err != nil || got.ExpiresAt != tc.until || got.ExpiryKnown != tc.known || len(got.DeviceID) != 32 {
			t.Fatal("TLS redemption", err)
		}
	}
}

func validRedemption(msg, sig []byte, key *ecdsa.PrivateKey) bool {
	if len(sig) != 64 {
		return false
	}
	hash := sha256.Sum256(msg)
	return ecdsa.Verify(&key.PublicKey, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
}
