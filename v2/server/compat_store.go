package server

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/compat"
	"github.com/shejiang000-prog/Nexus/v2/proof"
)

const compatSchema = `
CREATE TABLE compat_protocols(protocol TEXT PRIMARY KEY CHECK(protocol IN ('reality','hysteria2')), revision INTEGER NOT NULL DEFAULT 0, applied_revision INTEGER NOT NULL DEFAULT 0);
INSERT INTO compat_protocols(protocol) VALUES('reality'),('hysteria2');
CREATE TABLE compat_credentials(user_id BLOB NOT NULL REFERENCES users(id), protocol TEXT NOT NULL REFERENCES compat_protocols(protocol),
	generation INTEGER NOT NULL, applied_generation INTEGER NOT NULL DEFAULT 0, revision INTEGER NOT NULL, enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
	action TEXT NOT NULL, result TEXT NOT NULL CHECK(result IN ('pending','failed','applied')),
	uuid TEXT NOT NULL,private_key TEXT NOT NULL,public_key TEXT NOT NULL,short_id TEXT NOT NULL,password TEXT NOT NULL,
	PRIMARY KEY(user_id,protocol));
ALTER TABLE audit ADD COLUMN protocol TEXT;
ALTER TABLE audit ADD COLUMN generation INTEGER;
ALTER TABLE audit ADD COLUMN result TEXT;
PRAGMA user_version=5;`

func validProtocol(protocol string) bool { return protocol == "hysteria2" || protocol == "reality" }

func newCredential(protocol string) (c compat.Credential, err error) {
	if protocol == "hysteria2" {
		secret := make([]byte, 32)
		rand.Read(secret)
		c.Password = base64.RawURLEncoding.EncodeToString(secret)
		return c, nil
	}
	id := make([]byte, 16)
	rand.Read(id)
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	c.UUID = fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return c, err
	}
	c.PrivateKey = base64.RawURLEncoding.EncodeToString(key.Bytes())
	c.PublicKey = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	sid := make([]byte, 8)
	rand.Read(sid)
	c.ShortID = hex.EncodeToString(sid)
	return c, nil
}

// A failed rotation retries the same unacknowledged generation. A fresh
// rotation after an acknowledgement creates a new identity.
func changeCompat(ctx context.Context, tx *sql.Tx, user proof.ID, protocol, action string) error {
	if !validProtocol(protocol) {
		return errors.New("unknown compatibility protocol")
	}
	var expiry sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT expires_at FROM users WHERE id=?", user[:]).Scan(&expiry); err != nil {
		return err
	}
	var enabled bool
	var previous, result string
	err := tx.QueryRowContext(ctx, "SELECT enabled,action,result FROM compat_credentials WHERE user_id=? AND protocol=?", user[:], protocol).Scan(&enabled, &previous, &result)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	switch action {
	case "enable", "rotate":
		if expiry.Valid && expiry.Int64 <= time.Now().Unix() {
			return errors.New("user expired")
		}
		if action == "enable" && enabled || action == "rotate" && previous == "rotate" && result != "applied" {
			return nil
		}
		if action == "rotate" && !enabled {
			return errors.New("protocol is disabled")
		}
		enabled = true
	case "disable", "revoke-user":
		if !enabled {
			return nil
		}
		enabled = false
	default:
		return errors.New("unknown credential operation")
	}
	var c compat.Credential
	if enabled {
		c, err = newCredential(protocol)
		if err != nil {
			return err
		}
	}
	var rev, gen int64
	if err := tx.QueryRowContext(ctx, "UPDATE compat_protocols SET revision=revision+1 WHERE protocol=? RETURNING revision", protocol).Scan(&rev); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO compat_credentials(user_id,protocol,revision,generation,enabled,action,result,uuid,private_key,public_key,short_id,password)
		VALUES(?,?,?,1,?,?,'pending',?,?,?,?,?) ON CONFLICT(user_id,protocol) DO UPDATE SET revision=excluded.revision,generation=generation+1,
		enabled=excluded.enabled,action=excluded.action,result='pending',uuid=excluded.uuid,private_key=excluded.private_key,public_key=excluded.public_key,short_id=excluded.short_id,password=excluded.password
		RETURNING generation`, user[:], protocol, rev, enabled, action, c.UUID, c.PrivateKey, c.PublicKey, c.ShortID, c.Password).Scan(&gen); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit(occurred_at,action,user_id,protocol,generation,result) VALUES(unixepoch(),?,?,?,?, 'pending')`, "compat_"+action, user[:], protocol, gen)
	return err
}

func (l *Ledger) ChangeCompat(ctx context.Context, user proof.ID, protocol, action string) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := changeCompat(ctx, tx, user, protocol, action); err != nil {
		return err
	}
	return tx.Commit()
}

