package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type ufwLab struct {
	status, insertAtDelete, profileFile string
	rows                                []string
	failAt, writes                      int
}

func (u *ufwLab) run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "systemctl" {
		return []byte("inactive"), nil
	}
	if name != "ufw" {
		return nil, fmt.Errorf("unexpected helper %s", name)
	}
	if slices.Equal(args, []string{"status", "numbered"}) {
		out := "Status: " + u.status + "\n"
		for i, row := range u.rows {
			out += fmt.Sprintf("[%2d] %s\n", i+1, row)
		}
		return []byte(out), nil
	}
	remove := args[0] == "delete"
	if remove {
		args = args[1:]
		if u.insertAtDelete != "" {
			u.rows = slices.Insert(u.rows, 0, u.insertAtDelete)
			u.insertAtDelete = ""
		}
	}
	if len(args) != 10 || args[0] != "allow" || args[2] != "from" || args[6] != "app" || args[7] != args[9] {
		return nil, fmt.Errorf("unexpected ufw arguments %v", args)
	}
	source, name := args[3], args[7]
	family := ""
	if source == "::/0" {
		family = " (v6)"
	} else if source != "0.0.0.0/0" {
		return nil, fmt.Errorf("bad source family")
	}
	data, e := os.ReadFile(u.profileFile)
	if e != nil || !strings.Contains(string(data), "["+name+"]") {
		return nil, errors.New("missing named application profile")
	}
	row := fmt.Sprintf("%s%s  ALLOW IN  Anywhere%s  # %s", name, family, family, name)
	if remove {
		i := slices.Index(u.rows, row)
		if i < 0 {
			return nil, errors.New("missing application rule")
		}
		u.rows = slices.Delete(u.rows, i, i+1)
	} else {
		u.rows = append(u.rows, row)
	}
	u.writes++
	if u.writes == u.failAt {
		return nil, errors.New("injected failure after rule mutation")
	}
	return nil, nil
}

func TestOwnedUFW(t *testing.T) {
	d := deployment{Layout: 2, Ports: map[string]int{"v2": 443}, CertMode: "acme", Firewall: "ufw", Token: "abc"}
	const existing = "443/tcp          ALLOW IN    Anywhere  # operator-provided ingress"
	const unrelated = "22/tcp           ALLOW IN    Anywhere"
	base := []string{unrelated, existing}
	u := &ufwLab{status: "active", rows: slices.Clone(base)}
	h := host{root: t.TempDir(), command: u.run}
	if e := os.MkdirAll(h.path("/etc/default"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(h.path("/etc/default/ufw"), []byte("IPV6=yes\n"), 0644); e != nil {
		t.Fatal(e)
	}
	u.profileFile = h.path(profilePath(d))
	if e := os.MkdirAll(filepath.Dir(u.profileFile), 0755); e != nil {
		t.Fatal(e)
	}
	ctx := t.Context()
	u.failAt = 3
	if err := h.firewall(ctx, d, true); err == nil || !slices.Equal(base, u.rows) {
		t.Fatalf("partial addition changed existing rules: %v %v", err, u.rows)
	}
	if _, e := os.Stat(u.profileFile); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("profile leaked on rollback", e)
	}
	u.failAt = 0
	if err := h.firewall(ctx, d, true); err != nil {
		t.Fatal(err)
	}
	if len(u.rows) != 7 || u.rows[0] != unrelated || u.rows[1] != existing {
		t.Fatalf("unexpected installed rules %v", u.rows)
	}
	before := u.writes
	if pending, e := h.firewallPending(ctx, d, true); e != nil || pending {
		t.Fatal(pending, e)
	}
	if e := h.firewall(ctx, d, true); e != nil || u.writes != before {
		t.Fatal("repeated installation modified rules", e)
	}
	installed := slices.Clone(u.rows)
	u.failAt = u.writes + 2
	if err := h.firewall(ctx, d, false); err == nil || !slices.Equal(u.rows, installed) {
		t.Fatal("failed removal lost/reordered rules", err, u.rows)
	}
	u.rows[2] = strings.Replace(u.rows[2], "ALLOW IN", "DENY IN", 1)
	if e := h.firewall(ctx, d, false); e == nil {
		t.Fatal("removed altered owned rule")
	}
	u.rows = installed
	u.failAt = 0
	const concurrent = "443/udp   ALLOW IN    Anywhere   # added between status and delete"
	u.insertAtDelete = concurrent
	if e := h.firewall(ctx, d, false); e != nil || !slices.Equal(slices.Concat([]string{concurrent}, base), u.rows) {
		t.Fatal("uninstall changed administrator rules", e, u.rows)
	}
	if _, e := os.Stat(u.profileFile); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("profile leaked on uninstall", e)
	}
	u.status = "inactive"
	d.Firewall = "ufw"
	if e := h.firewall(ctx, d, true); e == nil || !strings.Contains(e.Error(), "changed from ufw to nft") {
		t.Fatal("manager drift accepted", e)
	}
	if mode, err := h.detectFirewall(ctx, d); err != nil || mode != "nft" {
		t.Fatal(mode, err)
	}
}

func TestFirewalldPreflightInstructions(t *testing.T) {
	h := host{command: func(_ context.Context, n string, args ...string) ([]byte, error) {
		if n != "systemctl" || args[len(args)-1] != "firewalld.service" {
			t.Fatalf("unexpected command: %s %v", n, args)
		}
		return []byte("active"), nil
	}}
	_, err := h.detectFirewall(t.Context(), deployment{Ports: map[string]int{"hysteria2": 9443}, CertMode: "acme"})
	if err == nil {
		t.Fatal("accepted active firewalld")
	}
	for _, part := range []string{"firewall-cmd --permanent", "--add-port=80/tcp", "--add-port=443/tcp", "--add-port=9443/udp", "firewall-cmd --reload"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatal("missing command", part, err)
		}
	}
}

func TestInactiveUFWLegacyResidue(t *testing.T) {
	const clean = "# generated\n*filter\n:INPUT ACCEPT [3:7]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [6:9]\nCOMMIT\n"
	h := host{root: t.TempDir()}
	if e := os.MkdirAll(h.path("/proc/net"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(h.path("/proc/net/ip_tables_names"), []byte("filter\n"), 0644); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		dump string
		ok   bool
	}{
		{clean, true},
		{strings.Replace(clean, "INPUT ACCEPT", "INPUT DROP", 1), false},
		{strings.Replace(clean, "COMMIT", "-A INPUT -j DROP\nCOMMIT", 1), false},
		{strings.Replace(clean, ":INPUT ACCEPT", ":PRIVATE -", 1), false},
		{"", false},
	} {
		h.command = func(_ context.Context, name string, _ ...string) ([]byte, error) {
			switch name {
			case "systemctl":
				return []byte("inactive"), nil
			case "ufw":
				return []byte("Status: inactive"), nil
			case "iptables-legacy-save":
				return []byte(tc.dump), nil
			}
			t.Fatal("unexpected helper", name)
			return nil, nil
		}
		mode, e := h.detectFirewall(t.Context(), deployment{})
		if tc.ok != (e == nil && mode == "nft") {
			t.Errorf("dump %q: %q, %v", tc.dump, mode, e)
		}
	}
}
