package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type socket struct {
	net  string
	port int
}

func sockets(d deployment) []socket {
	var out []socket
	for _, p := range protocols {
		port, ok := d.Ports[p]
		if !ok {
			continue
		}
		if p != "hysteria2" {
			out = append(out, socket{"tcp", port})
		}
		if p != "reality" {
			out = append(out, socket{"udp", port})
		}
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

func (h host) rules(ctx context.Context, d deployment) (ruleset, error) {
	r := ruleset{token: "nexus-v2-" + d.Token}
	b, e := h.command(ctx, "nft", "-j", "list", "ruleset")
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
				if !owned {
					r.native = true
				}
			case "rule":
				if v.Chain != "input" || owned && !strings.HasPrefix(v.Comment, r.token+"-") {
					return r, errors.New("unrecognized firewall rule in managed chain")
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
func (h host) firewall(ctx context.Context, d deployment, add bool) error {
	r, err := h.rules(ctx, d)
	if err != nil {
		return err
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
				continue
			}
			if v.Table != table || v.Handle <= 0 {
				return errors.New("managed firewall rule identity changed")
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
	if script.Len() == 0 {
		return nil
	}
	f, e := os.CreateTemp("", "nexus-firewall-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.WriteString(script.String())
	if e = errors.Join(e, f.Close()); e != nil {
		return e
	}
	_, e = h.command(ctx, "nft", "-f", f.Name())
	return e
}
