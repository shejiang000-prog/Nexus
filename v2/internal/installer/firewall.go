package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
)

type socket struct {
	net  string
	port int
}

func serviceSockets(d deployment, p string) []socket {
	var out []socket
	if port := d.Ports[p]; port != 0 {
		if p != "hysteria2" {
			out = append(out, socket{"tcp", port})
		}
		if p != "reality" {
			out = append(out, socket{"udp", port})
		}
	}
	if p == "v2" && d.CertMode == "acme" {
		for _, port := range []int{80, 443} {
			s := socket{"tcp", port}
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return out
}

func sockets(d deployment) []socket {
	var out []socket
	for _, p := range protocols {
		out = append(out, serviceSockets(d, p)...)
	}
	return out
}

type nftEntry struct {
	Family, Table, Chain, Name, Type, Hook, Policy, Comment string
	Handle                                                  int
	Expr                                                    []json.RawMessage
}
type ruleset struct {
	managed, native, filter bool
	token                   string
	entries                 []nftEntry
}

// Restrict changes to base policies whose existing exceptions we understand.
func verdictMatch(v nftEntry) (map[string]any, bool) {
	var expressions []map[string]any
	for _, raw := range v.Expr {
		var object map[string]any
		if json.Unmarshal(raw, &object) != nil || len(object) != 1 {
			return nil, false
		}
		if _, ok := object["counter"]; !ok {
			expressions = append(expressions, object)
		}
	}
	if len(expressions) != 2 {
		return nil, false
	}
	if _, ok := expressions[1]["accept"]; !ok {
		return nil, false
	}
	match, ok := expressions[0]["match"].(map[string]any)
	return match, ok
}
func allowedPort(v nftEntry) (socket, bool) {
	m, ok := verdictMatch(v)
	if !ok || m["op"] != "==" {
		return socket{}, false
	}
	port, ok := m["right"].(float64)
	if !ok || port != float64(int(port)) || port < 1 || port > 65535 {
		return socket{}, false
	}
	for _, n := range []string{"tcp", "udp"} {
		if reflect.DeepEqual(m["left"], map[string]any{"payload": map[string]any{"protocol": n, "field": "dport"}}) {
			return socket{n, int(port)}, true
		}
	}
	return socket{}, false
}
func basicAllow(v nftEntry) bool {
	m, ok := verdictMatch(v)
	if !ok {
		return false
	}
	if m["op"] == "==" && m["right"] == "lo" && reflect.DeepEqual(m["left"], map[string]any{"meta": map[string]any{"key": "iifname"}}) {
		return true
	}
	if !reflect.DeepEqual(m["left"], map[string]any{"ct": map[string]any{"key": "state"}}) {
		return false
	}
	if m["op"] != "in" && m["op"] != "==" {
		return false
	}
	switch right := m["right"].(type) {
	case string:
		return right == "established" || right == "related"
	case map[string]any:
		states, ok := right["set"].([]any)
		if !ok || len(states) == 0 {
			return false
		}
		for _, x := range states {
			if x != "established" && x != "related" {
				return false
			}
		}
		return true
	}
	return false
}

func (h host) rules(ctx context.Context, d deployment) (ruleset, error) {
	r := ruleset{token: "nexus-v2-" + d.Token}
	b, e := h.command(ctx, "nft", "-j", "-a", "list", "ruleset")
	if e != nil {
		return r, e
	}
	var doc struct{ Nftables []map[string]nftEntry }
	if e = json.Unmarshal(b, &doc); e != nil || doc.Nftables == nil {
		return r, errors.New("unrecognized nftables ruleset")
	}
	for _, obj := range doc.Nftables {
		for k, v := range obj {
			if k == "metainfo" {
				continue
			}
			table := v.Table
			if k == "table" {
				table = v.Name
			}
			if v.Family != "inet" || table != "filter" && table != "nexus_v2" {
				return r, errors.New("unsupported firewall topology; manual review required")
			}
			owned := table == "nexus_v2"
			switch k {
			case "table":
				if owned {
					if v.Comment != r.token {
						return r, errors.New("existing nft table is not owned by this installation")
					}
					r.managed = true
				} else {
					r.filter = true
				}
			case "chain":
				if v.Type != "filter" || v.Hook != "input" || v.Name != "input" || v.Policy != "accept" && v.Policy != "drop" {
					return r, errors.New("unsupported nftables base chain")
				}
				if owned && v.Policy != "accept" {
					return r, errors.New("managed firewall policy changed")
				}
				if !owned {
					r.native = true
				}
			case "rule":
				sock, known := allowedPort(v)
				if v.Chain != "input" || !known && !basicAllow(v) {
					return r, errors.New("unrecognized firewall rule in input chain")
				}
				if owned || strings.HasPrefix(v.Comment, r.token+"-") {
					if !known || v.Comment != fmt.Sprintf("%s-%s-%d", r.token, sock.net, sock.port) || !slices.Contains(sockets(d), sock) {
						return r, errors.New("managed firewall rule was modified")
					}
				}
				r.entries = append(r.entries, v)
			default:
				return r, errors.New("unrecognized nftables ruleset object")
			}
		}
	}
	if r.filter && !r.native || r.managed && r.native {
		return r, errors.New("ambiguous nftables input chains")
	}
	return r, nil
}
func (h host) firewallPlan(ctx context.Context, d deployment, add bool) (string, error) {
	r, err := h.rules(ctx, d)
	if err != nil {
		return "", err
	}
	var script strings.Builder
	table := "filter"
	if !r.native {
		table = "nexus_v2"
	}
	if add && !r.native && !r.managed {
		fmt.Fprintf(&script, "create table inet nexus_v2 { comment \"%s\"; }\nadd chain inet nexus_v2 input { type filter hook input priority filter; policy accept; }\n", r.token)
	}
	for _, s := range sockets(d) {
		comment := fmt.Sprintf("%s-%s-%d", r.token, s.net, s.port)
		found := false
		for _, v := range r.entries {
			if v.Comment != comment {
				if add {
					if allowed, ok := allowedPort(v); ok && allowed == s {
						found = true
					}
				}
				continue
			}
			if v.Table != table || v.Handle <= 0 {
				return "", errors.New("managed firewall rule identity changed")
			}
			found = true
			if !add && !r.managed {
				fmt.Fprintf(&script, "delete rule inet %s input handle %d\n", table, v.Handle)
			}
		}
		if add && !found {
			fmt.Fprintf(&script, "insert rule inet %s input %s dport %d accept comment \"%s\"\n", table, s.net, s.port, comment)
		}
	}
	if !add && r.managed {
		script.WriteString("delete table inet nexus_v2\n")
	}
	return script.String(), nil
}

func (h host) firewall(ctx context.Context, d deployment, add bool) error {
	if e := h.checkFirewall(ctx, d); e != nil {
		return e
	}
	if d.Firewall == "ufw" {
		return h.applyUFW(ctx, d, add)
	}
	script, err := h.firewallPlan(ctx, d, add)
	if err != nil {
		return err
	}
	if script == "" {
		return nil
	}
	f, e := os.CreateTemp("", "nexus-firewall-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.WriteString(script)
	if e = errors.Join(e, f.Close()); e != nil {
		return e
	}
	_, e = h.command(ctx, "nft", "-f", f.Name())
	return e
}
