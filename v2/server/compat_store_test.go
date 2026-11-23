package server

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
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
	if first.UUID == second.UUID || first.PrivateKey == second.PrivateKey || first.PublicKey == second.PublicKey || first.ShortID == second.ShortID {
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
	if len(exported) != 2 || exported["reality"].PrivateKey != "" || exported["reality"].PublicKey != second.PublicKey {
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
	for _, secret := range []string{first.UUID, second.PrivateKey, second.ShortID, exported["hysteria2"].Password} {
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
