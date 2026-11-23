package server

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/proof"
)

func TestQuotaReservationsAndRecovery(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "ledger.db")
	open := func(path string) *Ledger {
		t.Helper()
		l, err := OpenLedger(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		return l
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	l, admin := open(path), open(path)
	check(l.startQuotaNode())
	defer l.stopQuotaNode()
	user := proof.ID{1}
	check(l.CreateUser(ctx, user))
	limit := int64(9 << 20)
	check(admin.SetQuota(ctx, user, &limit))
	var ids [8]proof.ID
	var amounts [8]int64
	var wg sync.WaitGroup
	for i := range ids {
		wg.Go(func() {
			conn := l
			if i%2 != 0 {
				conn = admin
			}
			var err error
			ids[i], amounts[i], _, err = conn.reserveQuota(ctx, user)
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var held int64
	var first, tail proof.ID
	for i, n := range amounts {
		switch n {
		case 8 << 20:
			first = ids[i]
		case 1 << 20:
			tail = ids[i]
		case 0:
		default:
			t.Fatal("wrong grant", n)
		}
		held += n
	}
	if held != limit || first == (proof.ID{}) || tail == (proof.ID{}) {
		t.Fatal("concurrent grants", amounts)
	}
	// A lower limit cannot grant more credit against the old reservations.
	small := int64(1024)
	check(admin.SetQuota(ctx, user, &small))
	_, grant, _, err := l.reserveQuota(ctx, user)
	if err != nil || grant != 0 {
		t.Fatal("credit granted below live reservations", grant, err)
	}
	check(admin.SetQuota(ctx, user, &limit))
	check(l.settleQuota(ctx, first, 90, false))
	check(l.settleQuota(ctx, first, 75, false))
	check(l.settleQuota(ctx, first, 90, false))
	check(l.settleQuota(ctx, first, 100, true))
	check(l.settleQuota(ctx, first, 100, true))
	if l.settleQuota(ctx, first, 101, true) == nil {
		t.Fatal("advanced sealed lease")
	}
	check(l.settleQuota(ctx, tail, 17, false))
	if err := admin.startQuotaNode(); err == nil {
		t.Fatal("second owner took live reservation")
	}
	check(admin.Reconcile(ctx))
	check(l.settleQuota(ctx, tail, 19, false))
	dst := filepath.Join(t.TempDir(), "backup db")
	check(admin.Backup(ctx, dst))
	if err := admin.Backup(ctx, dst); !errors.Is(err, os.ErrExist) {
		t.Fatal("overwrote backup", err)
	}
	info, err := os.Stat(dst)
	check(err)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	restored := open(dst)
	check(restored.startQuotaNode())
	defer restored.stopQuotaNode()
	assertUsers := func(l *Ledger, used string) {
		t.Helper()
		var out bytes.Buffer
		check(l.List(ctx, "users", &out))
		if !strings.Contains(out.String(), `"used_bytes":`+used+`}`) {
			t.Fatal(out.String())
		}
	}
	assertUsers(l, "119")
	assertUsers(restored, "1048676")
	check(restored.Reconcile(ctx))
	check(restored.Reconcile(ctx))
	assertUsers(restored, "119")
	var count int
	check(restored.db.QueryRow("SELECT count(*) FROM audit WHERE action='reconcile' AND bytes=?", (1<<20)-19).Scan(&count))
	if count != 1 {
		t.Fatal("release audit count", count)
	}
}

func TestAdminAtomicityAndRevoke(t *testing.T) {
	ctx := t.Context()
	l, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	u := proof.ID{1}
	if err := l.CreateUserUntil(ctx, u, time.Now().Add(-time.Second)); err == nil {
		t.Fatal("past expiry accepted")
	}
	if err := l.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	a, b := enroll(t, l, u), enroll(t, l, u)
	if err := l.Revoke(ctx, proof.ID{}); err == nil || errors.Is(err, ErrAlreadyRevoked) {
		t.Fatal("missing device", err)
	}
	if err := l.Revoke(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := l.Revoke(ctx, a); !errors.Is(err, ErrAlreadyRevoked) {
		t.Fatal("duplicate revoke", err)
	}
	if _, err := l.db.Exec("CREATE TRIGGER reject_audit BEFORE INSERT ON audit BEGIN SELECT RAISE(ABORT,'audit fault'); END"); err != nil {
		t.Fatal(err)
	}
	if err := l.RevokeUser(ctx, u); err == nil {
		t.Fatal("audit fault lost")
	}
	v := int64(3)
	if err := l.SetQuota(ctx, u, &v); err == nil {
		t.Fatal("set quota without audit")
	}
	var unchanged int
	if err := l.db.QueryRow("SELECT (SELECT quota_bytes IS NULL FROM users) AND (SELECT revoked_at IS NULL FROM devices WHERE id=?)", b[:]).Scan(&unchanged); err != nil || unchanged != 1 {
		t.Fatal("not rolled back", err)
	}
	if _, err := l.db.Exec("DROP TRIGGER reject_audit"); err != nil {
		t.Fatal(err)
	}
	if err := l.RevokeUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := l.db.QueryRow("SELECT count(*) FROM devices WHERE revoked_at IS NULL").Scan(&n); err != nil || n != 0 {
		t.Fatal("surviving device", err)
	}
}

func TestQuotaOwnerSurvivesSecondOpen(t *testing.T) {
	if path := os.Getenv("NEXUS_TEST_OWNED_LEDGER"); path != "" {
		second, err := OpenLedger(path)
		if err != nil {
			t.Fatal(err)
		}
		defer second.Close()
		if err := second.startQuotaNode(); err == nil {
			second.stopQuotaNode()
			t.Fatal("second process acquired live ledger")
		}
		return
	}
	path := filepath.Join(t.TempDir(), "node.db")
	node, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if err := node.startQuotaNode(); err != nil {
		t.Fatal(err)
	}
	defer node.stopQuotaNode()
	reload, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reload.Close()
	if err := reload.startQuotaNode(); err == nil {
		t.Fatal("reload acquired live ledger")
	}
	alias := filepath.Join(filepath.Dir(path), "alias")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, alias} {
		child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestQuotaOwnerSurvivesSecondOpen$")
		child.Env = append(os.Environ(), "NEXUS_TEST_OWNED_LEDGER="+p)
		if out, err := child.CombinedOutput(); err != nil {
			t.Fatalf("owner check %s: %v: %s", filepath.Base(p), err, out)
		}
	}
}
