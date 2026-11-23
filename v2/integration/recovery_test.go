package integration

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLeaseRecovery(t *testing.T) {
	for _, mode := range []string{"sigkill", "online-backup"} {
		t.Run(mode, func(t *testing.T) {
			testNode(t, true, false, func(t *testing.T, l *nodeLab) {
				const quota = 1 << 20
				l.admin(t, "set-quota", hex.EncodeToString(l.user[:]), "1048576")
				token, id, key := l.enroll(t, "h2")
				client := l.connect(t, "h2", id, key)
				stream, err := client.DialTCP(t.Context(), l.tcp, []byte("abcd"))
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				echo := func(s string) {
					b := make([]byte, len(s))
					if _, err := io.ReadFull(stream, b); err != nil || string(b) != s {
						t.Fatalf("echo: %q %v", b, err)
					}
				}
				echo("abcd")
				// Observe a periodic checkpoint while the stream is still open.
				deadline := time.Now().Add(8 * time.Second)
				for {
					_, c, state := leaseRow(t, l.dbPath)
					if c == 8 && state == "active" {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("checkpoint: %d, %s", c, state)
					}
					time.Sleep(50 * time.Millisecond)
				}
				devices, audit := l.admin(t, "devices"), l.admin(t, "audit")
				if _, err = stream.Write([]byte("z")); err != nil {
					t.Fatal(err)
				}
				echo("z")
				backup := filepath.Join(t.TempDir(), "copy.db")
				if mode == "online-backup" {
					l.admin(t, "backup", backup)
					reserved, committed, state := leaseRow(t, backup)
					if reserved != quota || committed != 8 || state != "active" {
						t.Fatalf("backup lease: %d %d %s", reserved, committed, state)
					}
					l.stop(t, os.Interrupt)
					_, committed, state = leaseRow(t, l.dbPath)
					if committed != 10 || state != "closed" {
						t.Fatalf("graceful shutdown: %d %s", committed, state)
					}
					if err := l.ledger.Close(); err != nil {
						t.Fatal(err)
					}
					l.ledger = nil
					// Move the entire stopped SQLite family aside before installing the snapshot.
					for _, suffix := range []string{"", "-shm", "-wal", "-journal"} {
						if err := os.Rename(l.dbPath+suffix, l.dbPath+".before"+suffix); err != nil && !os.IsNotExist(err) {
							t.Fatal(err)
						}
					}
					if err := os.Rename(backup, l.dbPath); err != nil {
						t.Fatal(err)
					}
				} else {
					l.stop(t, os.Kill)
				}
				l.start(t)
				r, c, state := leaseRow(t, l.dbPath)
				if r != quota || c != 8 || state != "frozen" {
					t.Fatalf("recovered lease: %d %d %s", r, c, state)
				}
				if !bytes.Equal(devices, l.admin(t, "devices")) {
					t.Fatal("restored devices differ")
				}
				var u struct {
					ID    string
					Quota int64 `json:"quota_bytes"`
					Used  int64 `json:"used_bytes"`
				}
				checkUsed := func(want int64) {
					if err := json.Unmarshal(l.admin(t, "users"), &u); err != nil || u.ID != hex.EncodeToString(l.user[:]) || u.Quota != quota || u.Used != want {
						t.Fatalf("users mismatch: used=%d quota=%d err=%v", u.Used, u.Quota, err)
					}
				}
				checkUsed(quota)
				l.admin(t, "reconcile")
				checkUsed(c)
				after := l.admin(t, "audit")
				if !bytes.HasPrefix(after, audit) || bytes.Equal(after, audit) {
					t.Fatal("audit history mismatch")
				}
				for i, line := range bytes.Split(bytes.TrimSpace(after), []byte("\n")) {
					var a struct{ Seq int }
					if err := json.Unmarshal(line, &a); err != nil || a.Seq != i+1 {
						t.Fatal("audit sequence discontinuity")
					}
				}
				l.admin(t, "reconcile")
				if !bytes.Equal(after, l.admin(t, "audit")) {
					t.Fatal("duplicate reconcile changed audit")
				}
				other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				testRedemption(t, "h2", l.endpoint, l.authority, l.roots, token, other, false)
				// The original device can use the released balance.
				cl := l.connect(t, "h2", id, key)
				s, err := cl.DialTCP(t.Context(), l.tcp, []byte("r"))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				var b [1]byte
				if _, err = io.ReadFull(s, b[:]); err != nil || b[0] != 'r' {
					t.Fatal("restored device echo", err)
				}
				t.Logf("reserved=%d checkpoint=%d frozen charge=%d reconciled=%d audit rows=%d", r, c, quota, c, bytes.Count(after, []byte("\n")))
			})
		})
	}
}

func leaseRow(t *testing.T, dbPath string) (reserved, committed int64, state string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.QueryRow("SELECT reserved_bytes,committed_bytes,state FROM quota_leases").Scan(&reserved, &committed, &state)
	if err != nil {
		t.Fatal(err)
	}
	return
}
