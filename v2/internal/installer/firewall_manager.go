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
	for _, p := range []string{"/proc/net/ip_tables_names", "/proc/net/ip6_tables_names"} {
		b, err := os.ReadFile(h.path(p))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if strings.TrimSpace(string(b)) != "" {
			return "", fmt.Errorf("legacy iptables is active; required inbound ports %v", sockets(d))
		}
	}
	return "nft", nil
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
		plan, err := h.ufwPlan(ctx, d, add)
		return len(plan) > 0, err
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
	number  int
	comment string
	key     familySocket
}

var (
	numberedRule = regexp.MustCompile(`^\[\s*([1-9][0-9]*)\]\s+(.*)$`)
	publicAllow  = regexp.MustCompile(`^([0-9]+)/(tcp|udp)( \(v6\))?\s+ALLOW( IN)?\s+Anywhere( \(v6\))?$`)
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
		body, comment, _ := strings.Cut(index[2], "#")
		comment = strings.TrimSpace(comment)
		fields := publicAllow.FindStringSubmatch(strings.TrimSpace(body))
		if fields == nil {
			if strings.HasPrefix(comment, "nexus-v2-"+d.Token+"-") {
				return nil, errors.New("owned ufw rule changed")
			}
			continue
		}
		port, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, err
		}
		key := familySocket{socket{fields[2], port}, fields[3] != "" || fields[5] != ""}
		if strings.HasPrefix(comment, "nexus-v2-"+d.Token+"-") && (comment != key.marker(d) || !slices.Contains(sockets(d), key.socket)) {
			return nil, errors.New("owned ufw rule changed")
		}
		n, err := strconv.Atoi(index[1])
		if err != nil {
			return nil, err
		}
		rules = append(rules, ufwRule{n, comment, key})
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
			if r := rules[i]; r.comment == r.key.marker(d) {
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

// Re-read rule numbers immediately before deletion. An equivalent rule with
// an administrator's comment must survive rollback and uninstall.
func (h host) changeUFW(ctx context.Context, d deployment, key familySocket, add bool) error {
	rules, err := h.ufwRules(ctx, d)
	if err != nil {
		return err
	}
	for _, r := range rules {
		if r.key != key {
			continue
		}
		if add {
			return nil
		}
		if r.comment == key.marker(d) {
			_, err = h.command(ctx, "ufw", "--force", "delete", strconv.Itoa(r.number))
			return err
		}
	}
	if !add {
		return nil
	}
	from := "0.0.0.0/0"
	if key.v6 {
		from = "::/0"
	}
	_, err = h.command(ctx, "ufw", "allow", "in", "proto", key.net, "from", from, "to", "any", "port", strconv.Itoa(key.port), "comment", key.marker(d))
	return err
}

func (h host) applyUFW(ctx context.Context, d deployment, add bool) (err error) {
	plan, err := h.ufwPlan(ctx, d, add)
	if err != nil {
		return err
	}
	var undo []familySocket
	defer func() {
		if err != nil {
			for i := len(undo) - 1; i >= 0; i-- {
				err = errors.Join(err, h.changeUFW(context.Background(), d, undo[i], !add))
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
	return err
}
