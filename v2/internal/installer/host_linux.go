package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func execute(ctx context.Context, name string, args ...string) ([]byte, error) {
	if strings.Contains(name, "/") {
		return nil, errors.New("unexpected helper path")
	}
	var cmdPath string
	for _, dir := range []string{"/usr/bin", "/usr/sbin"} {
		p := filepath.Join(dir, name)
		if fi, e := os.Stat(p); e == nil && fi.Mode().IsRegular() {
			cmdPath = p
			break
		}
	}
	if cmdPath == "" {
		return nil, fmt.Errorf("required system tool %s missing", name)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cmdPath, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "HOME=/root"}
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w", name, args[0], err)
	}
	return out, nil
}
func Run(ctx context.Context, action string, o Options, render RenderFunc, out io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("root privileges required")
	}
	h := host{root: "/", command: execute, out: out}
	// The boot oneshot can be invoked by systemd while install owns this lock.
	if action == "firewall-apply" {
		d, e := h.readManifest()
		if e != nil {
			return e
		}
		if !d.Installed {
			return errors.New("node is uninstalled")
		}
		return h.firewall(ctx, d, true)
	}
	lock, e := os.Open("/opt")
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return errors.New("another installation transaction is active")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return h.run(ctx, action, o, render)
}
func (h host) preflight(ctx context.Context, d deployment, checkPorts bool) error {
	b, e := os.ReadFile(h.path("/etc/os-release"))
	if e != nil {
		return e
	}
	release := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(line, "=")
		release[k] = strings.Trim(v, "\"'")
	}
	id, v := release["ID"], release["VERSION_ID"]
	if !(id == "debian" && (v == "12" || v == "13") || id == "ubuntu" && (v == "22.04" || v == "24.04")) {
		return fmt.Errorf("unsupported system %s %s", id, v)
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return errors.New("unsupported architecture")
	}
	if _, e = h.command(ctx, "systemctl", "show", "--property=Version", "--value"); e != nil {
		return e
	}
	timeState, e := h.command(ctx, "timedatectl", "show", "--property=NTPSynchronized", "--value")
	if e != nil || strings.TrimSpace(string(timeState)) != "yes" {
		return errors.New("clock is not synchronized")
	}
	for _, p := range []string{"/etc", "/opt", "/var/lib"} {
		var s syscall.Statfs_t
		if e := syscall.Statfs(h.path(p), &s); e != nil {
			return e
		}
		if uint64(s.Bavail)*uint64(s.Bsize) < uint64((1+len(d.Ports))*96<<20) {
			return fmt.Errorf("insufficient free space at %s", p)
		}
	}
	for _, unit := range []string{"ufw.service", "firewalld.service", "netfilter-persistent.service"} {
		b, e := h.command(ctx, "systemctl", "show", "--property=ActiveState", "--value", unit)
		if e != nil {
			return e
		}
		if st := strings.TrimSpace(string(b)); st != "inactive" && st != "failed" {
			return fmt.Errorf("unsupported firewall manager %s (%s)", unit, st)
		}
	}
	if _, e := h.rules(ctx, d); e != nil {
		return e
	}
	if checkPorts {
		b, e := h.command(ctx, "ss", "-H", "-lntu")
		if e != nil {
			return e
		}
		for _, s := range sockets(d) {
			if listening(string(b), s) {
				return fmt.Errorf("port occupied: %d/%s", s.port, s.net)
			}
		}
	}
	return nil
}
func listening(output string, s socket) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 && fields[0] == s.net && strings.HasSuffix(fields[4], ":"+strconv.Itoa(s.port)) {
			return true
		}
	}
	return false
}
func (h host) healthy(ctx context.Context, d deployment) error {
	deadline := time.Now().Add(8 * time.Second)
	for {
		ok := true
		for _, name := range d.units() {
			if _, e := h.command(ctx, "systemctl", "is-active", "--quiet", name); e != nil {
				ok = false
				break
			}
		}
		if ok {
			b, e := h.command(ctx, "ss", "-H", "-lntu")
			if e != nil {
				return e
			}
			for _, s := range sockets(d) {
				if !listening(string(b), s) {
					ok = false
					break
				}
			}
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("service or listening socket failed health check")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(80 * time.Millisecond):
		}
	}
}
