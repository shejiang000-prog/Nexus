package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func (h host) detectFirewall(ctx context.Context, d deployment) (string, error) {
	for _, unit := range []string{"firewalld.service", "netfilter-persistent.service"} {
		state, err := h.command(ctx, "systemctl", "show", "--property=ActiveState", "--value", unit)
		if err != nil {
			return "", err
		}
		switch strings.TrimSpace(string(state)) {
		case "inactive", "failed":
			continue
		}
		if unit == "firewalld.service" {
			var flags []string
			for _, s := range sockets(d) {
				flags = append(flags, fmt.Sprintf("--add-port=%d/%s", s.port, s.net))
			}
			return "", fmt.Errorf("firewalld manages this host. Review the ingress zone, then run:\nfirewall-cmd --permanent %s && firewall-cmd --reload", strings.Join(flags, " "))
		}
		return "", fmt.Errorf("unsupported active firewall manager %s; required inbound ports %v", unit, sockets(d))
	}
	status, err := h.command(ctx, "ufw", "status", "numbered")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err == nil {
		first, _, _ := strings.Cut(strings.TrimSpace(string(status)), "\n")
		switch strings.TrimSpace(first) {
		case "Status: active":
			return "ufw", nil
		case "Status: inactive":
		default:
			return "", errors.New("unknown ufw status")
		}
	}
	for proc, tool := range map[string]string{"/proc/net/ip_tables_names": "iptables-legacy-save", "/proc/net/ip6_tables_names": "ip6tables-legacy-save"} {
		b, err := os.ReadFile(h.path(proc))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if strings.TrimSpace(string(b)) != "" {
			dump, e := h.command(ctx, tool)
			if e != nil {
				return "", fmt.Errorf("cannot verify loaded legacy tables: %w", e)
			}
			if !emptyLegacyTables(string(b), string(dump)) {
				return "", fmt.Errorf("legacy iptables is active; required inbound ports %v", sockets(d))
			}
		}
	}
	return "nft", nil
}

// UFW disable may leave ACCEPT-only built-in tables loaded in the kernel.
// Verify the saved rules, even when the system iptables alternative uses nft.
var emptyChain = regexp.MustCompile(`^:([A-Z]+) ACCEPT \[[0-9]+:[0-9]+\]$`)

func builtinHook(table, name string) bool {
	switch table {
	case "filter":
		return name == "INPUT" || name == "FORWARD" || name == "OUTPUT"
	case "mangle":
		return name == "PREROUTING" || name == "INPUT" || name == "FORWARD" || name == "OUTPUT" || name == "POSTROUTING"
	case "nat":
		return name == "PREROUTING" || name == "INPUT" || name == "OUTPUT" || name == "POSTROUTING"
	}
	return false
}
func emptyLegacyTables(loaded, dump string) bool {
	pending := map[string]bool{}
	for _, table := range strings.Fields(loaded) {
		pending[table] = true
	}
	var table string
	chains := 0
	for _, line := range strings.Split(dump, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case table == "" && strings.HasPrefix(line, "*") && pending[line[1:]]:
			table = line[1:]
			chains = 0
		case table != "" && line == "COMMIT" && chains > 0:
			delete(pending, table)
			table = ""
		default:
			m := emptyChain.FindStringSubmatch(line)
			if m == nil || !builtinHook(table, m[1]) {
				return false
			}
			chains++
		}
	}
	return table == "" && len(pending) == 0
}

func (h host) checkFirewall(ctx context.Context, d deployment) error {
	if d.Layout == 0 {
		return nil
	}
	manager, err := h.detectFirewall(ctx, d)
	if err != nil {
		return err
	}
	if d.Firewall != manager {
		return fmt.Errorf("firewall manager changed from %s to %s; review previously installed ingress rules", d.Firewall, manager)
	}
	return nil
}

func (h host) firewallPending(ctx context.Context, d deployment, add bool) (bool, error) {
	if e := h.checkFirewall(ctx, d); e != nil {
		return false, e
	}
	if d.Firewall == "ufw" {
		exists, err := h.hasProfile(d)
		if err != nil {
			return false, err
		}
		plan, err := h.ufwPlan(ctx, d, add)
		return len(plan) > 0 || exists != add, err
	}
	cmd, err := h.firewallPlan(ctx, d, add)
	return cmd != "", err
}

type familySocket struct {
	socket
	v6 bool
}

func (s familySocket) marker(d deployment) string {
	family := 4
	if s.v6 {
		family = 6
	}
	return fmt.Sprintf("nexus-v2-%s-v%d-%s-%d", d.Token, family, s.net, s.port)
}

type ufwRule struct {
	owned bool
	key   familySocket
}

