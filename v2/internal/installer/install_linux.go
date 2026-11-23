package installer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

type transaction []func() error

func (t *transaction) undo(f func() error) { *t = append(*t, f) }
func (t transaction) rollback(err *error) {
	if *err == nil {
		return
	}
	for i := len(t) - 1; i >= 0; i-- {
		if e := t[i](); e != nil {
			*err = errors.Join(*err, fmt.Errorf("rollback: %w", e))
		}
	}
}
func absent(p string) error {
	_, e := os.Lstat(p)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	return fmt.Errorf("existing path is not owned by this transaction: %s", p)
}
func safeDir(path string, uid int) error {
	fi, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !fi.IsDir() || fi.Sys().(*syscall.Stat_t).Uid != uint32(uid) || fi.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("unexpected owner or permissions for %s", path)
	}
	return nil
}
func (h host) readManifest() (deployment, error) {
	var d deployment
	if e := safeDir(h.path(base), 0); e != nil {
		return d, e
	}
	p := h.path(base + "/manifest.json")
	fi, e := os.Lstat(p)
	if e != nil {
		return d, e
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0600 || fi.Sys().(*syscall.Stat_t).Uid != 0 {
		return d, errors.New("unsafe deployment manifest")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return d, e
	}
	if e = json.Unmarshal(b, &d); e != nil {
		return d, e
	}
	token, e := hex.DecodeString(d.Token)
	if e != nil || len(token) != 16 || d.UID <= 0 || d.GID <= 0 {
		return d, errors.New("invalid deployment identity")
	}
	if _, e := normalize(Options{Domain: d.Domain, Ports: d.Ports}); e != nil {
		return d, e
	}
	return d, nil
}
func (h host) writeManifest(d deployment) error {
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(h.path(base), ".manifest-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(append(b, '\n'))
	if e = errors.Join(e, f.Close()); e != nil {
		return e
	}
	return os.Rename(f.Name(), h.path(base+"/manifest.json"))
}
func (h host) accountIDs(ctx context.Context) (int, int, error) {
	get := func(flag string) (int, error) {
		b, e := h.command(ctx, "id", flag, account)
		if e != nil {
			return 0, e
		}
		n, e := strconv.Atoi(strings.TrimSpace(string(b)))
		if e != nil || n <= 0 {
			return 0, errors.New("invalid service identity")
		}
		return n, nil
	}
	u, e := get("-u")
	if e != nil {
		return 0, 0, e
	}
	g, e := get("-g")
	return u, g, e
}
func missingAccount(ctx context.Context, h host, db string) error {
	b, e := h.command(ctx, "getent", db, account)
	if e == nil || len(b) != 0 {
		return fmt.Errorf("existing %s entry %s", db, account)
	}
	var code interface{ ExitCode() int }
	if !errors.As(e, &code) || code.ExitCode() != 2 {
		return e
	}
	return nil
}
func (h host) run(ctx context.Context, action string, o Options, render RenderFunc) error {
	d, e := h.readManifest()
	missing := errors.Is(e, os.ErrNotExist)
	if e != nil && !missing {
		return e
	}
	if action == "install" {
		o, e = normalize(o)
		if e != nil {
			return e
		}
		if !missing && d.Installed {
			if d.Domain != o.Domain || d.RealityTarget != o.RealityTarget || !maps.Equal(d.Ports, o.Ports) {
				return errors.New("existing deployment options differ")
			}
			if e := h.validateOwned(ctx, d); e != nil {
				return e
			}
			return h.healthy(ctx, d)
		}
		return h.install(ctx, o, d, !missing, render)
	}
	if missing {
		return errors.New("no managed deployment")
	}
	switch action {
	case "status":
		if !d.Installed {
			fmt.Fprintln(h.out, "uninstalled; data retained")
			return nil
		}
		var errs error
		for _, p := range protocols {
			if n, ok := d.Ports[p]; ok {
				b, e := h.command(ctx, "systemctl", "is-active", unitName(p))
				fmt.Fprintf(h.out, "%s %d %s\n", p, n, strings.TrimSpace(string(b)))
				errs = errors.Join(errs, e)
			}
		}
		return errs
	case "upgrade":
		if !d.Installed {
			return errors.New("node is uninstalled")
		}
		return h.upgrade(ctx, d, o)
	case "uninstall":
		return h.uninstall(ctx, d, o.Purge)
	default:
		return errors.New("unknown installation action")
	}
}
func (h host) install(ctx context.Context, o Options, old deployment, retained bool, render RenderFunc) (err error) {
	d := deployment{Domain: o.Domain, RealityTarget: o.RealityTarget, Ports: o.Ports, Installed: true}
	b := make([]byte, 16)
	rand.Read(b)
	d.Token = hex.EncodeToString(b)
	if retained {
		d.Token, d.UID, d.GID = old.Token, old.UID, old.GID
	}
	if err = h.preflight(ctx, d, true); err != nil {
		return err
	}
	if retained {
		if err = h.validateIdentity(ctx, d); err != nil {
			return err
		}
	} else {
		for _, p := range []string{base, state} {
			if err = absent(h.path(p)); err != nil {
				return err
			}
		}
		for _, db := range []string{"passwd", "group"} {
			if err = missingAccount(ctx, h, db); err != nil {
				return err
			}
		}
	}
	for _, p := range []string{base + "/bin", cfg} {
		if err = absent(h.path(p)); err != nil {
			return err
		}
	}
	for _, name := range d.units() {
		if err = absent(h.path(filepath.Join(unitDir, name))); err != nil {
			return err
		}
		b, e := h.command(ctx, "systemctl", "show", "--property=LoadState", "--value", name)
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(b)) != "not-found" {
			return fmt.Errorf("existing unit %s", name)
		}
	}
	temp, err := os.MkdirTemp("", "nexus-install-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	stagedBin := filepath.Join(temp, "bin")
	stagedCfg := filepath.Join(temp, "config")
	for _, p := range []string{stagedBin, stagedCfg} {
		if err = os.Mkdir(p, 0700); err != nil {
			return err
		}
	}
	if err = stageBinaries(ctx, o, stagedBin); err != nil {
		return err
	}
	if render == nil {
		return errors.New("configuration renderer required")
	}
	if err = render(ctx, o, stagedCfg); err != nil {
		return err
	}
	cfgFiles, err := os.ReadDir(stagedCfg)
	if err != nil {
		return err
	}
	if len(cfgFiles) == 0 {
		return errors.New("empty installation configuration")
	}
	for _, f := range cfgFiles {
		if !f.Type().IsRegular() {
			return errors.New("unexpected configuration entry")
		}
		d.ConfigFiles = append(d.ConfigFiles, f.Name())
	}
	var tx transaction
	defer func() { tx.rollback(&err) }()
	bg := context.Background()
	if !retained {
		if _, err = h.command(ctx, "useradd", "--system", "--user-group", "--no-create-home", "--home-dir", state, "--shell", "/usr/sbin/nologin", account); err != nil {
			return err
		}
		tx.undo(func() error { _, e := h.command(bg, "userdel", account); return e })
		if d.UID, d.GID, err = h.accountIDs(ctx); err != nil {
			return err
		}
	}
	newDir := func(p string, mode os.FileMode, uid, gid int) error {
		target := h.path(p)
		if e := os.Mkdir(target, mode); e != nil {
			return e
		}
		tx.undo(func() error { return os.RemoveAll(target) })
		if err := os.Chmod(target, mode); err != nil {
			return err
		}
		return os.Chown(target, uid, gid)
	}
	if !retained {
		if err = newDir(base, 0755, 0, 0); err != nil {
			return err
		}
		if err = newDir(state, 0700, 0, 0); err != nil {
			return err
		}
		if err = newDir(state+"/backups", 0700, d.UID, d.GID); err != nil {
			return err
		}
	}
	if !retained {
		if err = os.Chown(h.path(state), d.UID, d.GID); err != nil {
			return err
		}
	}
	if err = newDir(base+"/bin", 0755, 0, 0); err != nil {
		return err
	}
	if err = newDir(cfg, 0700, 0, 0); err != nil {
		return err
	}
	bins, err := os.ReadDir(stagedBin)
	if err != nil {
		return err
	}
	for _, f := range bins {
		if err = copyPath(h.path(base+"/bin/"+f.Name()), filepath.Join(stagedBin, f.Name()), 0755); err != nil {
			return err
		}
	}
	for _, f := range cfgFiles {
		dst := h.path(cfg + "/" + f.Name())
		if err = copyPath(dst, filepath.Join(stagedCfg, f.Name()), 0600); err != nil {
			return err
		}
		if err = os.Chown(dst, d.UID, d.GID); err != nil {
			return err
		}
	}
	if err = os.Chown(h.path(cfg), d.UID, d.GID); err != nil {
		return err
	}
	if _, err = h.command(ctx, "runuser", "-u", account, "--", base+"/bin/nexus-node", "ledger", state+"/ledger.db", "users"); err != nil {
		return err
	}
	if err = h.writeManifest(d); err != nil {
		return err
	}
	if retained {
		tx.undo(func() error { return h.writeManifest(old) })
	}
	tx.undo(func() error { _, e := h.command(bg, "systemctl", "daemon-reload"); return e })
	for _, name := range d.units() {
		dest := h.path(filepath.Join(unitDir, name))
		if err = copyFile(dest, strings.NewReader(unitText(name)), 0644); err != nil {
			return err
		}
		tx.undo(func() error { return os.Remove(dest) })
	}
	if _, err = h.command(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err = h.firewall(ctx, d, true); err != nil {
		return err
	}
	tx.undo(func() error { return h.firewall(bg, d, false) })
	for _, name := range d.units() {
		tx.undo(func() error { _, e := h.command(bg, "systemctl", "disable", "--now", name); return e })
		if _, err = h.command(ctx, "systemctl", "enable", "--now", name); err != nil {
			return err
		}
	}
	if err = h.healthy(ctx, d); err != nil {
		return err
	}
	for _, s := range sockets(d) {
		fmt.Fprintf(h.out, "Allow cloud ingress %d/%s\n", s.port, s.net)
	}
	return nil
}
func (h host) validateIdentity(ctx context.Context, d deployment) error {
	u, g, e := h.accountIDs(ctx)
	if e != nil {
		return e
	}
	if d.UID != u || d.GID != g {
		return errors.New("service account identity changed")
	}
	for _, p := range []string{state, state + "/backups"} {
		if e := safeDir(h.path(p), u); e != nil {
			return e
		}
	}
	return nil
}
func (h host) validateOwned(ctx context.Context, d deployment) error {
	if err := h.validateIdentity(ctx, d); err != nil {
		return err
	}
	if err := safeDir(h.path(cfg), d.UID); err != nil {
		return err
	}
	if err := safeDir(h.path(base+"/bin"), 0); err != nil {
		return err
	}
	for _, name := range d.units() {
		path := h.path(filepath.Join(unitDir, name))
		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() || fi.Sys().(*syscall.Stat_t).Uid != 0 {
			return errors.New("unit ownership changed")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(content, []byte(unitText(name))) {
			return fmt.Errorf("unit was modified: %s", name)
		}
	}
	for _, p := range []string{base + "/bin", cfg} {
		owned := d.ConfigFiles
		if p != cfg {
			owned = []string{"nexus-node"}
			for _, proto := range []string{"reality", "hysteria2"} {
				if _, ok := d.Ports[proto]; ok {
					owned = append(owned, release(proto, runtime.GOARCH).binary)
				}
			}
		}
		entries, e := os.ReadDir(h.path(p))
		if e != nil {
			return e
		}
		if len(entries) != len(owned) {
			return fmt.Errorf("unexpected installation contents: %s", p)
		}
		for _, f := range entries {
			if !f.Type().IsRegular() || !slices.Contains(owned, f.Name()) {
				return fmt.Errorf("unowned file at %s", p)
			}
		}
	}
	return nil
}
