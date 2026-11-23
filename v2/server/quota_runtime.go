package server

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/proof"
)

var errQuota = errors.New("quota unavailable")

// All devices of a user share a single reservation. The read lock pins its
// identity during a write. Neither an in-flight socket write nor a SQLite
// transaction holds the ledger's admission/revocation lock.
type quotaAccount struct {
	ledger        *Ledger
	user          proof.ID
	mu            sync.RWMutex
	id            proof.ID
	reserved      int64
	lastLimit     int64
	limit         atomic.Int64
	left, written atomic.Int64
	refs          atomic.Int64
	retired       atomic.Bool
	settleMu      sync.Mutex
	saved         int64
}

func (a *quotaAccount) setLimit(q sql.NullInt64) {
	limit := int64(-1)
	if q.Valid {
		limit = q.Int64
	}
	a.limit.Store(limit)
}

func (a *quotaAccount) isLast() bool {
	limit := a.limit.Load()
	return a.lastLimit > 0 && limit >= 0 && limit <= a.lastLimit
}

func (a *quotaAccount) settle(ctx context.Context, final bool) error {
	// Caller pins the lease with mu. Only an exclusive holder may finalize.
	a.settleMu.Lock()
	defer a.settleMu.Unlock()
	n := a.written.Load()
	if a.reserved == 0 || !final && a.saved == n {
		return nil
	}
	if err := a.ledger.settleQuota(ctx, a.id, n, final); err != nil {
		return err
	}
	a.saved = n
	if final {
		a.reserved = 0
		a.left.Store(0)
	}
	return nil
}

// Under the exclusive lock no partial write can be outstanding. A datagram
// straddling a lease boundary releases the unused tail before renewing.
func (a *quotaAccount) ensure(ctx context.Context, need int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.retired.Load() {
		return errQuota
	}
	if a.left.Load() >= need {
		return nil
	}
	if a.isLast() && a.left.Load() > 0 {
		return errQuota
	}
	if err := a.settle(ctx, true); err != nil {
		return err
	}
	id, reserved, last, err := a.ledger.reserveQuota(ctx, a.user)
	if err != nil {
		return err
	}
	a.id, a.reserved, a.lastLimit, a.saved = id, reserved, last, 0
	a.written.Store(0)
	a.left.Store(reserved)
	if reserved < need {
		return errQuota
	}
	return nil
}

func (a *quotaAccount) admit(ctx context.Context) error {
	a.mu.RLock()
	ok := !a.retired.Load() && a.left.Load() > 0
	a.mu.RUnlock()
	if ok {
		return nil
	}
	return a.ensure(ctx, 1)
}

// Only the actual writer return value is charged. Admission precedes the
// write, so concurrent writes (including other devices) cannot oversell.
func (a *quotaAccount) write(ctx context.Context, p []byte, packet bool, write func([]byte) (int, error)) (int, error) {
	if a == nil {
		return write(p)
	}
	done := 0
	need := int64(1)
	if packet {
		need = max(1, int64(len(p)))
	}
	for {
		a.mu.RLock()
		if a.retired.Load() {
			a.mu.RUnlock()
			return done, errQuota
		}
		left := a.left.Load()
		if left < need {
			a.mu.RUnlock()
			if err := a.ensure(ctx, need); err != nil {
				return done, err
			}
			continue
		}
		count := min(int64(len(p)), left)
		if !a.left.CompareAndSwap(left, left-count) {
			a.mu.RUnlock()
			continue
		}
		n, err := write(p[:count])
		a.left.Add(count - int64(n))
		full := a.written.Add(int64(n)) == a.reserved
		id, last := a.id, a.isLast()
		if full && last {
			a.retired.Store(true)
		}
		a.mu.RUnlock()
		done += n
		if full {
			if last {
				a.ledger.retireQuota(a)
			}
			a.mu.Lock()
			if a.id == id {
				if e := a.settle(context.Background(), true); e != nil {
					err = errors.Join(err, e)
				}
			}
			a.mu.Unlock()
		}
		if err != nil {
			return done, err
		}
		if int64(n) < count {
			return done, io.ErrShortWrite
		}
		p = p[n:]
		if len(p) == 0 {
			return done, nil
		}
	}
}

type quotaWriter struct {
	ctx context.Context
	a   *quotaAccount
	w   io.Writer
}

func (w quotaWriter) Write(p []byte) (int, error) { return w.a.write(w.ctx, p, false, w.w.Write) }

func (l *Ledger) retireQuota(a *quotaAccount) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.retireQuotaLocked(a)
	l.pruneLocked()
}

func (l *Ledger) retireQuotaLocked(a *quotaAccount) {
	a.retired.Store(true)
	l.retired[a] = true
	if l.meters[a.user] == a {
		delete(l.meters, a.user)
	}
}

func (a *quotaAccount) checkpoint(finalIfIdle bool) error {
	if finalIfIdle && a.refs.Load() == 0 {
		a.mu.Lock()
		defer a.mu.Unlock()
		idle := a.refs.Load() == 0
		err := a.settle(context.Background(), idle)
		if err == nil && idle {
			l := a.ledger
			l.mu.Lock()
			if a.refs.Load() == 0 {
				delete(l.retired, a)
				if l.meters[a.user] == a {
					delete(l.meters, a.user)
				}
			}
			l.mu.Unlock()
		}
		return err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.settle(context.Background(), false)
}

func (l *Ledger) accounts() []*quotaAccount {
	l.mu.Lock()
	defer l.mu.Unlock()
	accounts := make([]*quotaAccount, 0, len(l.meters)+len(l.retired))
	for _, a := range l.meters {
		accounts = append(accounts, a)
	}
	for a := range l.retired {
		accounts = append(accounts, a)
	}
	return accounts
}

func (l *Ledger) checkpoints(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	defer close(l.quotaDone)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, a := range l.accounts() {
				if err := a.checkpoint(true); err != nil {
					slog.Error("quota checkpoint failed", "error", err)
				}
			}
		}
	}
}
