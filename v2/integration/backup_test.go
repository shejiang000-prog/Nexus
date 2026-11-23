package integration

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOnlineBackupRestore(t *testing.T) {
	testNode(t, true, false, func(t *testing.T, l *nodeLab) {
		token, id, key := l.enroll(t, "h2")
		client := l.connect(t, "h2", id, key)
		stream, err := client.DialTCP(t.Context(), l.tcp, []byte("live"))
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		var echo [4]byte
		if _, err = io.ReadFull(stream, echo[:]); err != nil || string(echo[:]) != "live" {
			t.Fatalf("active stream: %q %v", echo, err)
		}
		want := make(map[string][]byte)
		for _, name := range []string{"users", "devices", "audit"} {
			want[name] = l.admin(t, name)
		}
		backup := filepath.Join(t.TempDir(), "backup.db")
		l.admin(t, "backup", backup)
		l.stop(t, os.Interrupt)
		if err := l.ledger.Close(); err != nil {
			t.Fatal(err)
		}
		l.ledger = nil
		for _, suffix := range []string{"", "-shm", "-wal", "-journal"} {
			if err := os.Rename(l.dbPath+suffix, l.dbPath+".before"+suffix); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		if err := os.Rename(backup, l.dbPath); err != nil {
			t.Fatal(err)
		}
		l.start(t)
		for name, expected := range want {
			if !bytes.Equal(l.admin(t, name), expected) {
				t.Fatalf("restored %s differ", name)
			}
		}
		other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		testRedemption(t, "h2", l.endpoint, l.authority, l.roots, token, other, false)
		restored := l.connect(t, "h2", id, key)
		s, err := restored.DialTCP(t.Context(), l.tcp, []byte("back"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err = io.ReadFull(s, echo[:]); err != nil || string(echo[:]) != "back" {
			t.Fatalf("restored stream: %q %v", echo, err)
		}
	})
}
