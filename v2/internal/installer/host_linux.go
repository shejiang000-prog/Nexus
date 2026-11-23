package installer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	lock, e := os.Open("/opt")
	if e != nil {
		return e
	}
	defer lock.Close()
	lockErr := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if action == "firewall-apply" {
		d, e := h.readManifest()
		if e != nil {
			return e
		}
		if !d.Installed {
			return errors.New("node is uninstalled")
		}
		if lockErr == nil {
			return h.firewall(ctx, d, true)
		}
		// An installer starts this unit while holding the lock. It already
		// published the rules, so the oneshot only needs to verify them.
		plan, e := h.firewallPlan(ctx, d, true)
		if e != nil {
			return e
		}
		if plan != "" {
			return errors.New("another installation transaction is active; firewall update deferred")
		}
		return nil
	}
	if lockErr != nil {
		return errors.New("another installation transaction is active")
	}
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
	for _, p := range []string{"/proc/net/ip_tables_names", "/proc/net/ip6_tables_names"} {
		b, e := os.ReadFile(h.path(p))
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if strings.TrimSpace(string(b)) != "" {
			return errors.New("legacy iptables is active; manual firewall review required")
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
		return fmt.Errorf("needed inbound ports %v: %w", sockets(d), e)
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

var socketPID = regexp.MustCompile(`\bpid=([1-9][0-9]*)\b`)

func (h host) ownsSocket(output string, s socket, group string) bool {
	if !strings.HasPrefix(group, "/") || group == "/" {
		return false
	}
	found := false
	for _, line := range strings.Split(output, "\n") {
		if !listening(line, s) {
			continue
		}
		owned := false
		for _, m := range socketPID.FindAllStringSubmatch(line, -1) {
			b, err := os.ReadFile(h.path("/proc/" + m[1] + "/cgroup"))
			if err != nil {
				continue
			}
			for _, entry := range strings.Split(string(b), "\n") {
				parts := strings.SplitN(entry, ":", 3)
				if len(parts) == 3 && (parts[1] == "" || parts[1] == "name=systemd") && (parts[2] == group || strings.HasPrefix(parts[2], group+"/")) {
					owned = true
					break
				}
			}
		}
		if !owned {
			return false
		}
		found = true
	}
	return found
}

func (h host) protocolHealth(ctx context.Context, d deployment, p string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if p == "v2" {
		pem, e := os.ReadFile(h.path(cfg + "/cert.pem"))
		if e != nil {
			return e
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return errors.New("invalid installed V2 certificate chain")
		}
		dialer := tls.Dialer{Config: &tls.Config{MinVersion: tls.VersionTLS13, ServerName: d.Domain, RootCAs: roots, NextProtos: []string{"h2"}}}
		c, e := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(d.Ports[p])))
		if e != nil {
			return e
		}
		defer c.Close()
		if c.(*tls.Conn).ConnectionState().NegotiatedProtocol != "h2" {
			return errors.New("V2 TLS listener did not negotiate H2")
		}
		return nil
	}
	tr := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return new(net.Dialer).DialContext(ctx, "unix", h.path(state+"/compat-"+p+"/control.sock"))
	}}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/ready", nil)
	if e != nil {
		return e
	}
	resp, e := tr.RoundTrip(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("%s health returned %d", p, resp.StatusCode)
	}
	return nil
}
func (h host) healthy(ctx context.Context, d deployment) error {
	deadline := time.Now().Add(8 * time.Second)
	var last error
	for {
		last = nil
		for _, name := range d.units() {
			if _, e := h.command(ctx, "systemctl", "is-active", "--quiet", name); e != nil {
				last = e
				break
			}
		}
		if last == nil {
			b, e := h.command(ctx, "ss", "-H", "-lntup")
			if e != nil {
				return e
			}
			for _, p := range protocols {
				port, ok := d.Ports[p]
				if !ok {
					continue
				}
				group, e := h.command(ctx, "systemctl", "show", "--property=ControlGroup", "--value", unitName(p))
				if e != nil {
					return e
				}
				for _, s := range sockets(deployment{Ports: map[string]int{p: port}}) {
					if !h.ownsSocket(string(b), s, strings.TrimSpace(string(group))) {
						last = fmt.Errorf("%s does not own %d/%s", unitName(p), port, s.net)
						break
					}
				}
				if last != nil {
					break
				}
				probe := h.probe
				if probe == nil {
					probe = h.protocolHealth
				}
				if last = probe(ctx, d, p); last != nil {
					break
				}
			}
		}
		if last == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("health check failed: %w", last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(80 * time.Millisecond):
		}
	}
}
