package server

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shejiang000-prog/Nexus/v2/proof"
)

func TestQuotaPartialWritesAndDatagramBoundary(t *testing.T) {
	l, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	u := proof.ID{42}
	ctx := context.Background()
	if err := l.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	limit := int64(8<<20) + 4
	if err := l.SetQuota(ctx, u, &limit); err != nil {
		t.Fatal(err)
	}
	a := &quotaAccount{ledger: l, user: u}
	if err := a.admit(ctx); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("partial write")
	n, err := a.write(ctx, make([]byte, 32), false, func([]byte) (int, error) { return 3, wantErr })
	if n != 3 || err != wantErr {
		t.Fatalf("partial %d %v", n, err)
	}
	n, err = a.write(ctx, make([]byte, 32), false, func([]byte) (int, error) { return 2, nil })
	if n != 2 || err != io.ErrShortWrite {
		t.Fatalf("short %d %v", n, err)
	}
	write := func(p []byte) (int, error) { return len(p), nil }
	if _, err := a.write(ctx, make([]byte, (8<<20)-7), false, write); err != nil {
		t.Fatal(err)
	}
	if n, err = a.write(ctx, make([]byte, 5), true, write); err != nil || n != 5 {
		t.Fatalf("whole datagram across renewal: %d %v", n, err)
	}
	if n, err = a.write(ctx, make([]byte, 2), true, func([]byte) (int, error) { t.Fatal("partial datagram forwarded"); return 0, nil }); n != 0 || !errors.Is(err, errQuota) {
		t.Fatalf("oversized %d %v", n, err)
	}
	if err := a.checkpoint(true); err != nil {
		t.Fatal(err)
	}
	var used int64
	if err := l.db.QueryRow("SELECT used_bytes FROM users WHERE id=?", u[:]).Scan(&used); err != nil || used != limit-1 {
		t.Fatalf("settlement: %d %v", used, err)
	}
}

// Count the bytes accepted by the payload sink independently from SQLite.
func TestConcurrentDevicesSharePayloadBudget(t *testing.T) {
	l, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx := t.Context()
	user := proof.ID{4}
	if err := l.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	limit := int64(491521)
	if err := l.SetQuota(ctx, user, &limit); err != nil {
		t.Fatal(err)
	}
	var writers []*quotaAccount

	for range 6 {
		id := enroll(t, l, user)
		a, done, ok := l.register(id, func() {})
		if !ok {
			t.Fatal("register")
		}
		defer done()
		writers = append(writers, a)
	}

	var actual atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, a := range writers {
		wg.Go(func() {
			<-start
			p := make([]byte, 7001)
			for {
				_, err := a.write(ctx, p, false, func(b []byte) (int, error) {
					actual.Add(int64(len(b)))
					return len(b), nil
				})
				if err != nil {
					if !errors.Is(err, errQuota) {
						t.Error(err)
					}
					return
				}
			}
		})
	}
	close(start)
	wg.Wait()
	if got := actual.Load(); got != limit {
		t.Fatalf("sink accepted %d payload bytes, limit %d", got, limit)
	}
}
