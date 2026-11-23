package server

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/compat"
	"github.com/shejiang000-prog/Nexus/v2/proof"
)

func TestCompatSchemaFourAndLifecycle(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "ledger.db")
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(os.WriteFile(path, nil, 0600))
	old, err := sql.Open("sqlite", path)
	check(err)
	// Build an actual v4 record set, including a consumed invitation and
	// one revoked device. All four tables must survive the migration.
	_, err = old.Exec(schema)
	check(err)
	_, err = old.Exec(`INSERT INTO users VALUES(zeroblob(16),unixepoch()+3600);
		INSERT INTO invites VALUES(zeroblob(32),zeroblob(16),123,0);
		INSERT INTO devices VALUES(zeroblob(16),zeroblob(32),zeroblob(16),zeroblob(33),'old',123);
		INSERT INTO audit VALUES(77,123,'revoke',zeroblob(16),NULL)`)
	check(err)
	check(old.Close())
	l, err := OpenLedger(path)
	check(err)
	defer l.Close()
	var ok bool
	check(l.db.QueryRow(`SELECT (SELECT user_version FROM pragma_user_version)=5 AND
		(SELECT expires_at>unixepoch() FROM users) AND (SELECT remaining=0 FROM invites)
		AND (SELECT revoked_at=123 AND name='old' FROM devices) AND (SELECT seq=77 AND action='revoke' FROM audit)`).Scan(&ok))
	if !ok {
		t.Fatal("v4 records lost")
	}
	user := proof.ID{}
	for _, p := range []string{"reality", "hysteria2"} {
		check(l.ChangeCompat(ctx, user, p, "enable"))
	}
	if _, err := l.CompatExport(ctx, user); err == nil {
		t.Fatal("exported pending state")
	}
	empty, err := l.CompatSnapshot(ctx, "reality")
	check(err)
	node := empty.RealityKey
	if node == nil || node.Generation != 1 || node.PrivateKey == "" || node.PublicKey == "" || len(node.ShortID) != 16 {
		t.Fatal("missing initial Reality identity")
	}
	var first, second compat.Credential
	apply := func(p string, capture *compat.Credential, fail bool) error {
		return l.ApplyCompat(ctx, p, false, func(s compat.Snapshot) error {
			if len(s.Credentials) != 1 {
				return errors.New("expected enabled credential")
			}
			if capture != nil {
				*capture = s.Credentials[0]
			}
			if fail {
				return errors.New("sensitive child output")
			}
			return nil
		})
	}
	check(apply("reality", &first, false))
	check(apply("hysteria2", nil, false))
	check(l.ChangeCompat(ctx, user, "reality", "rotate"))
	err = apply("reality", &second, true)
	if err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatal("wrong failure handling")
	}
	if first.UUID == second.UUID {
		t.Fatal("rotation retained old material")
	}
	check(l.ChangeCompat(ctx, user, "reality", "rotate"))
	var retry compat.Credential
	check(apply("reality", &retry, false))
	if retry != second || second.Generation != 2 {
		t.Fatal("retry changed pending generation")
	}
	exported, err := l.CompatExport(ctx, user)
	check(err)
	if len(exported) != 2 || exported["reality"].PublicKey != node.PublicKey || exported["reality"].ShortID != node.ShortID {
		t.Fatal("wrong public credential projection")
	}
	status, err := l.CompatStatus(ctx, user)
	check(err)
	if len(status) != 2 || status[1].AppliedGeneration != 2 {
		t.Fatal("wrong ack generation")
	}
	if err := l.ApplyCompat(ctx, "reality", true, func(compat.Snapshot) error { return errors.New("control unavailable") }); err == nil {
		t.Fatal("explicit replay did not call service")
	}
	unconfirmed, err := l.CompatExport(ctx, user)
	check(err)
	if _, ok := unconfirmed["reality"]; ok {
		t.Fatal("exported after failed explicit replay")
	}
	check(apply("reality", &retry, false))
	if retry != second {
		t.Fatal("recovery changed credential")
	}
	_, err = l.db.Exec("UPDATE users SET expires_at=1")
	check(err)
	expired, err := l.CompatSnapshot(ctx, "reality")
	check(err)
	if len(expired.Credentials) != 1 || expired.Credentials[0].ExpiresAt != 1 || expired.Revision != second.Generation {
		t.Fatal("expiry changed snapshot membership or revision")
	}
	if _, err := l.CompatExport(ctx, user); err == nil {
		t.Fatal("exported expired user")
	}
	check(l.RevokeUser(ctx, user))
	if _, err := l.CompatExport(ctx, user); err == nil {
		t.Fatal("revoked credentials exported")
	}
	for _, p := range []string{"reality", "hysteria2"} {
		check(l.ApplyCompat(ctx, p, false, func(s compat.Snapshot) error {
			if len(s.Credentials) != 0 {
				return errors.New("user survived revoke")
			}
			return nil
		}))
	}
	var audit bytes.Buffer
	check(l.List(ctx, "audit", &audit))
	for _, secret := range []string{first.UUID, second.UUID, node.PrivateKey, node.ShortID, exported["hysteria2"].Password} {
		if strings.Contains(audit.String(), secret) {
			t.Fatal("audit exposed credentials")
		}
	}
	if !strings.Contains(audit.String(), `"result":"failed"`) || !strings.Contains(audit.String(), `"action":"compat_revoke-user"`) {
		t.Fatal("missing lifecycle audit")
	}
}

func TestCompatConcurrentApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	m, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	user := proof.ID{1}
	ctx := t.Context()
	if err := l.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := l.ChangeCompat(ctx, user, "hysteria2", "enable"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	resume := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		done <- l.ApplyCompat(ctx, "hysteria2", false, func(s compat.Snapshot) error {
			close(started)
			<-resume
			if s.Revision != 1 || len(s.Credentials) != 1 {
				return errors.New("wrong first snapshot")
			}
			return nil
		})
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("no first application: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("first application stalled")
	}
	// Persist a disable during service I/O, before the old request is acknowledged.
	err = m.ChangeCompat(ctx, user, "hysteria2", "disable")
	if err != nil {
		close(resume)
		<-done
		t.Fatal(err)
	}
	next := make(chan struct{})
	go func() {
		done <- m.ApplyCompat(ctx, "hysteria2", false, func(s compat.Snapshot) error {
			close(next)
			if s.Revision != 2 || len(s.Credentials) != 0 {
				return errors.New("stale second snapshot")
			}
			return nil
		})
	}()
	select {
	case <-next:
		close(resume)
		t.Fatal("overlapping external applications")
	case <-time.After(30 * time.Millisecond):
	}
	close(resume)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	status, err := l.CompatStatus(ctx, user)
	if err != nil || len(status) != 1 || status[0].Enabled || status[0].AppliedGeneration != 2 {
		t.Fatal("stale application acknowledgement", err)
	}
}

func TestRealityNodeRotation(t *testing.T) {
	ctx := t.Context()
	l, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.db"))
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(err)
	defer l.Close()
	original, err := l.CompatSnapshot(ctx, "reality")
	check(err)
	if original.RealityKey == nil || len(original.Credentials) != 0 {
		t.Fatal("empty Reality service lacks a node identity")
	}
	hy, err := l.CompatSnapshot(ctx, "hysteria2")
	check(err)
	if hy.RealityKey != nil {
		t.Fatal("Hysteria2 received a Reality key")
	}
	users := []proof.ID{{1}, {2}}
	for _, user := range users {
		check(l.CreateUser(ctx, user))
		check(l.ChangeCompat(ctx, user, "reality", "enable"))
	}
	var snap compat.Snapshot
	apply := func(s compat.Snapshot) error { snap = s; return nil }
	check(l.ApplyCompat(ctx, "reality", false, apply))
	old := snap
	if !reflect.DeepEqual(original.RealityKey, old.RealityKey) || len(old.Credentials) != 2 || old.Credentials[0].UUID == old.Credentials[1].UUID {
		t.Fatal("invalid shared identity or per-user UUIDs")
	}
	// A node rotation may arrive while an older snapshot is in flight.
	check(l.ApplyCompat(ctx, "reality", true, func(compat.Snapshot) error { return l.RotateRealityKey(ctx) }))
	for _, user := range users {
		if _, err := l.CompatExport(ctx, user); err == nil {
			t.Fatal("stale acknowledgement enabled export")
		}
	}
	if err := l.ApplyCompat(ctx, "reality", false, func(s compat.Snapshot) error { snap = s; return errors.New("upstream unavailable") }); err == nil {
		t.Fatal("missing application failure")
	}
	rotated := snap
	if rotated.RealityKey.Generation != old.RealityKey.Generation+1 || rotated.RealityKey.PrivateKey == old.RealityKey.PrivateKey || rotated.RealityKey.PublicKey == old.RealityKey.PublicKey || rotated.RealityKey.ShortID == old.RealityKey.ShortID || !reflect.DeepEqual(rotated.Credentials, old.Credentials) {
		t.Fatal("wrong node rotation scope")
	}
	check(l.RotateRealityKey(ctx))
	check(l.ApplyCompat(ctx, "reality", false, apply))
	if !reflect.DeepEqual(snap, rotated) {
		t.Fatal("node rotation retry changed the pending generation")
	}
	for _, user := range users {
		c, err := l.CompatExport(ctx, user)
		check(err)
		if c["reality"].PublicKey != rotated.RealityKey.PublicKey || c["reality"].ShortID != rotated.RealityKey.ShortID {
			t.Fatal("exported wrong node generation")
		}
		st, err := l.CompatStatus(ctx, user)
		check(err)
		if len(st) != 1 || st[0].NodeKeyGeneration != 2 || st[0].NodeKeyAppliedGeneration != 2 || st[0].Generation != 1 || st[0].NodeKeyResult != "applied" {
			t.Fatal("missing node status")
		}
	}
	var audit bytes.Buffer
	check(l.List(ctx, "audit", &audit))
	if strings.Count(audit.String(), `"action":"compat_rotate_node_key"`) != 1 || strings.Contains(audit.String(), rotated.RealityKey.PrivateKey) || strings.Contains(audit.String(), rotated.RealityKey.ShortID) {
		t.Fatal("invalid node rotation audit")
	}
	backup := filepath.Join(t.TempDir(), "snapshot.db")
	check(l.Backup(ctx, backup))
	restored, err := OpenLedger(backup)
	check(err)
	defer restored.Close()
	restoredSnapshot, err := restored.CompatSnapshot(ctx, "reality")
	check(err)
	if !reflect.DeepEqual(restoredSnapshot, rotated) {
		t.Fatal("backup lost identity or credentials")
	}
}

