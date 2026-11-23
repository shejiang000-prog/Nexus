package installer

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCertificateModesAndPorts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    Options
		mode    string
		sockets []socket
		caddy   bool
	}{
		{"V2 default", Options{Domain: "node.test"}, "acme", []socket{{"tcp", 443}, {"udp", 443}, {"tcp", 80}}, true},
		{"V2 custom", Options{Domain: "node.test", Ports: map[string]int{"v2": 7443}}, "acme", []socket{{"tcp", 7443}, {"udp", 7443}, {"tcp", 80}, {"tcp", 443}}, true},
		{"Hysteria2", Options{Domain: "node.test", Ports: map[string]int{"hysteria2": 0}}, "acme", []socket{{"tcp", 80}, {"tcp", 443}, {"udp", 443}}, true},
		{"Hysteria2 manual", Options{Domain: "node.test", CertFile: "cert", KeyFile: "key", Ports: map[string]int{"hysteria2": 0}}, "manual", []socket{{"udp", 443}}, true},
		{"Reality address", Options{PublicHost: "192.0.2.2", Ports: map[string]int{"reality": 0}}, "none", []socket{{"tcp", 443}}, false},
		{"V2 manual", Options{Domain: "node.test", CertFile: "cert", KeyFile: "key"}, "manual", []socket{{"tcp", 443}, {"udp", 443}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, e := normalize(tc.opts)
			if e != nil {
				t.Fatal(e)
			}
			d := o.deployment()
			if d.CertMode != tc.mode || !slices.Equal(sockets(d), tc.sockets) || slices.Contains(d.units(), unitName("v2")) != tc.caddy {
				t.Fatal(d, sockets(d), d.units())
			}
			if d.uses("hysteria2") && !strings.Contains(d.unitText(unitName("hysteria2")), "Requires=nexus-v2-firewall.service nexus-v2.service") {
				t.Fatal("missing Caddy dependency")
			}
		})
	}
	for _, o := range []Options{
		{Ports: map[string]int{"v2": 443}, PublicHost: "192.0.2.1"},
		{Ports: map[string]int{"hysteria2": 443}, PublicHost: "192.0.2.1"},
		{Ports: map[string]int{"reality": 443}, PublicHost: "0.0.0.0"},
		{Domain: "node.test", CertFile: "cert"},
		{Domain: "node.test", CertFile: "cert", KeyFile: "key", ACMECA: "https://ca.test/"},
		{Domain: "node.test", Ports: map[string]int{"v2": 80}},
		{Domain: "node.test", Ports: map[string]int{"hysteria2": 8443, "reality": 80}},
	} {
		if _, e := normalize(o); e == nil {
			t.Errorf("accepted conflicting settings: %+v", o)
		}
	}
}

func TestACMEPreflight(t *testing.T) {
	l := newLab(t)
	l.dnsCheck = func(context.Context, string) error { return errCallbackFailed }
	input := Options{Domain: "node.test", NodePath: "/bin/true"}
	if err := l.run(t.Context(), "install", input, sampleRender); err == nil || !strings.Contains(err.Error(), "--skip-dns-check") || l.user {
		t.Fatal(err)
	}
	input.SkipDNSCheck = true
	l.busy = "tcp LISTEN 0 100 *:80 *:*"
	if err := l.run(t.Context(), "install", input, sampleRender); err == nil || !strings.Contains(err.Error(), "80/tcp") || l.user {
		t.Fatal(err)
	}
	if _, e := os.Stat(l.path(base)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("mutated system before validation", e)
	}
}

