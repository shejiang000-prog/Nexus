package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type exitStatus int

func (s exitStatus) Error() string { return fmt.Sprintf("exit %d", s) }
func (s exitStatus) ExitCode() int { return int(s) }

type lab struct {
	host
	t                       *testing.T
	user                    bool
	active                  map[string]bool
	nft, busy, fail, backup string
	starts                  int
}

func newLab(t *testing.T) *lab {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("private-root transaction tests require chown")
	}
	l := &lab{t: t, active: map[string]bool{}}
	l.host = host{root: t.TempDir(), out: io.Discard, command: l.runCmd, probe: func(context.Context, deployment, string) error { return nil }}
	for _, p := range []string{"/opt", "/etc", "/var/lib", unitDir, "/proc/999"} {
		if e := os.MkdirAll(l.path(p), 0755); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(l.path("/proc/999/cgroup"), []byte("0::/system.slice/nexus-v2.service"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(l.path("/etc/os-release"), []byte("ID=debian\nVERSION_ID=13"), 0644); e != nil {
		t.Fatal(e)
	}
	return l
}
func (l *lab) runCmd(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	if l.fail != "" && strings.HasPrefix(call, l.fail) {
		l.fail = ""
		return nil, errors.New("injected failure")
	}
	text := func(s string) ([]byte, error) { return []byte(s), nil }
	switch name {
	case "systemctl":
		unit := args[len(args)-1]
		switch args[0] {
		case "show":
			if args[1] == "--property=ControlGroup" {
				return text("/system.slice/" + unit)
			}
			if args[1] == "--property=LoadState" {
				return text("not-found")
			}
			return text("inactive")
		case "enable", "start":
			l.starts++
			l.active[unit] = true
		case "disable", "stop":
			delete(l.active, unit)
		case "is-active":
			if !l.active[unit] {
				return nil, exitStatus(3)
			}
			return text("active")
		}
	case "nft":
		if args[0] == "-f" {
			f, e := os.ReadFile(args[1])
			if e != nil {
				return nil, e
			}
			switch {
			case strings.Contains(string(f), "create table"):
				d, e := l.readManifest()
				if e != nil {
					return nil, e
				}
				l.nft = "nexus-v2-" + d.Token
			case strings.Contains(string(f), "delete table"):
				l.nft = ""
			}
			return text("")
		}
		if l.nft != "" {
			return text(fmt.Sprintf("{\"nftables\":[{\"table\":{\"family\":\"inet\",\"name\":\"nexus_v2\",\"comment\":%q}},{\"chain\":{\"family\":\"inet\",\"table\":\"nexus_v2\",\"name\":\"input\",\"type\":\"filter\",\"hook\":\"input\",\"policy\":\"accept\"}}]}", l.nft))
		}
		return text("{\"nftables\":[{\"metainfo\":{}}]}")
	case "ss":
		out := l.busy
		for p, on := range l.active {
			if p == unitName("v2") && on {
				out += "tcp LISTEN 0 100 *:443 *:* users:((pid=999,fd=3))\nudp UNCONN 0 0 *:443 *:* users:((pid=999,fd=4))\n"
			}
		}
		return text(out)
	case "timedatectl":
		return text("yes")
	case "getent":
		if !l.user {
			return nil, exitStatus(2)
		}
		return text(account)
	case "id":
		return text("12345")
	case "useradd":
		l.user = true
	case "userdel":
		l.user = false
	case "runuser":
		cmd := args[3]
		rest := args[4:]
		switch cmd {
		case base + "/bin/nexus-node":
			db := l.path(state + "/ledger.db")
			if rest[2] == "backup" {
				return nil, copyPath(l.path(rest[3]), db, 0600)
			}
			if _, e := os.Stat(db); os.IsNotExist(e) {
				return nil, os.WriteFile(db, []byte("initial"), 0600)
			}
		case "/usr/bin/mkdir":
			return nil, os.Mkdir(l.path(rest[len(rest)-1]), 0700)
		case "/usr/bin/mv":
			src, dest := rest[len(rest)-2], rest[len(rest)-1]
			return nil, os.Rename(l.path(src), filepath.Join(l.path(dest), filepath.Base(src)))
		case "/usr/bin/cp":
			src, dest := rest[len(rest)-2], rest[len(rest)-1]
			if strings.HasSuffix(dest, "/") {
				dest = filepath.Join(dest, filepath.Base(src))
			}
			return nil, copyPath(l.path(dest), l.path(src), 0600)
		default:
			return nil, fmt.Errorf("unexpected service command %s", cmd)
		}
	default:
		return nil, fmt.Errorf("unexpected helper %s", name)
	}
	return nil, nil
}
func sampleRender(_ context.Context, _ Options, dir string) error {
	return os.WriteFile(filepath.Join(dir, "v2.json"), []byte("private"), 0600)
}
func TestInstallFailureRollback(t *testing.T) {
	for _, point := range []string{"port", "useradd", "ledger", "daemon-reload", "firewall", "enable", "health"} {
		t.Run(point, func(t *testing.T) {
			l := newLab(t)
			o := Options{Domain: "node.test", NodePath: "/bin/true"}
			switch point {
			case "port":
				l.busy = "udp UNCONN 0 0 *:443 *:*"
			case "useradd":
				l.fail = "useradd "
			case "ledger":
				l.fail = "runuser -u nexus-v2 -- /opt"
			case "daemon-reload":
				l.fail = "systemctl daemon-reload"
			case "firewall":
				l.fail = "nft -f"
			case "enable":
				l.fail = "systemctl enable --now nexus-v2.service"
			case "health":
				l.fail = "ss -H -lntup"
				o.Ports = map[string]int{"v2": 443}
			}
			if e := l.run(t.Context(), "install", o, sampleRender); e == nil {
				t.Fatal("accepted fault")
			}
			for _, p := range []string{base, cfg, state, filepath.Join(unitDir, unitName("v2")), filepath.Join(unitDir, "nexus-v2-firewall.service")} {
				if e := absent(l.path(p)); e != nil {
					t.Fatal(e)
				}
			}
			if l.user || l.nft != "" || len(l.active) > 0 {
				t.Fatalf("resources leaked: account=%t rules=%q active=%v", l.user, l.nft, l.active)
			}
		})
	}
}
func TestInstallRepeatUpgradeRollbackAndPurge(t *testing.T) {
	oldMask := syscall.Umask(0077)
	defer syscall.Umask(oldMask)
	l := newLab(t)
	o := Options{Domain: "node.test", NodePath: "/bin/true"}
	run := func(action string, o Options) {
		t.Helper()
		if e := l.run(t.Context(), action, o, sampleRender); e != nil {
			t.Fatal(e)
		}
	}
	run("install", o)
	for path, mode := range map[string]os.FileMode{base: 0755, base + "/bin": 0755, base + "/bin/nexus-node": 0755, unitDir + "/nexus-v2.service": 0644, cfg: 0700, cfg + "/v2.json": 0600} {
		fi, e := os.Stat(l.path(path))
		if e != nil || fi.Mode().Perm() != mode {
			t.Fatalf("mode at %s: %v %v", path, fi, e)
		}
	}
	started := l.starts
	run("install", o)
	if started != l.starts {
		t.Fatal("idempotent install restarted services")
	}
	run("status", o)
	newNode := Options{NodePath: "/bin/false"}
	l.fail = "systemctl start nexus-v2.service"
	if e := l.run(t.Context(), "upgrade", newNode, nil); e == nil || !strings.Contains(e.Error(), "previous version restored") {
		t.Fatal("upgrade rollback", e)
	}
	b1, _ := os.ReadFile("/bin/true")
	b2, _ := os.ReadFile(l.path(base + "/bin/nexus-node"))
	if !bytes.Equal(b1, b2) || !l.active[unitName("v2")] {
		t.Fatal("previous binary/service lost")
	}
	backups, _ := filepath.Glob(l.path(state + "/backups/upgrade-*/*.db"))
	if len(backups) != 2 {
		t.Fatal("missing online/cutover snapshot")
	}
	run("upgrade", newNode)
	run("uninstall", Options{})
	if !l.user || l.nft != "" || len(l.active) != 0 {
		t.Fatal("wrong retained resources")
	}
	if _, e := os.Stat(l.path(state + "/ledger.db")); e != nil {
		t.Fatal(e)
	}
	// A trial start after reinstall may migrate the retained ledger.
	oldRunner := l.command
	l.command = func(ctx context.Context, n string, args ...string) ([]byte, error) {
		if n == "systemctl" && strings.Join(args, " ") == "enable --now nexus-v2.service" {
			if e := os.WriteFile(l.path(state+"/ledger.db"), []byte("trial schema"), 0600); e != nil {
				t.Fatal(e)
			}
			return nil, errors.New("failed new service")
		}
		return oldRunner(ctx, n, args...)
	}
	if e := l.run(t.Context(), "install", o, sampleRender); e == nil {
		t.Fatal("expected failed reinstall")
	}
	l.command = oldRunner
	if b, e := os.ReadFile(l.path(state + "/ledger.db")); e != nil || string(b) != "initial" {
		t.Fatal("retained database was not restored", e)
	}
	run("install", o)
	run("uninstall", Options{Purge: true})
	if l.user || absent(l.path(state)) != nil || absent(l.path(base)) != nil {
		t.Fatal("purge incomplete")
	}
}
func TestPortsAndCorruptAsset(t *testing.T) {
	for _, ports := range []map[string]int{{"reality": 443, "hysteria2": 0}, {"v2": 0, "reality": 8443, "hysteria2": 8443}, {"other": 443}} {
		if _, e := normalize(Options{Domain: "node.test", Ports: ports}); e == nil {
			t.Fatal(ports)
		}
	}
	o, e := normalize(Options{Domain: "node.test", Ports: map[string]int{"v2": 0, "reality": 0, "hysteria2": 0}})
	if e != nil || o.Ports["v2"] != 443 || o.Ports["reality"] == o.Ports["hysteria2"] {
		t.Fatal(o, e)
	}
	dir, stage := t.TempDir(), t.TempDir()
	a := release("hysteria2", "amd64")
	if e = os.WriteFile(filepath.Join(dir, a.name), []byte("damaged"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = fetch(t.Context(), a, dir, stage); e == nil || absent(filepath.Join(stage, a.binary)) != nil {
		t.Fatal("corrupt asset accepted")
	}
	a.sha = fmt.Sprintf("%x", sha256.Sum256([]byte("damaged")))
	if e = fetch(t.Context(), a, dir, stage); e == nil {
		t.Fatal("non-ELF accepted")
	}
}

// Exercise kernel nft transactions in a separate net namespace if available.
func TestScopedNft(t *testing.T) {
	if os.Getenv("NEXUS_INSTALLER_NFT_TEST") != "namespace" {
		if os.Geteuid() != 0 {
			t.Skip("network namespace needs CAP_NET_ADMIN")
		}
		if err := exec.Command("unshare", "-n", "true").Run(); err != nil {
			t.Skip("network namespaces unavailable")
		}
		if _, err := exec.LookPath("nft"); err != nil {
			t.Skip("nft unavailable")
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "-n", os.Args[0], "-test.run=^TestScopedNft$")
		cmd.Env = append(os.Environ(), "NEXUS_INSTALLER_NFT_TEST=namespace")
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("%v %s", e, b)
		}
		return
	}
	self, e := os.Readlink("/proc/self/ns/net")
	if e != nil {
		t.Fatal(e)
	}
	init, e := os.Readlink("/proc/1/ns/net")
	if e != nil || init == self {
		t.Fatal("test network namespace lacks isolation")
	}
	h := host{command: execute}
	d := deployment{Token: "0123456789abcdef", Ports: map[string]int{"v2": 443}}
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	check(h.firewall(t.Context(), d, true))
	check(h.firewall(t.Context(), d, true))
	check(h.firewall(t.Context(), d, false))
	for _, line := range [][]string{{"add", "table", "inet", "filter"}, {"add", "chain", "inet", "filter", "input", "{ type filter hook input priority filter; policy drop; }"}, {"add", "rule", "inet", "filter", "input", "tcp", "dport", "443", "accept"}} {
		_, e := execute(t.Context(), "nft", line...)
		check(e)
	}
	baseline, e := execute(t.Context(), "nft", "-j", "-a", "list", "ruleset")
	check(e)
	check(h.firewall(t.Context(), d, true))
	r, e := h.rules(t.Context(), d)
	check(e)
	if len(r.entries) != 2 {
		t.Fatal("duplicated existing entry", len(r.entries))
	}
	check(h.firewall(t.Context(), d, false))
	after, e := execute(t.Context(), "nft", "-j", "-a", "list", "ruleset")
	check(e)
	if !bytes.Equal(baseline, after) {
		t.Fatal("foreign firewall changed")
	}
	_, e = execute(t.Context(), "nft", "add", "rule", "inet", "filter", "input", "ip", "saddr", "127.0.0.1", "drop")
	check(e)
	if _, e = h.rules(t.Context(), d); e == nil {
		t.Fatal("unrecognized rule accepted")
	}
}
