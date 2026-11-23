package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type ufwLab struct {
	status         string
	rows           []string
	failAt, writes int
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
	switch {
	case args[0] == "allow" && len(args) == 12:
		family := ""
		if args[5] == "::/0" {
			family = " (v6)"
		} else if args[5] != "0.0.0.0/0" {
			return nil, fmt.Errorf("bad source family")
		}
		u.rows = append(u.rows, fmt.Sprintf("%s/%s%s   ALLOW IN    Anywhere%s  # %s", args[9], args[3], family, family, args[11]))
	case len(args) == 3 && args[0] == "--force" && args[1] == "delete":
		n, e := strconv.Atoi(args[2])
		if e != nil || n <= 0 || n > len(u.rows) {
			return nil, errors.New("invalid rule number")
		}
		u.rows = slices.Delete(u.rows, n-1, n)
	default:
		return nil, fmt.Errorf("unexpected ufw arguments %v", args)
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
	ctx := t.Context()
	u.failAt = 3
	if err := h.firewall(ctx, d, true); err == nil || !slices.Equal(base, u.rows) {
		t.Fatalf("partial addition changed existing rules: %v %v", err, u.rows)
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
	if e := h.firewall(ctx, d, false); e != nil || !slices.Equal(base, u.rows) {
		t.Fatal("uninstall changed administrator rules", e, u.rows)
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