func TestRejectExperimentalV5Ledger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err = old.Exec(schema); err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`CREATE TABLE compat_credentials(user_id BLOB,private_key TEXT); INSERT INTO users VALUES(zeroblob(16),NULL); PRAGMA user_version=5`)
	if err != nil {
		t.Fatal(err)
	}
	if ledger, err := OpenLedger(path); err == nil {
		ledger.Close()
		t.Fatal("experimental v5 was accepted")
	} else if !strings.Contains(err.Error(), "back up") || !strings.Contains(err.Error(), "rebuild") {
		t.Fatal("missing recovery instructions", err)
	}
	var intact bool
	if err := old.QueryRow(`SELECT (SELECT COUNT(*) FROM users)=1 AND (SELECT user_version FROM pragma_user_version)=5 AND NOT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='compat_reality_key')`).Scan(&intact); err != nil || !intact {
		t.Fatal("rejected ledger was modified", err)
	}
}

func TestRealityResetSurvivesCoalescingAndPolicyRetry(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "ledger.db")
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	l, err := OpenLedger(path)
	check(err)
	a, b := proof.ID{1}, proof.ID{2}
	check(l.CreateUser(ctx, a))
	check(l.CreateUser(ctx, b))
	check(l.ChangeCompat(ctx, a, "reality", "enable"))
	// Emulate the preceding deployed v5 layout. Open the same database again.
	initial, err := l.CompatSnapshot(ctx, "reality")
	check(err)
	_, err = l.db.Exec("ALTER TABLE compat_reality_key DROP COLUMN reset_revision")
	check(err)
	check(l.Close())
	l, err = OpenLedger(path)
	check(err)
	defer l.Close()
	snapshot := func() compat.Snapshot { t.Helper(); s, err := l.CompatSnapshot(ctx, "reality"); check(err); return s }
	if !reflect.DeepEqual(initial, snapshot()) {
		t.Fatal("v5 extension changed existing identity")
	}
	check(l.ChangeCompatWithPolicy(ctx, a, "reality", "rotate", true))
	soft := snapshot()
	if soft.ResetRevision != 0 {
		t.Fatal("graceful rotation requested disconnect")
	}
	// Simulate an offline rotation followed by an explicit policy escalation.
	check(l.ChangeCompat(ctx, a, "reality", "rotate"))
	hard := snapshot()
	if hard.ResetRevision != hard.Revision || hard.ResetRevision <= soft.Revision || !reflect.DeepEqual(soft.Credentials, hard.Credentials) {
		t.Fatal("pending rotation lost its identity or its reset")
	}
	check(l.ChangeCompat(ctx, a, "reality", "rotate"))
	if !reflect.DeepEqual(hard, snapshot()) {
		t.Fatal("retry generated another reset")
	}
	check(l.ChangeCompat(ctx, b, "reality", "enable"))
	check(l.ChangeCompatWithPolicy(ctx, a, "reality", "disable", true))
	merged := snapshot()
	if merged.ResetRevision != hard.ResetRevision || merged.Revision <= hard.Revision {
		t.Fatal("later graceful update lost a pending reset")
	}
	check(l.RevokeUser(ctx, a))
	strict := snapshot()
	if strict.ResetRevision != strict.Revision || strict.ResetRevision <= merged.Revision {
		t.Fatal("revocation failed to close retained sessions")
	}
	check(l.ChangeCompat(ctx, a, "reality", "disable"))
	if !reflect.DeepEqual(strict, snapshot()) {
		t.Fatal("strict disable retry requested another reset")
	}
}
