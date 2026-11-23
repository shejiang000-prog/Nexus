package server

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shejiang000-prog/Nexus/v2/proof"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

var errDenied = errors.New("redemption denied")

type Ledger struct {
	db        *sql.DB
	cancel    context.CancelFunc
	done      chan struct{}
	path      string
	file      os.FileInfo
	reader    *sql.Conn
	refreshMu sync.Mutex
	version   int64
	devices   map[proof.ID]deviceState
	mu        sync.Mutex
	active    map[proof.ID]map[*activeFlow]struct{}
}
type deviceState struct {
	key     *ecdsa.PublicKey
	expires sql.NullInt64
}
type activeFlow struct{ stop func() }

func OpenLedger(path string) (*Ledger, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("absolute ledger path required")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	f.Close()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("regular ledger file required")
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("ledger must be private (0600)")
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	db, err := sql.Open("sqlite", uri.String()+"?_txlock=immediate&_pragma=foreign_keys(1)&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	var mode string
	if err = db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil || mode != "wal" {
		db.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("ledger requires WAL")
	}
	if err = initSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &Ledger{path: path, file: info, db: db, cancel: cancel, done: make(chan struct{}), active: make(map[proof.ID]map[*activeFlow]struct{})}
	l.reader, err = db.Conn(ctx)
	if err == nil {
		err = l.refresh(ctx)
	}
	if err != nil {
		cancel()
		if l.reader != nil {
			l.reader.Close()
		}
		db.Close()
		return nil, err
	}
	go l.watchRevocations(ctx)
	return l, nil
}

func initSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	switch version {
	case 0:
		if _, err = tx.Exec(schema); err != nil {
			return err
		}
	case 1:
		if _, err = tx.Exec(`ALTER TABLE audit RENAME TO old_audit;
		CREATE TABLE audit(seq INTEGER PRIMARY KEY,occurred_at INTEGER NOT NULL,action TEXT NOT NULL,device_id BLOB REFERENCES devices(id),user_id BLOB REFERENCES users(id));
		INSERT INTO audit(seq,occurred_at,action,device_id) SELECT seq,occurred_at,action,device_id FROM old_audit;
		DROP TABLE old_audit;
		PRAGMA user_version=2;`); err != nil {
			return err
		}
	case 2:
	default:
		return errors.New("unsupported ledger schema")
	}
	return tx.Commit()
}
func (l *Ledger) Close() error {
	l.cancel()
	<-l.done
	l.disconnectAll()
	l.reader.Close()
	return l.db.Close()
}

