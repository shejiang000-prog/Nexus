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
INSERT INTO compat_protocols(protocol,applied_revision) VALUES('reality',-1),('hysteria2',0);
CREATE TABLE compat_reality_key (
	id INTEGER PRIMARY KEY CHECK(id=1), generation INTEGER NOT NULL, applied_generation INTEGER NOT NULL DEFAULT 0,
	revision INTEGER NOT NULL, result TEXT NOT NULL CHECK(result IN ('pending','failed','applied')),
	private_key TEXT NOT NULL, public_key TEXT NOT NULL, short_id TEXT NOT NULL, reset_revision INTEGER NOT NULL DEFAULT 0);
CREATE TABLE compat_credentials(user_id BLOB NOT NULL REFERENCES users(id), protocol TEXT NOT NULL REFERENCES compat_protocols(protocol),
	generation INTEGER NOT NULL, applied_generation INTEGER NOT NULL DEFAULT 0, revision INTEGER NOT NULL, enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
	action TEXT NOT NULL, result TEXT NOT NULL CHECK(result IN ('pending','failed','applied')),
	uuid TEXT NOT NULL,password TEXT NOT NULL,
	PRIMARY KEY(user_id,protocol));
CREATE UNIQUE INDEX compat_active_reality_uuid ON compat_credentials(uuid) WHERE protocol='reality' AND enabled;
ALTER TABLE audit ADD COLUMN protocol TEXT;
ALTER TABLE audit ADD COLUMN generation INTEGER;
ALTER TABLE audit ADD COLUMN result TEXT;
PRAGMA user_version=5;`

func validProtocol(protocol string) bool { return protocol == "hysteria2" || protocol == "reality" }

func newCredential(protocol string) (c compat.Credential) {
	if protocol == "hysteria2" {
		secret := make([]byte, 32)
		rand.Read(secret)
		c.Password = base64.RawURLEncoding.EncodeToString(secret)
		return c
	}
	id := make([]byte, 16)
	rand.Read(id)
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	c.UUID = fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	return c
}

func newRealityKey() (c compat.RealityKey, err error) {
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

func initCompatSchema(tx *sql.Tx) error {
	if _, err := tx.Exec(compatSchema); err != nil {
		return err
	}
	key, err := newRealityKey()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO compat_reality_key(id,generation,revision,result,private_key,public_key,short_id)
		VALUES(1,1,0,'pending',?,?,?)`, key.PrivateKey, key.PublicKey, key.ShortID)
	if err == nil {
		_, err = tx.Exec(`INSERT INTO audit(occurred_at,action,protocol,generation,result)
			VALUES(unixepoch(),'compat_init_node_key','reality',1,'pending')`)
	}
	return err
}

// RotateRealityKey changes the node identity for every Reality user. Repeated
// requests for a pending rotation reuse the same key until it is acknowledged.
func (l *Ledger) RotateRealityKey(ctx context.Context) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var gen int64
	var result string
	if err := tx.QueryRowContext(ctx, "SELECT generation,result FROM compat_reality_key WHERE id=1").Scan(&gen, &result); err != nil {
		return err
	}
	if gen > 1 && result != "applied" {
		return nil
	}
	key, err := newRealityKey()
	if err != nil {
		return err
	}
	var rev int64
	if err := tx.QueryRowContext(ctx, "UPDATE compat_protocols SET revision=revision+1 WHERE protocol='reality' RETURNING revision").Scan(&rev); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compat_reality_key SET generation=generation+1,revision=?,reset_revision=?,result='pending',private_key=?,public_key=?,short_id=? WHERE id=1`,
		rev, rev, key.PrivateKey, key.PublicKey, key.ShortID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit(occurred_at,action,protocol,generation,result)
		VALUES(unixepoch(),'compat_rotate_node_key','reality',?,'pending')`, gen+1); err != nil {
		return err
	}
	return tx.Commit()
}

