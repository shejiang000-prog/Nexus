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
	db     *sql.DB
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	active map[proof.ID]map[*activeFlow]struct{}
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
	db.SetMaxOpenConns(1)
	if err = initSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	var seq int64
	if err = db.QueryRow("SELECT COALESCE(MAX(seq),0) FROM audit").Scan(&seq); err != nil {
		db.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &Ledger{db: db, cancel: cancel, done: make(chan struct{}), active: make(map[proof.ID]map[*activeFlow]struct{})}
	go l.watchRevocations(ctx, seq)
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
	default:
		return errors.New("unsupported ledger schema")
	}
	return tx.Commit()
}
func (l *Ledger) Close() error { l.cancel(); <-l.done; l.disconnectAll(); return l.db.Close() }

// A Caddy reload or a local management process can open a second handle to the same file.
func (l *Ledger) watchRevocations(ctx context.Context, seq int64) {
	defer close(l.done)
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	var backlog bool
	for ctx.Err() == nil {
		if !backlog {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
		backlog = false
		l.mu.Lock()
		idle := len(l.active) == 0
		l.mu.Unlock()
		if idle {
			continue
		}
		q, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		rows, err := l.db.QueryContext(q, "SELECT seq,device_id FROM audit WHERE action='revoke' AND seq>? ORDER BY seq LIMIT 256", seq)
		var revoked []proof.ID
		if err == nil {
			for rows.Next() {
				var id []byte
				if err = rows.Scan(&seq, &id); err != nil {
					break
				}
				if len(id) != 16 {
					err = errors.New("invalid revocation audit row")
					break
				}
				revoked = append(revoked, proof.ID(id))
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
		}
		cancel()
		if err != nil {
			l.disconnectAll()
			continue
		}
		backlog = len(revoked) == 256
		for _, id := range revoked {
			l.disconnect(id)
		}
	}
}
func (l *Ledger) disconnect(id proof.ID) {
	l.mu.Lock()
	flows := l.active[id]
	delete(l.active, id)
	l.mu.Unlock()
	for flow := range flows {
		go flow.stop()
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
	_, err := l.db.ExecContext(ctx, "INSERT INTO users(id) VALUES(?)", id[:])
	return err
}
func (l *Ledger) Invite(ctx context.Context, user proof.ID, until time.Time) ([]byte, error) {
	if until.Unix() <= time.Now().Unix() {
		return nil, errDenied
	}
	token := make([]byte, 32)
	rand.Read(token)
	digest := sha256.Sum256(token)
	_, err := l.db.ExecContext(ctx, "INSERT INTO invites(digest,user_id,expires_at,remaining) VALUES(?,?,?,1)", digest[:], user[:], until.Unix())
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
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return id, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
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
		return proof.ID(prior), nil
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
	return id, tx.Commit()
}

func (l *Ledger) Lookup(id proof.ID) *ecdsa.PublicKey {
	var pub []byte
	err := l.db.QueryRow(`SELECT public_key FROM devices d JOIN invites i ON d.invite_digest=i.digest JOIN users u ON i.user_id=u.id
        WHERE d.id=? AND d.revoked_at IS NULL AND u.quota_bytes IS NULL AND (u.expires_at IS NULL OR u.expires_at>?)`, id[:], time.Now().Unix()).Scan(&pub)
	if err != nil {
		return nil
	}
	x, y := elliptic.UnmarshalCompressed(elliptic.P256(), pub)
	if x == nil {
		return nil
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
}

// register closes the gap between authentication and opening the first business stream.
func (l *Ledger) register(id proof.ID, stop func()) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Lookup(id) == nil {
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
		l.disconnect(id)
	}
	return err
}
