package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (h host) asService(ctx context.Context, cmd string, args ...string) error {
	_, err := h.command(ctx, "runuser", append([]string{"-u", account, "--", cmd}, args...)...)
	return err
}
func (h host) system(ctx context.Context, args ...string) error {
	_, err := h.command(ctx, "systemctl", args...)
	return err
}
func (h host) start(ctx context.Context, d deployment) error {
	for _, name := range d.units() {
		if e := h.system(ctx, "start", name); e != nil {
			return e
		}
	}
	return h.healthy(ctx, d)
}
func (h host) stop(ctx context.Context, d deployment) error {
	names := d.units()
	for i := len(names) - 1; i >= 0; i-- {
		if e := h.system(ctx, "stop", names[i]); e != nil {
			return e
		}
	}
	return nil
}
func (h host) backup(ctx context.Context, dest string) error {
	return h.asService(ctx, base+"/bin/nexus-node", "ledger", state+"/ledger.db", "backup", dest)
}

var ledgerFiles = []string{"ledger.db", "ledger.db-wal", "ledger.db-shm", "compat-reality", "compat-hysteria2"}

// All accesses to persisted service data run under its unprivileged account.
func (h host) transferState(ctx context.Context, from, to string, names []string, move bool) error {
	for _, n := range names {
		src := filepath.Join(from, n)
		if _, e := os.Lstat(h.path(src)); errors.Is(e, os.ErrNotExist) {
			continue
		} else if e != nil {
			return e
		}
		var err error
		if move {
			err = h.asService(ctx, "/usr/bin/mv", "--", src, to+"/")
		} else {
			err = h.asService(ctx, "/usr/bin/cp", "-a", "--", src, to+"/")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (h host) protectRetained(ctx context.Context, tx *transaction) error {
	if e := h.requireClosedDB(); e != nil {
		return e
	}
	dir := state + "/backups/reinstall-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if e := h.asService(ctx, "/usr/bin/mkdir", "-m", "0700", "--", dir); e != nil {
		return e
	}
	if e := h.transferState(ctx, state, dir, ledgerFiles, false); e != nil {
		return e
	}
	tx.undo(func() error {
		bg := context.Background()
		if e := h.requireClosedDB(); e != nil {
			return e
		}
		if e := h.asService(bg, "/usr/bin/mkdir", "-m", "0700", "--", dir+"/failed"); e != nil {
			return e
		}
		if e := h.transferState(bg, state, dir+"/failed", ledgerFiles, true); e != nil {
			return e
		}
		return h.transferState(bg, dir, state, ledgerFiles, false)
	})
	return nil
}
func (h host) upgrade(ctx context.Context, d deployment, o Options) (err error) {
	if err = h.validateOwned(ctx, d); err != nil {
		return err
	}
	if err = h.preflight(ctx, d, false); err != nil {
		return err
	}
	backupDir := state + "/backups/upgrade-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err = h.asService(ctx, "/usr/bin/mkdir", "-m", "0700", "--", backupDir); err != nil {
		return err
	}
	if err = h.backup(ctx, backupDir+"/online.db"); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(h.path(base), ".upgrade-*")
	if err != nil {
		return err
	}
	clean := true
	defer func() {
		if clean {
			os.RemoveAll(tmp)
		}
	}()
	newBin := filepath.Join(tmp, "new")
	if err = os.Mkdir(newBin, 0755); err != nil {
		return err
	}
	if err = os.Chmod(newBin, 0755); err != nil {
		return err
	}
	o.Ports = d.Ports
	if err = stageBinaries(ctx, o, newBin); err != nil {
		return err
	}
	bg := context.Background()
	if err = h.stop(ctx, d); err != nil {
		return errors.Join(err, h.start(bg, d))
	}
	binDir := h.path(base + "/bin")
	oldBin := filepath.Join(tmp, "previous")
	// Snapshot again after draining the old services: successful writes during
	// download or shutdown belong to the rollback version.
	if err = h.backup(ctx, backupDir+"/before-switch.db"); err != nil {
		return errors.Join(err, h.start(bg, d))
	}
	if e := h.transferState(ctx, state, backupDir, ledgerFiles[3:], false); e != nil {
		return errors.Join(e, h.start(bg, d))
	}
	if err = os.Rename(binDir, oldBin); err != nil {
		return errors.Join(err, h.start(bg, d))
	}
	if err = os.Rename(newBin, binDir); err != nil {
		restore := os.Rename(oldBin, binDir)
		if restore != nil {
			clean = false
		}
		return errors.Join(err, restore, h.start(bg, d))
	}
	if err = h.start(ctx, d); err == nil {
		fmt.Fprintf(h.out, "Upgrade backup: %s\n", backupDir)
		return nil
	}
	failure := err
	// Keep the failed trial database (including its WAL) and credentials for
	// recovery of writes received while the trial was running.
	rollback := func() error {
		if e := h.stop(bg, d); e != nil {
			return e
		}
		// An external admin may still have SQLite open. Never replace that
		// inode out from under another process.
		if e := h.requireClosedDB(); e != nil {
			return e
		}
		failed := backupDir + "/failed"
		if e := h.asService(bg, "/usr/bin/mkdir", "-m", "0700", "--", failed); e != nil {
			return e
		}
		if e := h.transferState(bg, state, failed, ledgerFiles, true); e != nil {
			return e
		}
		if e := h.asService(bg, "/usr/bin/cp", "--preserve=mode", "--", backupDir+"/before-switch.db", state+"/ledger.db"); e != nil {
			return e
		}
		if e := h.transferState(bg, backupDir, state, ledgerFiles[3:], false); e != nil {
			return e
		}
		if e := os.Rename(binDir, newBin); e != nil {
			return e
		}
		if e := os.Rename(oldBin, binDir); e != nil {
			return e
		}
		return h.start(bg, d)
	}()
	if rollback != nil {
		clean = false
		return errors.Join(failure, fmt.Errorf("rollback incomplete; binaries in %s, state in %s: %w", tmp, backupDir, rollback))
	}
	return fmt.Errorf("upgrade failed; previous version restored, diagnostics in %s: %w", backupDir, failure)
}
func (h host) requireClosedDB() error {
	fi, err := os.Stat(h.path(state + "/ledger.db"))
	if err != nil {
		return err
	}
	fds, err := filepath.Glob(h.path("/proc/[0-9]*/fd/*"))
	if err != nil {
		return err
	}
	for _, fd := range fds {
		other, e := os.Stat(fd)
		if e == nil && os.SameFile(fi, other) {
			return errors.New("ledger is still open by another process")
		}
	}
	return nil
}
func (h host) uninstall(ctx context.Context, d deployment, purge bool) (err error) {
	if !d.Installed && !purge {
		return nil
	}
	if err = h.validateIdentity(ctx, d); err != nil {
		return err
	}
	var tx transaction
	var trash []string
	defer func() {
		tx.rollback(&err)
		if err == nil {
			for _, p := range trash {
				err = errors.Join(err, os.RemoveAll(p))
			}
			if purge && err == nil {
				files, e := os.ReadDir(h.path(base))
				if e != nil {
					return
				}
				if len(files) == 0 {
					err = os.Remove(h.path(base))
				}
			}
		}
	}()
	move := func(path string) error {
		p := h.path(path)
		temp, e := os.MkdirTemp(filepath.Dir(p), ".nexus-remove-*")
		if e != nil {
			return e
		}
		dst := filepath.Join(temp, "owned")
		if e = os.Rename(p, dst); e != nil {
			os.Remove(temp)
			return e
		}
		trash = append(trash, temp)
		tx.undo(func() error {
			if e := os.Rename(dst, p); e != nil {
				return e
			}
			return os.Remove(temp)
		})
		return nil
	}
	bg := context.Background()
	if d.Installed {
		if err = h.validateOwned(ctx, d); err != nil {
			return err
		}
		tx.undo(func() error {
			for _, name := range d.units() {
				if e := h.system(bg, "enable", "--now", name); e != nil {
					return e
				}
			}
			return nil
		})
		names := d.units()
		for i := len(names) - 1; i >= 0; i-- {
			if err = h.system(ctx, "disable", "--now", names[i]); err != nil {
				return err
			}
		}
		if err = h.firewall(ctx, d, false); err != nil {
			return err
		}
		tx.undo(func() error { return h.firewall(bg, d, true) })
		tx.undo(func() error { return h.system(bg, "daemon-reload") })
		for _, n := range names {
			if err = move(filepath.Join(unitDir, n)); err != nil {
				return err
			}
		}
		if err = h.system(ctx, "daemon-reload"); err != nil {
			return err
		}
		if err = move(cfg); err != nil {
			return err
		}
		if err = move(base + "/bin"); err != nil {
			return err
		}
	}
	if purge {
		if err = h.requireClosedDB(); err != nil {
			return err
		}
		if err = move(state); err != nil {
			return err
		}
		// Move the manifest by itself: a previous failed upgrade may have
		// left administrator recovery material under the binary directory.
		if err = move(base + "/manifest.json"); err != nil {
			return err
		}
		if _, err = h.command(ctx, "userdel", account); err != nil {
			return err
		}

	} else {
		d.Installed = false
		if err = h.writeManifest(d); err != nil {
			return err
		}
	}
	return nil
}
