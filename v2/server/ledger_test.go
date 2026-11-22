package server

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/proof"
)

func TestInvitationTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	db, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx := t.Context()
	user := proof.ID{1}
	if err = db.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	invite, err := db.Invite(ctx, user, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := elliptic.MarshalCompressed(key.Curve, key.X, key.Y)
	sign := func(secret []byte, request proof.ID) []byte {
		msg, e := proof.RedeemMessage(secret, request, sha256.Sum256(invite), pub)
		if e != nil {
			t.Fatal(e)
		}
		digest := sha256.Sum256(msg)
		r, s, e := ecdsa.Sign(rand.Reader, key, digest[:])
		if e != nil {
			t.Fatal(e)
		}
		return append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	}
	secret := make([]byte, 32)
	request := proof.ID{3}
	sig := sign(secret, request)
	if _, err := db.redeem(ctx, bytes.Repeat([]byte{1}, 32), invite, pub, sig, request, ""); err == nil {
		t.Fatal("cross-connection replay")
	}
	var wg sync.WaitGroup
	var ids [16]proof.ID
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn := db
			if i%2 == 1 {
				conn = other
			}
			id, e := conn.redeem(ctx, secret, invite, pub, sig, request, "device")
			if e != nil {
				t.Error(e)
			}
			ids[i] = id
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] || db.Lookup(id) == nil {
			t.Fatal("duplicate redemption")
		}
	}
	count := func(query string, want int) {
		t.Helper()
		var got int
		if err := db.db.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: %d %v", query, got, err)
		}
	}
	count("SELECT remaining FROM invites", 0)
	count("SELECT count(*) FROM devices", 1)
	count("SELECT count(*) FROM audit", 1)
	another := proof.ID{4}
	if _, err := db.redeem(ctx, secret, invite, pub, sign(secret, another), another, ""); err == nil {
		t.Fatal("spent invite accepted")
	}
	fresh := bytes.Repeat([]byte{2}, 32)
	got, err := other.redeem(ctx, fresh, invite, pub, sign(fresh, request), request, "")
	if err != nil || got != ids[0] {
		t.Fatal("idempotent retry", err)
	}
	if _, err = db.db.Exec("UPDATE invites SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.redeem(ctx, secret, invite, pub, sig, request, ""); err == nil {
		t.Fatal("expired retry accepted")
	}
	if db.Lookup(got) == nil {
		t.Fatal("invite expiry ended enrolled device")
	}
	// Different identities racing a fresh invitation must have exactly one winner.
	invite, err = db.Invite(ctx, user, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	successes := make(chan proof.ID, 8)
	for i := range 8 {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		key = k
		pub = elliptic.MarshalCompressed(key.Curve, key.X, key.Y)
		req := proof.ID{byte(i + 10)}
		point, sig := pub, sign(secret, req)
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn := db
			if i%2 == 1 {
				conn = other
			}
			if id, err := conn.redeem(ctx, secret, invite, point, sig, req, ""); err == nil {
				successes <- id
			}
		}()
	}
	wg.Wait()
	close(successes)
	if len(successes) != 1 {
		t.Fatalf("distinct winners %d", len(successes))
	}
	independent := <-successes
	defer func() {
		if db.Lookup(independent) == nil {
			t.Error("unrelated device was revoked")
		}
	}()
	if err = other.Revoke(ctx, got); err != nil || db.Lookup(got) != nil {
		t.Fatal("persisted revocation", err)
	}
}

func TestInvalidRedeemFallsThroughWithoutBody(t *testing.T) {
	h := &Handler{Authority: "node.test:443", ledger: &Ledger{}}
	for _, data := range []string{`{"invite":"dummy","invite":"duplicate"}`, strings.Repeat("a", 4097), `{"proof":123}`, `{}`, `{}{}`} {
		req := httptest.NewRequest("POST", "https://node.test:443/.well-known/nexus/v2/redeem", strings.NewReader(data))
		req.TLS = &tls.ConnectionState{HandshakeComplete: true, Version: tls.VersionTLS13}
		called := false
		err := h.ServeHTTP(httptest.NewRecorder(), req, caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			called = true
			body, e := io.ReadAll(r.Body)
			if e != nil || len(body) != 0 || r.ContentLength != 0 {
				t.Error("credential body forwarded")
			}
			return nil
		}))
		if err != nil || !called {
			t.Fatal("ordinary site fallback missing")
		}
	}
}
