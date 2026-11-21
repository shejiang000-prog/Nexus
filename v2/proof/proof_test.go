package proof

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"sync"
	"testing"
)

type keys map[ID]*ecdsa.PublicKey

func (k keys) Lookup(id ID) *ecdsa.PublicKey { return k[id] }

func TestInterop(t *testing.T) {
	data, e := os.ReadFile("testdata/connection.json")
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Authority, Label, Context, Exporter               string
		Device, Public, Message, Signature, Authorization string
		Request, Redeem                                   string
		IPv6                                              string   `json:"ipv6_authority"`
		IPv6Context                                       string   `json:"ipv6_context"`
		Hash                                              string   `json:"invite_hash"`
		RedeemSig                                         string   `json:"redeem_signature"`
		Reject                                            []string `json:"rejected_headers"`
		Input                                             struct {
			Invite, Proof string
			Public        string `json:"public_key"`
			ID            string `json:"request_id"`
		} `json:"redeem_request"`
	}
	if e = json.Unmarshal(data, &v); e != nil {
		t.Fatal(e)
	}
	b := func(s string) []byte {
		p, e := hex.DecodeString(s)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	secret := b(v.Exporter)
	id := ID(b(v.Device))
	x, y := elliptic.UnmarshalCompressed(elliptic.P256(), b(v.Public))
	k := keys{id: {Curve: elliptic.P256(), X: x, Y: y}}
	for a, expected := range map[string]string{v.Authority: v.Context, v.IPv6: v.IPv6Context} {
		got, e := Secret(a, func(l string, ctx []byte, n int) ([]byte, error) {
			if l != v.Label || hex.EncodeToString(ctx) != expected || n != 32 {
				t.Fatal("exporter arguments")
			}
			return secret, nil
		})
		if e != nil || !bytes.Equal(got, secret) {
			t.Fatal(e)
		}
	}
	h, e := Create(secret, id, func(m []byte) ([]byte, error) {
		if !bytes.Equal(m, b(v.Message)) {
			t.Fatal("connection message")
		}
		return b(v.Signature), nil
	})
	if e != nil || h != v.Authorization {
		t.Fatal("header")
	}
	local := func() ([]byte, error) { return secret, nil }
	for _, bad := range append(v.Reject, h+"=", "", h+"x") {
		if new(Binding).Accept(bad, k, local) {
			t.Fatal("accepted malformed proof")
		}
	}
	if new(Binding).Accept(h, k, func() ([]byte, error) { return make([]byte, 32), nil }) {
		t.Fatal("cross-connection replay")
	}
	var binding Binding
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !binding.Accept(h, k, local) {
				t.Error("concurrent first request")
			}
		}()
	}
	wg.Wait()
	if !binding.Accept("", k, nil) || binding.Accept(v.Reject[3], k, nil) {
		t.Fatal("bound identity")
	}
	delete(k, id)
	if binding.Accept("", k, nil) {
		t.Fatal("revoked identity")
	}
	decode := base64.RawURLEncoding.Strict().DecodeString
	invite, err := decode(v.Input.Invite)
	if err != nil || len(invite) != 32 || sha256.Sum256(invite) != [32]byte(b(v.Hash)) {
		t.Fatal("invite digest")
	}
	for value, want := range map[string]string{v.Input.Public: v.Public, v.Input.ID: v.Request, v.Input.Proof: v.RedeemSig} {
		got, err := decode(value)
		if err != nil || !bytes.Equal(got, b(want)) {
			t.Fatal("redeem request")
		}
	}
	msg, e := RedeemMessage(secret, ID(b(v.Request)), [32]byte(b(v.Hash)), b(v.Public))
	if e != nil || !bytes.Equal(msg, b(v.Redeem)) {
		t.Fatal("invite message")
	}
	d := sha256.Sum256(msg)
	sig := b(v.RedeemSig)
	if !ecdsa.Verify(&ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, d[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("invite signature")
	}
	if _, e = RedeemMessage(secret, id, [32]byte{}, []byte{1}); e == nil {
		t.Fatal("invalid public key")
	}
}
