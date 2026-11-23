package server

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/caddyserver/caddy/v2"
)

var nodeLedgers = caddy.NewUsagePool()

type nodeLedger struct{ *Ledger }

func (l nodeLedger) Destruct() error { return l.Close() }

// Only configured handlers own references. Requests from the old
// configuration can continue while the new configuration owns the same DB.
func acquireNodeLedger(path string) (*Ledger, func() error, error) {
	if !filepath.IsAbs(path) {
		return nil, nil, errors.New("absolute ledger path required")
	}
	realPath, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		var parent string
		parent, err = filepath.EvalSymlinks(filepath.Dir(path))
		realPath = filepath.Join(parent, filepath.Base(path))
	}
	if err != nil {
		return nil, nil, err
	}
	value, loaded, err := nodeLedgers.LoadOrNew(realPath, func() (caddy.Destructor, error) {
		l, err := OpenLedger(realPath)
		if err != nil {
			return nil, err
		}
		return nodeLedger{l}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	l := value.(nodeLedger).Ledger
	release := sync.OnceValue(func() error { _, err := nodeLedgers.Delete(realPath); return err })
	if loaded {
		info, err := os.Stat(realPath)
		if err != nil || !os.SameFile(info, l.file) || info.Mode().Perm() != 0600 {
			release()
			return nil, nil, errors.New("shared ledger file identity or permissions changed")
		}
	}
	return l, release, nil
}