// A failed rotation retries the same unacknowledged generation. A fresh
// rotation after an acknowledgement creates a new identity.
func changeCompat(ctx context.Context, tx *sql.Tx, user proof.ID, protocol, action string, keepConnections bool) error {
	if !validProtocol(protocol) {
		return errors.New("unknown compatibility protocol")
	}
	var expiry sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT expires_at FROM users WHERE id=?", user[:]).Scan(&expiry); err != nil {
		return err
	}
	var enabled bool
	var previousRevision int64
	var previous, result string
	err := tx.QueryRowContext(ctx, "SELECT enabled,action,result,revision FROM compat_credentials WHERE user_id=? AND protocol=?", user[:], protocol).Scan(&enabled, &previous, &result, &previousRevision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	reset := protocol == "reality" && action != "enable" && !keepConnections
	var repeat bool
	switch action {
	case "enable", "rotate":
		if expiry.Valid && expiry.Int64 <= time.Now().Unix() {
			return errors.New("user expired")
		}
		if action == "enable" && enabled {
			return nil
		}
		if action == "rotate" && !enabled {
			return errors.New("protocol is disabled")
		}
		repeat = action == "rotate" && previous == "rotate" && result != "applied"
		enabled = true
	case "disable", "revoke-user":
		repeat = !enabled
		enabled = false
	default:
		return errors.New("unknown credential operation")
	}
	if repeat {
		if !reset || previousRevision == 0 {
			return nil
		}
		var covered int64
		if err := tx.QueryRowContext(ctx, "SELECT reset_revision FROM compat_reality_key WHERE id=1").Scan(&covered); err != nil || covered >= previousRevision {
			return err
		}
	}
	var rev, gen int64
	if err := tx.QueryRowContext(ctx, "UPDATE compat_protocols SET revision=revision+1 WHERE protocol=? RETURNING revision", protocol).Scan(&rev); err != nil {
		return err
	}
	if reset {
		if _, err := tx.ExecContext(ctx, "UPDATE compat_reality_key SET reset_revision=? WHERE id=1", rev); err != nil {
			return err
		}
	}
	// Upgrade an outstanding keep-connections operation to a reset while
	// preserving its credential generation and UUID.
	if repeat {
		if err := tx.QueryRowContext(ctx, `UPDATE compat_credentials SET revision=?,result='pending' WHERE user_id=? AND protocol=? RETURNING generation`, rev, user[:], protocol).Scan(&gen); err != nil {
			return err
		}
	} else {
		var c compat.Credential
		if enabled {
			c = newCredential(protocol)
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO compat_credentials(user_id,protocol,revision,generation,enabled,action,result,uuid,password)
		VALUES(?,?,?,1,?,?,'pending',?,?) ON CONFLICT(user_id,protocol) DO UPDATE SET revision=excluded.revision,generation=generation+1,
		enabled=excluded.enabled,action=excluded.action,result='pending',uuid=excluded.uuid,password=excluded.password
		RETURNING generation`, user[:], protocol, rev, enabled, action, c.UUID, c.Password).Scan(&gen); err != nil {
			return err
		}
	}
	if protocol == "reality" && keepConnections {
		action += "_keep_connections"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit(occurred_at,action,user_id,protocol,generation,result) VALUES(unixepoch(),?,?,?,?, 'pending')`, "compat_"+action, user[:], protocol, gen)
	return err
}

func (l *Ledger) ChangeCompat(ctx context.Context, user proof.ID, protocol, action string) error {
	return l.ChangeCompatWithPolicy(ctx, user, protocol, action, false)
}

func (l *Ledger) ChangeCompatWithPolicy(ctx context.Context, user proof.ID, protocol, action string, keepConnections bool) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := changeCompat(ctx, tx, user, protocol, action, keepConnections); err != nil {
		return err
	}
	return tx.Commit()
}

// CompatSnapshot reads the authoritative desired state before a compatibility
// service opens its public listener. It does not acknowledge an application.
func (l *Ledger) CompatSnapshot(ctx context.Context, protocol string) (compat.Snapshot, error) {
	snap, _, err := l.compatSnapshot(ctx, protocol)
	return snap, err
}