// A dedicated reader sees commits from every writer, including this process.
func (l *Ledger) refresh(ctx context.Context) error {
	l.refreshMu.Lock()
	defer l.refreshMu.Unlock()
	info, err := os.Stat(l.path)
	if err != nil {
		return err
	}
	if !os.SameFile(info, l.file) || info.Mode().Perm() != 0600 {
		return errors.New("ledger file identity or permissions changed")
	}
	var version int64
	if err = l.reader.QueryRowContext(ctx, "PRAGMA data_version").Scan(&version); err != nil {
		return err
	}
	if version == l.version && l.devices != nil {
		return nil
	}
	rows, err := l.reader.QueryContext(ctx, `SELECT d.id,public_key,u.expires_at FROM devices d JOIN invites i ON i.digest=d.invite_digest JOIN users u ON u.id=i.user_id WHERE d.revoked_at IS NULL AND u.quota_bytes IS NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()
	devices := make(map[proof.ID]deviceState)
	for rows.Next() {
		var id, pub []byte
		var state deviceState
		if err := rows.Scan(&id, &pub, &state.expires); err != nil {
			return err
		}
		x, y := elliptic.UnmarshalCompressed(elliptic.P256(), pub)
		if len(id) != 16 || x == nil {
			return errors.New("invalid ledger device")
		}
		state.key = &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
		devices[proof.ID(id)] = state
	}
	if err := rows.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	l.devices = devices
	l.pruneLocked()
	l.mu.Unlock()
	l.version = version
	return nil
}
func (l *Ledger) pruneLocked() {
	for id, flows := range l.active {
		if l.keyLocked(id) != nil {
			continue
		}
		delete(l.active, id)
		for flow := range flows {
			go flow.stop()
		}
	}
}

const ledgerFailureLimit = 30 * time.Second

func (l *Ledger) watchRevocations(ctx context.Context) {
	defer close(l.done)
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	var failed time.Time
	var closed bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		q, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		err := l.refresh(q)
		cancel()
		if ctx.Err() != nil {
			return
		}
		delay := 150 * time.Millisecond
		if err != nil {
			if failed.IsZero() {
				failed = time.Now()
				slog.Error("ledger monitor read failed", "error", err)
			}
			delay = 300 * time.Millisecond
			if !closed && time.Since(failed) >= ledgerFailureLimit {
				slog.Error("ledger unavailable for 30s; closing active flows")
				l.refreshMu.Lock()
				l.mu.Lock()
				l.devices = nil
				l.pruneLocked()
				l.mu.Unlock()
				l.refreshMu.Unlock()
				closed = true
			}
		} else {
			if !failed.IsZero() {
				slog.Info("ledger monitor recovered")
			}
			failed = time.Time{}
			closed = false
		}
		l.mu.Lock()
		l.pruneLocked()
		l.mu.Unlock()
		timer.Reset(delay)
	}
}
func (l *Ledger) disconnectAll() {
	l.mu.Lock()
	active := l.active
	l.active = make(map[proof.ID]map[*activeFlow]struct{})
	l.mu.Unlock()
	for _, flows := range active {
		for flow := range flows {
			go flow.stop()
		}
	}
}

func (l *Ledger) CreateUser(ctx context.Context, id proof.ID) error {
	return l.CreateUserUntil(ctx, id, time.Time{})
}
func (l *Ledger) CreateUserUntil(ctx context.Context, id proof.ID, until time.Time) error {
	var expires any
	if !until.IsZero() {
		expires = until.Unix()
	}
	return l.userWrite(ctx, id, "create_user", "INSERT INTO users(id,expires_at) VALUES(?,?)", id[:], expires)
}
func (l *Ledger) userWrite(ctx context.Context, user proof.ID, action, query string, args ...any) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO audit(occurred_at,action,user_id) VALUES(?,?,?)", time.Now().Unix(), action, user[:]); err != nil {
		return err
	}
	return tx.Commit()
}
func (l *Ledger) Invite(ctx context.Context, user proof.ID, until time.Time) ([]byte, error) {
	if until.Unix() <= time.Now().Unix() {
		return nil, errDenied
	}
	token := make([]byte, 32)
	rand.Read(token)
	digest := sha256.Sum256(token)
	err := l.userWrite(ctx, user, "invite", "INSERT INTO invites(digest,user_id,expires_at,remaining) VALUES(?,?,?,1)", digest[:], user[:], until.Unix())
	if err != nil {
		return nil, err
	}
	return token, nil
}

func (l *Ledger) redeem(ctx context.Context, secret, token, public, sig []byte, request proof.ID, name string) (id proof.ID, err error) {
	if len(token) != 32 || len(sig) != 64 || len(name) > 128 || !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return id, errDenied
	}
	digest := sha256.Sum256(token)
	msg, err := proof.RedeemMessage(secret, request, digest, public)
	if err != nil {
		return id, errDenied
	}
	x, y := elliptic.UnmarshalCompressed(elliptic.P256(), public)
	hash := sha256.Sum256(msg)
	if !ecdsa.Verify(&ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return id, errDenied
	}
	var valid int
	now := time.Now().Unix()
	err = l.db.QueryRowContext(ctx, `SELECT 1 FROM invites i JOIN users u ON u.id=i.user_id
	WHERE digest=? AND i.expires_at>? AND u.quota_bytes IS NULL AND (u.expires_at IS NULL OR u.expires_at>?)
	AND (remaining>0 OR EXISTS(SELECT 1 FROM devices WHERE request_id=? AND public_key=? AND invite_digest=i.digest AND revoked_at IS NULL))`, digest[:], now, now, request[:], public).Scan(&valid)
	if err != nil {
		return id, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return id, err
	}
	defer tx.Rollback()
	now = time.Now().Unix()
	var remaining int
	err = tx.QueryRowContext(ctx, `SELECT remaining FROM invites i JOIN users u ON u.id=i.user_id
        WHERE digest=? AND i.expires_at>? AND u.quota_bytes IS NULL AND (u.expires_at IS NULL OR u.expires_at>?)`, digest[:], now, now).Scan(&remaining)
	if err != nil {
		return id, err
	}
	var prior, point, inv []byte
	err = tx.QueryRowContext(ctx, "SELECT id,public_key,invite_digest FROM devices WHERE request_id=? AND revoked_at IS NULL", request[:]).Scan(&prior, &point, &inv)
	if err == nil {
		if len(prior) != 16 || !bytes.Equal(inv, digest[:]) || !bytes.Equal(point, public) {
			return id, errDenied
		}
		if err = tx.Rollback(); err != nil {
			return id, err
		}
		return proof.ID(prior), l.refresh(ctx)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	if remaining == 0 {
		return id, errDenied
	}
	rand.Read(id[:])
	if _, err = tx.ExecContext(ctx, "UPDATE invites SET remaining=remaining-1 WHERE digest=?", digest[:]); err != nil {
		return id, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO devices(id,invite_digest,request_id,public_key,name) VALUES(?,?,?,?,?)", id[:], digest[:], request[:], public, name); err != nil {
		return id, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO audit(occurred_at,action,device_id) VALUES(?,'redeem',?)", now, id[:]); err != nil {
		return id, err
	}
	if err = tx.Commit(); err != nil {
		return id, err
	}
	return id, l.refresh(ctx)
}

func (l *Ledger) Lookup(id proof.ID) *ecdsa.PublicKey {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.keyLocked(id)
}
func (l *Ledger) keyLocked(id proof.ID) *ecdsa.PublicKey {
	state := l.devices[id]
	if state.expires.Valid && state.expires.Int64 <= time.Now().Unix() {
		return nil
	}
	return state.key
}

// register closes the gap between authentication and opening the first business stream.
func (l *Ledger) register(id proof.ID, stop func()) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.keyLocked(id) == nil {
		return nil, false
	}
	flow := &activeFlow{stop}
	if l.active[id] == nil {
		l.active[id] = map[*activeFlow]struct{}{}
	}
	l.active[id][flow] = struct{}{}
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.active[id], flow)
		if len(l.active[id]) == 0 {
			delete(l.active, id)
		}
	}, true
}
func (l *Ledger) Revoke(ctx context.Context, id proof.ID) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	row, err := tx.ExecContext(ctx, "UPDATE devices SET revoked_at=? WHERE id=? AND revoked_at IS NULL", now, id[:])
	if err == nil {
		var n int64
		if n, err = row.RowsAffected(); err == nil && n == 1 {
			_, err = tx.ExecContext(ctx, "INSERT INTO audit(occurred_at,action,device_id) VALUES(?,'revoke',?)", now, id[:])
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		err = l.refresh(ctx)
	}
	return err
}

// List emits one JSON object per record. Columns are explicitly allowlisted.
func (l *Ledger) List(ctx context.Context, kind string, out io.Writer) error {
	var query string
	switch kind {
	case "users":
		query = `SELECT json_object('id',lower(hex(id)),'expires_at',expires_at) FROM users ORDER BY id`
	case "devices":
		query = `SELECT json_object('id',lower(hex(d.id)),'user_id',lower(hex(i.user_id)),'name',d.name,'revoked_at',d.revoked_at) FROM devices d JOIN invites i ON d.invite_digest=i.digest ORDER BY d.id`
	case "audit":
		query = `SELECT json_object('seq',a.seq,'time',a.occurred_at,'action',a.action,'user_id',lower(hex(coalesce(a.user_id,i.user_id))),'device_id',CASE WHEN a.device_id IS NOT NULL THEN lower(hex(a.device_id)) END) FROM audit a LEFT JOIN devices d ON d.id=a.device_id LEFT JOIN invites i ON i.digest=d.invite_digest ORDER BY a.seq`
	default:
		return errors.New("unknown listing")
	}
	rows, err := l.db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var record string
		if err := rows.Scan(&record); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, record); err != nil {
			return err
		}
	}
	return rows.Err()
}
