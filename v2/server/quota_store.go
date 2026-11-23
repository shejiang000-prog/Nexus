package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/proof"
)

// used_bytes holds closed leases only. Live and frozen reservations remain
// separate so reconciliation can return only their uncommitted remainder.
const quotaSchema = `
ALTER TABLE users ADD COLUMN used_bytes INTEGER NOT NULL DEFAULT 0 CHECK(typeof(used_bytes)='integer' AND used_bytes>=0);
CREATE TABLE quota_leases(
	lease_id BLOB PRIMARY KEY NOT NULL CHECK(length(lease_id)=16),
	user_id BLOB NOT NULL REFERENCES users(id),
	reserved_bytes INTEGER NOT NULL CHECK(reserved_bytes>0),
	committed_bytes INTEGER NOT NULL DEFAULT 0 CHECK(committed_bytes BETWEEN 0 AND reserved_bytes),
	state TEXT NOT NULL CHECK(state IN ('active','frozen','closed'))
);
CREATE INDEX outstanding_quota ON quota_leases(user_id) WHERE state IN ('active','frozen');
ALTER TABLE audit ADD COLUMN lease_id BLOB REFERENCES quota_leases(lease_id);
ALTER TABLE audit ADD COLUMN bytes INTEGER;
PRAGMA user_version=3;`

// A second SQLite file supplies a process-lifetime lock without blocking
// ledger writers. A killed process drops the lock in the kernel.
func (l *Ledger) startQuotaNode() error {
	realPath, err := filepath.EvalSymlinks(l.path)
	if err != nil {
		return err
	}
	path := realPath + ".owner"
	info, err := privateLedgerFile(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return errors.New("private node owner file required")
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	owner, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	owner.SetMaxOpenConns(1)
	if _, err := owner.Exec("BEGIN EXCLUSIVE"); err != nil {
		owner.Close()
		return fmt.Errorf("cannot acquire node ledger ownership: %w", err)
	}
	tx, err := l.db.Begin()
	if err == nil {
		defer tx.Rollback()
		_, err = tx.Exec(`INSERT INTO audit(occurred_at,action,user_id,lease_id,bytes)
			SELECT unixepoch(),'freeze',user_id,lease_id,reserved_bytes FROM quota_leases WHERE state='active';
			UPDATE quota_leases SET state='frozen' WHERE state='active'`)
		if err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		owner.Close()
		return err
	}
	l.owner = owner
	return nil
}

func (l *Ledger) stopQuotaNode() error {
	if l.owner == nil {
		return nil
	}
	return l.owner.Close()
}

func (l *Ledger) reserveQuota(ctx context.Context, user proof.ID) (id proof.ID, reserved, lastLimit int64, err error) {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return id, 0, 0, err
	}
	defer tx.Rollback()
	var quota sql.NullInt64
	var used, outstanding int64
	err = tx.QueryRowContext(ctx, `SELECT quota_bytes,used_bytes,coalesce((
		SELECT sum(reserved_bytes) FROM quota_leases WHERE user_id=u.id AND state IN ('active','frozen')),0)
		FROM users u WHERE id=? AND (expires_at IS NULL OR expires_at>unixepoch())`, user[:]).Scan(&quota, &used, &outstanding)
	if err != nil {
		return id, 0, 0, err
	}
	limit := int64(math.MaxInt64)
	if quota.Valid {
		limit = quota.Int64
	}
	if used >= limit || outstanding >= limit-used {
		return id, 0, 0, nil
	}
	remaining := limit - used - outstanding
	reserved = min(remaining, 8<<20)
	rand.Read(id[:])
	if _, err = tx.ExecContext(ctx, "INSERT INTO quota_leases(lease_id,user_id,reserved_bytes,state) VALUES(?,?,?,'active')", id[:], user[:], reserved); err != nil {
		return id, 0, 0, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO audit(occurred_at,action,user_id,lease_id,bytes) VALUES(unixepoch(),'reserve',?,?,?)", user[:], id[:], reserved); err != nil {
		return id, 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return proof.ID{}, 0, 0, err
	}
	if quota.Valid && reserved == remaining {
		lastLimit = quota.Int64
	}
	return id, reserved, lastLimit, nil
}

func (l *Ledger) settleQuota(ctx context.Context, id proof.ID, committed int64, final bool) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var user []byte
	var reserved, prior int64
	var state string
	if err := tx.QueryRowContext(ctx, "SELECT user_id,reserved_bytes,committed_bytes,state FROM quota_leases WHERE lease_id=?", id[:]).Scan(&user, &reserved, &prior, &state); err != nil {
		return err
	}
	if committed < 0 || committed > reserved || state == "frozen" || state == "closed" && committed > prior {
		return errors.New("invalid lease settlement")
	}
	if state == "closed" || !final && committed <= prior {
		return nil
	}
	committed = max(prior, committed)
	action := "settle"
	if final {
		action, state = "close_lease", "closed"
		if _, err := tx.ExecContext(ctx, "UPDATE users SET used_bytes=used_bytes+? WHERE id=?", committed, user); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE quota_leases SET committed_bytes=?,state=? WHERE lease_id=?", committed, state, id[:]); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO audit(occurred_at,action,user_id,lease_id,bytes) VALUES(unixepoch(),?,?,?,?)", action, user, id[:], committed); err != nil {
		return err
	}
	return tx.Commit()
}

func (l *Ledger) Reconcile(ctx context.Context) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO audit(occurred_at,action,user_id,lease_id,bytes)
		SELECT unixepoch(),'reconcile',user_id,lease_id,reserved_bytes-committed_bytes FROM quota_leases WHERE state='frozen';
		UPDATE users SET used_bytes=used_bytes+(SELECT sum(committed_bytes) FROM quota_leases WHERE user_id=users.id AND state='frozen')
		WHERE id IN (SELECT user_id FROM quota_leases WHERE state='frozen');
		UPDATE quota_leases SET state='closed' WHERE state='frozen'`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (l *Ledger) SetQuota(ctx context.Context, user proof.ID, quota *int64) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE users SET quota_bytes=? WHERE id=?", quota, user[:]); err != nil {
		return err
	}
	// The user foreign key also rejects an unknown user.
	if _, err := tx.ExecContext(ctx, "INSERT INTO audit(occurred_at,action,user_id,bytes) VALUES(unixepoch(),'set_quota',?,?)", user[:], quota); err != nil {
		return err
	}
	return tx.Commit()
}

func (l *Ledger) RevokeUser(ctx context.Context, user proof.ID) error {
	return l.userWrite(ctx, user, "revoke_user", `UPDATE devices SET revoked_at=? WHERE revoked_at IS NULL AND invite_digest IN (SELECT digest FROM invites WHERE user_id=?)`, time.Now().Unix(), user[:])
}

// Backup publishes the verified copy atomically. Link refuses an existing
// destination, including one created concurrently during VACUUM.
func (l *Ledger) Backup(ctx context.Context, path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("absolute backup path required")
	}
	if _, err := os.Lstat(path); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ledger-backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := l.db.ExecContext(ctx, "VACUUM INTO ?", tmp.Name()); err != nil {
		return err
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(tmp.Name()), RawQuery: "mode=ro&immutable=1"}
	copyDB, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	defer copyDB.Close()
	var result string
	if err := copyDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("backup integrity check failed")
	}
	return os.Link(tmp.Name(), path)
}