func (l *Ledger) compatSnapshot(ctx context.Context, protocol string) (snap compat.Snapshot, applied int64, err error) {
	snap.Protocol = protocol
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return snap, 0, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, "SELECT revision,applied_revision FROM compat_protocols WHERE protocol=?", protocol).Scan(&snap.Revision, &applied); err != nil {
		return snap, 0, err
	}
	// Keep the snapshot for a revision stable as time passes. The service
	// enforces ExpiresAt at admission; export filters against the clock.
	rows, err := tx.QueryContext(ctx, `SELECT lower(hex(user_id)),generation,coalesce(u.expires_at,0),uuid,private_key,public_key,short_id,password FROM compat_credentials c JOIN users u ON u.id=c.user_id
		WHERE protocol=? AND enabled ORDER BY user_id`, protocol)
	if err != nil {
		return snap, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var c compat.Credential
		if err := rows.Scan(&c.UserID, &c.Generation, &c.ExpiresAt, &c.UUID, &c.PrivateKey, &c.PublicKey, &c.ShortID, &c.Password); err != nil {
			return snap, 0, err
		}
		snap.Credentials = append(snap.Credentials, c)
	}
	return snap, applied, rows.Err()
}

func (l *Ledger) recordCompatApply(ctx context.Context, protocol string, rev int64, success, verifyAll bool) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result := "failed"
	if success {
		result = "applied"
	}
	if success || verifyAll {
		ack := rev
		if !success {
			// A failed explicit replay cannot confirm the saved configuration.
			ack = -1
		}
		if _, err := tx.ExecContext(ctx, "UPDATE compat_protocols SET applied_revision=? WHERE protocol=?", ack, protocol); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit(occurred_at,action,user_id,protocol,generation,result)
		SELECT unixepoch(),'compat_apply',user_id,protocol,generation,? FROM compat_credentials WHERE protocol=? AND revision<=? AND (result!='applied' OR ?) AND result!=?`, result, protocol, rev, verifyAll, result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compat_credentials SET result=?,applied_generation=CASE WHEN ? THEN generation ELSE applied_generation END
		WHERE protocol=? AND revision<=? AND (result!='applied' OR ?)`, result, success, protocol, rev, verifyAll); err != nil {
		return err
	}
	return tx.Commit()
}

// An exclusive transaction on a separate per-protocol file serializes external
// service updates across CLI processes, without holding the ledger's writer
// lock during a restart. Process death releases the lock.
func (l *Ledger) ApplyCompat(ctx context.Context, protocol string, force bool, apply func(compat.Snapshot) error) error {
	if !validProtocol(protocol) {
		return errors.New("unknown compatibility protocol")
	}
	realPath, err := filepath.EvalSymlinks(l.path)
	if err != nil {
		return err
	}
	path := realPath + ".apply-" + protocol
	info, err := privateLedgerFile(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return errors.New("private compatibility lock required")
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "_txlock=exclusive&_pragma=busy_timeout(8000)"}
	lock, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	defer lock.Close()
	held, err := lock.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer held.Rollback()
	snap, applied, err := l.compatSnapshot(ctx, protocol)
	if err != nil {
		return err
	}
	if !force && snap.Revision == applied {
		return nil
	}
	applyErr := apply(snap)
	if err := l.recordCompatApply(ctx, protocol, snap.Revision, applyErr == nil, force); err != nil {
		return err
	}
	if applyErr != nil {
		return errors.New("service application failed; retry compat apply")
	}
	return nil
}

type CompatStatus struct {
	Protocol          string `json:"protocol"`
	Enabled           bool   `json:"enabled"`
	Generation        int64  `json:"generation"`
	AppliedGeneration int64  `json:"applied_generation"`
	Result            string `json:"result"`
}

func (l *Ledger) CompatStatus(ctx context.Context, user proof.ID) ([]CompatStatus, error) {
	var exists bool
	if err := l.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=?)", user[:]).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.New("user not found")
	}
	rows, err := l.db.QueryContext(ctx, "SELECT protocol,enabled,generation,applied_generation,result FROM compat_credentials WHERE user_id=? ORDER BY protocol", user[:])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	status := []CompatStatus{}
	for rows.Next() {
		var item CompatStatus
		if err := rows.Scan(&item.Protocol, &item.Enabled, &item.Generation, &item.AppliedGeneration, &item.Result); err != nil {
			return nil, err
		}
		status = append(status, item)
	}
	return status, rows.Err()
}

// Export reads only public routing data and the client credential. The
// Reality server private key is never selected for client delivery.
func (l *Ledger) CompatExport(ctx context.Context, user proof.ID) (map[string]compat.Credential, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT protocol,uuid,public_key,short_id,password FROM compat_credentials c JOIN users u ON c.user_id=u.id
		WHERE user_id=? AND enabled AND result='applied' AND generation=applied_generation AND (u.expires_at IS NULL OR u.expires_at>unixepoch())`, user[:])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]compat.Credential{}
	for rows.Next() {
		var protocol string
		var c compat.Credential
		c.UserID = hex.EncodeToString(user[:])
		if err := rows.Scan(&protocol, &c.UUID, &c.PublicKey, &c.ShortID, &c.Password); err != nil {
			return nil, err
		}
		out[protocol] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("user has no applied, active credentials")
	}
	return out, nil
}
