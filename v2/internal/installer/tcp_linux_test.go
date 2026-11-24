package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUbuntu2604TCPInstallUpgradeRollback(t *testing.T) {
	l := newLab(t)
	if err := os.WriteFile(l.path("/etc/os-release"), []byte("ID=ubuntu\nVERSION_ID=\"26.04\""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Dir(l.path(tcpConfig)), 0755); err != nil {
		t.Fatal(err)
	}
	defaults := [2]string{"cubic", "fq_codel"}
	live := defaults
	supported := false
	command := l.command
	l.command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "sysctl" {
			return command(ctx, name, args...)
		}
		key, value, _ := strings.Cut(args[1], "=")
		for i, k := range tcpKeys {
			if k != key {
				continue
			}
			if args[0] == "-n" {
				return []byte(live[i]), nil
			}
			if value == "fq" && !supported {
				return nil, errors.New("unsupported scheduler")
			}
			live[i] = value
			return nil, nil
		}
		t.Fatalf("unexpected sysctl %v", args)
		return nil, nil
	}
	o := Options{Domain: "node.test", CertFile: "/cert", KeyFile: "/key", NodePath: "/bin/true"}
	run := func(action string, success bool) {
		t.Helper()
		if err := l.run(t.Context(), action, o, sampleRender); (err == nil) != success {
			t.Fatalf("%s: %v", action, err)
		}
	}
	check := func(want [2]string, persisted bool) {
		t.Helper()
		if live != want {
			t.Fatalf("live defaults = %v; want %v", live, want)
		}
		b, err := os.ReadFile(l.path(tcpConfig))
		if persisted && (err != nil || !strings.Contains(string(b), "net.ipv4.tcp_congestion_control = bbr\nnet.core.default_qdisc = fq\n")) {
			t.Fatalf("missing boot configuration: %v", err)
		}
		if !persisted && !os.IsNotExist(err) {
			t.Fatal("unexpected boot configuration", err)
		}
	}

	// Ubuntu 26.04, optional feature absent: undo even the successful half of the probe.
	run("install", true)
	check(defaults, false)
	supported = true
	l.fail = "systemctl start nexus-v2.service"
	run("upgrade", false)
	check(defaults, false)
	run("upgrade", true)
	check(tcpWant, true)
	run("install", true)
	// Keep a later administrator override at removal.
	live[1] = "cake"
	run("uninstall", true)
	check([2]string{"cubic", "cake"}, false)
	l.fail = "systemctl enable --now nexus-v2.service"
	run("install", false)
	check([2]string{"cubic", "cake"}, false)
	if err := os.WriteFile(l.path(tcpConfig), []byte("# administrator policy\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("install", true)
	if live != [2]string{"cubic", "cake"} {
		t.Fatalf("overrode administrator config: %v", live)
	}
	run("uninstall", true)
	if b, err := os.ReadFile(l.path(tcpConfig)); err != nil || string(b) != "# administrator policy\n" {
		t.Fatal("administrator configuration lost", err)
	}
}