func profilePath(d deployment) string { return "/etc/ufw/applications.d/nexus-v2-" + d.Token }
func profileText(d deployment) string {
	var b strings.Builder
	for _, s := range sockets(d) {
		for _, v6 := range []bool{false, true} {
			key := familySocket{s, v6}
			fmt.Fprintf(&b, "[%s]\ntitle=Nexus ingress\ndescription=Managed node ingress\nports=%d/%s\n\n", key.marker(d), s.port, s.net)
		}
	}
	return b.String()
}
func (h host) hasProfile(d deployment) (bool, error) {
	path := h.path(profilePath(d))
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0644 {
		return false, errors.New("unsafe UFW application profile")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if string(data) != profileText(d) {
		return false, errors.New("UFW application profile was modified")
	}
	return true, nil
}

var (
	numberedRule = regexp.MustCompile(`^\[\s*[1-9][0-9]*\]\s+(.*)$`)
	publicAllow  = regexp.MustCompile(`^(\S+)( \(v6\))?\s+ALLOW( IN)?\s+Anywhere( \(v6\))?$`)
)

func (h host) ufwRules(ctx context.Context, d deployment) ([]ufwRule, error) {
	out, err := h.command(ctx, "ufw", "status", "numbered")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if lines[0] != "Status: active" {
		return nil, errors.New("ufw is no longer active")
	}
	expected := map[string]familySocket{}
	for _, s := range sockets(d) {
		for _, v6 := range []bool{false, true} {
			key := familySocket{s, v6}
			expected[key.marker(d)] = key
		}
	}
	prefix := "nexus-v2-" + d.Token + "-"
	var rules []ufwRule
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") {
			continue
		}
		index := numberedRule.FindStringSubmatch(line)
		if index == nil {
			return nil, errors.New("unrecognized numbered ufw rule")
		}
		body, comment, _ := strings.Cut(index[1], "#")
		comment = strings.TrimSpace(comment)
		fields := publicAllow.FindStringSubmatch(strings.TrimSpace(body))
		if strings.HasPrefix(body, prefix) || strings.HasPrefix(comment, prefix) {
			if fields == nil || fields[1] != comment {
				return nil, errors.New("owned ufw application rule changed")
			}
			key, ok := expected[fields[1]]
			if !ok || key.v6 != (fields[2] != "" || fields[4] != "") {
				return nil, errors.New("unexpected owned ufw application rule")
			}
			rules = append(rules, ufwRule{true, key})
			continue
		}
		if fields == nil {
			continue
		}
		port, net, ok := strings.Cut(fields[1], "/")
		n, err := strconv.Atoi(port)
		if !ok || err != nil || (net != "tcp" && net != "udp") {
			continue
		}
		rules = append(rules, ufwRule{false, familySocket{socket{net, n}, fields[2] != "" || fields[4] != ""}})
	}
	return rules, nil
}

func (h host) ufwPlan(ctx context.Context, d deployment, add bool) ([]familySocket, error) {
	rules, err := h.ufwRules(ctx, d)
	if err != nil {
		return nil, err
	}
	var plan []familySocket
	if !add {
		for i := len(rules) - 1; i >= 0; i-- {
			if r := rules[i]; r.owned {
				plan = append(plan, r.key)
			}
		}
		return plan, nil
	}
	b, err := os.ReadFile(h.path("/etc/default/ufw"))
	if err != nil {
		return nil, err
	}
	var ipv6 string
	for _, line := range strings.Split(string(b), "\n") {
		line, _, _ = strings.Cut(line, "#")
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		if k == "IPV6" {
			ipv6 = strings.Trim(v, " \"'")
		}
	}
	if ipv6 != "yes" && ipv6 != "no" {
		return nil, errors.New("unrecognized IPV6 setting in /etc/default/ufw")
	}
	for _, sock := range sockets(d) {
		for _, v6 := range []bool{false, true} {
			if v6 && ipv6 == "no" {
				continue
			}
			key := familySocket{sock, v6}
			if !slices.ContainsFunc(rules, func(r ufwRule) bool { return r.key == key }) {
				plan = append(plan, key)
			}
		}
	}
	return plan, nil
}

// UFW includes the application name in rule identity. A concurrent insertion
// or a plain port rule with the same destination cannot redirect deletion.
func (h host) changeUFW(ctx context.Context, d deployment, key familySocket, add bool) error {
	rules, err := h.ufwRules(ctx, d)
	if err != nil {
		return err
	}
	owned := false
	for _, r := range rules {
		if r.key != key {
			continue
		}
		if add {
			return nil
		}
		owned = owned || r.owned
	}
	if !add && !owned {
		return nil
	}
	source := "0.0.0.0/0"
	if key.v6 {
		source = "::/0"
	}
	args := []string{"allow", "in", "from", source, "to", "any", "app", key.marker(d), "comment", key.marker(d)}
	if !add {
		args = append([]string{"delete"}, args...)
	}
	_, err = h.command(ctx, "ufw", args...)
	return err
}
func (h host) applyUFW(ctx context.Context, d deployment, add bool) (err error) {
	plan, err := h.ufwPlan(ctx, d, add)
	if err != nil {
		return err
	}
	exists, err := h.hasProfile(d)
	if err != nil {
		return err
	}
	path := h.path(profilePath(d))
	if !exists {
		if !add {
			if len(plan) > 0 {
				return errors.New("owned UFW rules have lost their application profile")
			}
			return nil
		}
		if err = copyFile(path, strings.NewReader(profileText(d)), 0644); err != nil {
			return err
		}
	}
	var undo []familySocket
	defer func() {
		if err != nil {
			for i := len(undo) - 1; i >= 0; i-- {
				err = errors.Join(err, h.changeUFW(context.Background(), d, undo[i], !add))
			}
			if !exists {
				left, e := h.ufwPlan(context.Background(), d, false)
				err = errors.Join(err, e)
				if e == nil && len(left) == 0 {
					err = errors.Join(err, os.Remove(path))
				}
			}
		}
	}()
	for _, key := range plan {
		// A helper may have committed a change before reporting failure.
		undo = append(undo, key)
		if err = h.changeUFW(ctx, d, key, add); err != nil {
			return err
		}
	}
	remaining, err := h.ufwPlan(ctx, d, add)
	if err == nil && len(remaining) > 0 {
		return errors.New("ufw did not apply the requested rules")
	}
	if err == nil && !add {
		err = os.Remove(path)
	}
	return err
}
