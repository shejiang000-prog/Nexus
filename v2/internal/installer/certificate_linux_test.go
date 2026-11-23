package installer

import (
	"context"
	"errors"
	"os"
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
	if e := localDNS(t.Context(), "127.0.0.1"); e != nil {
		t.Fatal("loopback", e)
	}
	if e := localDNS(t.Context(), "192.0.2.73"); e == nil {
		t.Fatal("accepted nonlocal address")
	}
	l := newLab(t)
	l.dnsCheck = func(context.Context, string) error { return errors.New("unrelated A record") }
	input := Options{Domain: "node.test", NodePath: "/bin/true"}
	if err := l.run(t.Context(), "install", input, sampleRender); err == nil || !strings.Contains(err.Error(), "DNS preflight") || l.user {
		t.Fatal(err)
	}
	l.dnsCheck = func(context.Context, string) error { return nil }
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
