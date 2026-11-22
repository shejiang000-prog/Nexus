package server

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	count("SELECT count(*) FROM audit WHERE action='redeem'", 1)
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
	if err = other.Revoke(ctx, got); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for db.Lookup(got) != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if db.Lookup(got) != nil {
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

func enroll(t *testing.T, l *Ledger, user proof.ID) proof.ID {
	t.Helper()
	invite, err := l.Invite(t.Context(), user, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := elliptic.MarshalCompressed(key.Curve, key.X, key.Y)
	var req proof.ID
	rand.Read(req[:])
	secret := make([]byte, 32)
	msg, err := proof.RedeemMessage(secret, req, sha256.Sum256(invite), pub)
	if err != nil {
		t.Fatal(err)
	}
	d := sha256.Sum256(msg)
	r, s, err := ecdsa.Sign(rand.Reader, key, d[:])
	if err != nil {
		t.Fatal(err)
	}
	id, err := l.redeem(t.Context(), secret, invite, pub, append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...), req, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestExclusiveLockKeepsUnrevokedFlows(t *testing.T) {
	for _, hold := range []time.Duration{600 * time.Millisecond, 3 * time.Second} {
		t.Run(hold.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ledger.db")
			l, err := OpenLedger(path)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			admin, err := OpenLedger(path)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close()
			u := proof.ID{7}
			if err := l.CreateUser(t.Context(), u); err != nil {
				t.Fatal(err)
			}
			keep, remove := enroll(t, l, u), enroll(t, l, u)
			kept, removed := make(chan struct{}), make(chan struct{})
			for id, ch := range map[proof.ID]chan struct{}{keep: kept, remove: removed} {
				done, ok := l.register(id, func() { close(ch) })
				if !ok {
					t.Fatal("register")
				}
				defer done()
			}
			if _, err := l.db.Exec("PRAGMA busy_timeout=100"); err != nil {
				t.Fatal(err)
			}
			conn, err := admin.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			for _, q := range []string{"BEGIN EXCLUSIVE", "UPDATE devices SET revoked_at=1 WHERE id=?", "INSERT INTO audit(occurred_at,action,device_id) VALUES(1,'revoke',?)"} {
				var args []any
				if strings.Contains(q, "?") {
					args = []any{remove[:]}
				}
				if _, err := conn.ExecContext(t.Context(), q, args...); err != nil {
					t.Fatal(err)
				}
			}
			time.Sleep(hold)
			select {
			case <-removed:
				t.Error("uncommitted revocation disconnected flow")
			default:
			}
			if _, err := conn.ExecContext(t.Context(), "COMMIT"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-removed:
			case <-time.After(time.Second):
				t.Fatal("late revocation")
			}
			select {
			case <-kept:
				t.Fatal("unrevoked flow disconnected")
			default:
			}
		})
	}
}

func TestExpiryAndMonitorOutage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	exp := time.Now().Add(2 * time.Second).Truncate(time.Second)
	for i, until := range []time.Time{exp, {}} {
		user := proof.ID{byte(i)}
		if err := l.CreateUserUntil(t.Context(), user, until); err != nil {
			t.Fatal(err)
		}
		id := enroll(t, l, user)
		done := make(chan struct{})
		unregister, ok := l.register(id, func() { close(done) })
		if !ok {
			t.Fatal("register")
		}
		defer unregister()
		if i == 0 {
			select {
			case <-done:
				if time.Now().Before(exp) {
					t.Fatal("premature expiry")
				}
			case <-time.After(time.Until(exp) + time.Second):
				t.Fatal("late expiry")
			}
			if _, ok := l.register(id, func() {}); ok || l.Lookup(id) != nil {
				t.Fatal("expired device accepted")
			}
		} else {
			if err := os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			select {
			case <-done:
				if time.Since(start) < ledgerFailureLimit {
					t.Fatal("premature fault shutdown")
				}
			case <-time.After(ledgerFailureLimit + 2*time.Second):
				t.Fatal("stale authorization")
			}
			if l.Lookup(id) != nil {
				t.Fatal("fault admission")
			}
			if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			end := time.Now().Add(time.Second)
			for l.Lookup(id) == nil && time.Now().Before(end) {
				time.Sleep(10 * time.Millisecond)
			}
			if l.Lookup(id) == nil {
				t.Fatal("recovery failed")
			}
		}
	}
}
func TestSourceLimit(t *testing.T) {
	var s redeemSources
	now := time.Now()
	for range 16 {
		if !s.allow("127.0.0.1:1", now) {
			t.Fatal("early limit")
		}
	}
	if s.allow("[::ffff:127.0.0.1]:2", now) || !s.allow("127.0.0.2:2", now) || !s.allow("127.0.0.1:3", now.Add(time.Second)) {
		t.Fatal("source isolation or reset")
	}
}

func TestV1AuditMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	user := proof.ID{3}
	if err = l.CreateUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	id := enroll(t, l, user)
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`DROP TABLE audit;
	CREATE TABLE audit(seq INTEGER PRIMARY KEY,occurred_at INTEGER NOT NULL,action TEXT NOT NULL,device_id BLOB NOT NULL REFERENCES devices(id));
	CREATE INDEX audit_revocations ON audit(seq) WHERE action='revoke';
	INSERT INTO audit(seq,occurred_at,action,device_id) SELECT 42,1,'redeem',id FROM devices;
	PRAGMA user_version=1;`)
	old.Close()
	if err != nil {
		t.Fatal(err)
	}
	l, err = OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if l.Lookup(id) == nil {
		t.Fatal("lost device")
	}
	if err = l.Revoke(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = l.List(t.Context(), "audit", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"seq":42`) || !strings.Contains(out.String(), `"seq":43`) {
		t.Fatal("migration lost sequence")
	}
}