func (l *Ledger) compatSnapshot(ctx context.Context, protocol string) (snap compat.Snapshot, applied int64, err error) {
	snap.Protocol = protocol
	tx, err := l.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return snap, 0, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, "SELECT revision,applied_revision FROM compat_protocols WHERE protocol=?", protocol).Scan(&snap.Revision, &applied); err != nil {
		return snap, 0, err
	}
	if protocol == "reality" {
		var key compat.RealityKey
		if err := tx.QueryRowContext(ctx, "SELECT generation,private_key,public_key,short_id,reset_revision FROM compat_reality_key WHERE id=1").Scan(&key.Generation, &key.PrivateKey, &key.PublicKey, &key.ShortID, &snap.ResetRevision); err != nil {
			return snap, 0, err
		}
		snap.RealityKey = &key
	}
	// Keep the snapshot for a revision stable as time passes. The service
	// enforces ExpiresAt at admission; export filters against the clock.
	rows, err := tx.QueryContext(ctx, `SELECT lower(hex(user_id)),generation,coalesce(u.expires_at,0),uuid,password FROM compat_credentials c JOIN users u ON u.id=c.user_id
		WHERE protocol=? AND enabled ORDER BY user_id`, protocol)
	if err != nil {
		return snap, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var c compat.Credential
		if err := rows.Scan(&c.UserID, &c.Generation, &c.ExpiresAt, &c.UUID, &c.Password); err != nil {
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
	if protocol == "reality" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit(occurred_at,action,protocol,generation,result)
			SELECT unixepoch(),'compat_apply_node_key','reality',generation,? FROM compat_reality_key
			WHERE revision<=? AND (result!='applied' OR ?) AND result!=?`, result, rev, verifyAll, result); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE compat_reality_key SET result=?,applied_generation=CASE WHEN ? THEN generation ELSE applied_generation END
			WHERE revision<=? AND (result!='applied' OR ?)`, result, success, rev, verifyAll); err != nil {
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
	Protocol                 string `json:"protocol"`
	Enabled                  bool   `json:"enabled"`
	Generation               int64  `json:"generation"`
	AppliedGeneration        int64  `json:"applied_generation"`
	Result                   string `json:"result"`
	NodeKeyGeneration        int64  `json:"node_key_generation,omitempty"`
	NodeKeyAppliedGeneration int64  `json:"node_key_applied_generation,omitempty"`
	NodeKeyResult            string `json:"node_key_result,omitempty"`
}

func (l *Ledger) CompatStatus(ctx context.Context, user proof.ID) ([]CompatStatus, error) {
	var exists bool
	if err := l.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=?)", user[:]).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.New("user not found")
	}
	rows, err := l.db.QueryContext(ctx, `SELECT c.protocol,c.enabled,c.generation,c.applied_generation,c.result,coalesce(k.generation,0),coalesce(k.applied_generation,0),coalesce(k.result,'')
		FROM compat_credentials c LEFT JOIN compat_reality_key k ON c.protocol='reality' AND k.id=1 WHERE c.user_id=? ORDER BY c.protocol`, user[:])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	status := []CompatStatus{}
	for rows.Next() {
		var item CompatStatus
		if err := rows.Scan(&item.Protocol, &item.Enabled, &item.Generation, &item.AppliedGeneration, &item.Result, &item.NodeKeyGeneration, &item.NodeKeyAppliedGeneration, &item.NodeKeyResult); err != nil {
			return nil, err
		}
		status = append(status, item)
	}
	return status, rows.Err()
}

type CompatShare struct {
	UserID, UUID, PublicKey, ShortID, Password string
}

// Export reads only public routing data and the client credential. The
// Reality server private key is never selected for client delivery.
func (l *Ledger) CompatExport(ctx context.Context, user proof.ID) (map[string]CompatShare, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT c.protocol,c.uuid,coalesce(k.public_key,''),coalesce(k.short_id,''),c.password
		FROM compat_credentials c JOIN users u ON c.user_id=u.id
		LEFT JOIN compat_reality_key k ON c.protocol='reality' AND k.id=1 AND k.result='applied' AND k.generation=k.applied_generation
		WHERE c.user_id=? AND c.enabled AND c.result='applied' AND c.generation=c.applied_generation
		AND (c.protocol!='reality' OR k.id IS NOT NULL) AND (u.expires_at IS NULL OR u.expires_at>unixepoch())`, user[:])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]CompatShare{}
	for rows.Next() {
		var protocol string
		var c CompatShare
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