func TestLegacyLayoutManagement(t *testing.T) {
	l := newLab(t)
	o := Options{Domain: "node.test", NodePath: "/bin/true", CertFile: "cert", KeyFile: "key"}
	if e := l.run(t.Context(), "install", o, sampleRender); e != nil {
		t.Fatal(e)
	}
	d, e := l.readManifest()
	if e != nil {
		t.Fatal(e)
	}
	d.Layout, d.Firewall, d.CertMode, d.CertFile, d.KeyFile = 0, "", "", "", ""
	if e = l.writeManifest(d); e != nil {
		t.Fatal(e)
	}
	if e = l.run(t.Context(), "status", Options{}, nil); e != nil {
		t.Fatal(e)
	}
	for _, action := range []string{"upgrade", "install"} {
		if e = l.run(t.Context(), action, o, sampleRender); e == nil || !strings.Contains(e.Error(), "back up") {
			t.Fatal(action, e)
		}
	}
	if e = l.run(t.Context(), "uninstall", Options{}, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(l.path(state + "/ledger.db")); e != nil {
		t.Fatal(e)
	}
	if e = l.run(t.Context(), "install", o, sampleRender); e == nil || !strings.Contains(e.Error(), "back up") {
		t.Fatal(e)
	}
	if e = l.run(t.Context(), "uninstall", Options{Purge: true}, nil); e != nil {
		t.Fatal(e)
	}
}

func TestACMEHairpinNAT(t *testing.T) {
	const env = "NEXUS_TEST_ACME_HAIRPIN"
	if os.Getenv(env) != "namespace" {
		if os.Geteuid() != 0 || exec.Command("unshare", "-nm", "true").Run() != nil {
			t.Skip("requires private network and mount namespaces")
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "-nm", "--propagation", "private", os.Args[0], "-test.run=^TestACMEHairpinNAT$", "-test.v")
		cmd.Env = append(os.Environ(), env+"=namespace")
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("%v: %s", e, b)
		}
		return
	}
	for _, ns := range []string{"net", "mnt"} {
		self, e := os.Readlink("/proc/self/ns/" + ns)
		if e != nil {
			t.Fatal(e)
		}
		init, e := os.Readlink("/proc/1/ns/" + ns)
		if e != nil || init == self {
			t.Fatal("missing test namespace isolation")
		}
	}
	run := func(name string, args ...string) string {
		t.Helper()
		b, err := exec.CommandContext(t.Context(), name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", name, err, b)
		}
		return string(b)
	}
	run("mount", "--make-rprivate", "/")
	hosts := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hosts, []byte("127.0.0.1 localhost\n192.0.2.87 node.test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("mount", "--bind", hosts, "/etc/hosts")
	run("ip", "link", "set", "lo", "up")
	run("ip", "addr", "add", "10.78.0.2/32", "dev", "lo")
	run("ip", "route", "add", "192.0.2.87/32", "dev", "lo", "src", "10.78.0.2")
	if strings.Contains(run("ip", "-br", "addr"), "192.0.2.87") {
		t.Fatal("public IP is assigned to an interface")
	}
	run("nft", "add", "table", "ip", "lab")
	run("nft", "add", "chain", "ip", "lab", "callback", "{ type nat hook output priority dstnat; }")
	run("nft", "add", "rule", "ip", "lab", "callback", "ip", "daddr", "192.0.2.87", "tcp", "dport", "80", "counter", "dnat", "to", "10.78.0.2")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	h := host{out: io.Discard}
	o := Options{Domain: "node.test"}
	if err := h.checkCallback(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(run("nft", "list", "chain", "ip", "lab", "callback"), "counter packets 0 ") {
		t.Fatal("callback did not cross DNAT")
	}
	run("nft", "delete", "table", "ip", "lab")
	run("ip", "route", "replace", "unreachable", "192.0.2.87/32")
	if err := h.checkCallback(t.Context(), o); !errors.Is(err, errCallbackFailed) || !strings.Contains(err.Error(), "--skip-dns-check") {
		t.Fatal("unattended", err)
	}
	// An interactive run must still confirm, even with the unattended override.
	o.SkipDNSCheck = true
	var prompts int
	for _, yes := range []bool{false, true} {
		o.ConfirmDNSFailure = func() (bool, error) { prompts++; return yes, nil }
		if err := h.checkCallback(t.Context(), o); (err == nil) != yes {
			t.Fatal("interactive", yes, err)
		}
	}
	if prompts != 2 {
		t.Fatal("failed callback did not prompt")
	}
	o.ConfirmDNSFailure = nil
	if err := h.checkCallback(t.Context(), o); err != nil {
		t.Fatal("explicit override", err)
	}
	ln, err := net.Listen("tcp", ":80")
	if err != nil {
		t.Fatal("probe left its listener open", err)
	}
	defer ln.Close()
	if err = h.checkCallback(t.Context(), o); err == nil || errors.Is(err, errCallbackFailed) {
		t.Fatal("waived local listener conflict", err)
	}
}
