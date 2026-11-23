package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shejiang000-prog/Nexus/v2/compat"
	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/server"
)

var compatUsage = errors.New("usage: ledger DB compat {enable|disable|rotate} USER_HEX PROTOCOL | rotate-node-key reality | apply PROTOCOL | status USER_HEX | export USER_HEX {links|mihomo} [--root ABSOLUTE_ROOT]")

type compatDeployment struct {
	Domain             string         `json:"domain"`
	PublicHost         string         `json:"public_host"`
	RealityServerNames []string       `json:"reality_server_names"`
	Ports              map[string]int `json:"ports"`
}

func compatAdmin(args []string, out io.Writer) (bool, error) {
	if len(args) < 2 || args[1] != "compat" && args[1] != "revoke-user" {
		return false, nil
	}
	root := "/"
	if len(args) >= 4 && args[len(args)-2] == "--root" {
		root = args[len(args)-1]
		if !filepath.IsAbs(root) {
			return true, compatUsage
		}
		args = args[:len(args)-2]
	}
	ctx := context.Background()
	revoke := args[1] == "revoke-user"
	var user proof.ID
	action := "revoke-user"
	tail := args[2:]
	if !revoke {
		if len(tail) == 0 {
			return true, compatUsage
		}
		action, tail = tail[0], tail[1:]
	}
	want := 2
	switch action {
	case "revoke-user":
		if !revoke {
			return true, compatUsage
		}
		want = 1
	case "status", "apply", "rotate-node-key":
		want = 1
	case "enable", "disable", "rotate", "export":
	default:
		return true, compatUsage
	}
	if len(tail) != want {
		return true, compatUsage
	}
	if action != "apply" && action != "rotate-node-key" {
		if len(tail[0]) != 32 {
			return true, compatUsage
		}
		if _, err := hex.Decode(user[:], []byte(tail[0])); err != nil {
			return true, compatUsage
		}
	}
	protocol := tail[len(tail)-1]
	if action == "enable" || action == "disable" || action == "rotate" || action == "apply" {
		if protocol != "reality" && protocol != "hysteria2" {
			return true, compatUsage
		}
	}
	if action == "rotate-node-key" && protocol != "reality" {
		return true, compatUsage
	}
	if action == "export" && protocol != "links" && protocol != "mihomo" {
		return true, compatUsage
	}

	deployedDB := filepath.Join(root, "var/lib/nexus-v2/ledger.db")
	if !filepath.IsAbs(args[0]) {
		return true, errors.New("absolute ledger path required")
	}
	wantFile, e1 := os.Stat(deployedDB)
	inputFile, e2 := os.Stat(args[0])
	installed := e1 == nil && e2 == nil && os.SameFile(wantFile, inputFile)
	if !revoke && !installed {
		return true, errors.New("compatibility operations require the installed ledger")
	}
	dbPath := args[0]
	if installed {
		// Use a single SQLite WAL and apply lock namespace, including
		// when the caller named the installed file through an alias.
		dbPath = deployedDB
	}
	l, err := server.OpenLedger(dbPath)
	if err != nil {
		return true, err
	}
	defer l.Close()
	apply := func(p string, force bool) error {
		return l.ApplyCompat(ctx, p, force, func(s compat.Snapshot) error { return compat.Apply(ctx, root, s) })
	}
	switch action {
	case "revoke-user":
		if err := l.RevokeUser(ctx, user); err != nil {
			return true, err
		}
		status, err := l.CompatStatus(ctx, user)
		if err != nil {
			return true, err
		}
		var failed error
		for _, s := range status {
			if !s.Enabled && s.Result != "applied" {
				if !installed {
					return true, errors.New("local revocation recorded; service application requires the installed ledger")
				}
				failed = errors.Join(failed, apply(s.Protocol, false))
			}
		}
		return true, failed
	case "status":
		status, err := l.CompatStatus(ctx, user)
		if err != nil {
			return true, err
		}
		return true, json.NewEncoder(out).Encode(status)
	case "apply":
		return true, apply(protocol, true)
	}
	var cfg compatDeployment
	deployment, err := os.ReadFile(filepath.Join(root, "etc/nexus-v2/deployment.json"))
	if err != nil {
		return true, err
	}
	if err := json.Unmarshal(deployment, &cfg); err != nil {
		return true, errors.New("invalid compatibility deployment")
	}
	if action == "export" {
		creds, err := l.CompatExport(ctx, user)
		if err != nil {
			return true, err
		}
		return true, exportCompat(out, cfg, creds, protocol)
	}
	if port := cfg.Ports[protocol]; port < 1 || port > 65535 {
		return true, errors.New("protocol is not installed")
	}
	if action == "rotate-node-key" {
		if err := l.RotateRealityKey(ctx); err != nil {
			return true, err
		}
		return true, apply(protocol, false)
	}
	if err := l.ChangeCompat(ctx, user, protocol, action); err != nil {
		return true, err
	}
	return true, apply(protocol, false)
}

func exportCompat(out io.Writer, cfg compatDeployment, creds map[string]server.CompatShare, format string) error {
	var doc strings.Builder
	if format == "mihomo" {
		doc.WriteString("proxies:\n")
	}
	for _, p := range []string{"reality", "hysteria2"} {
		c, ok := creds[p]
		if !ok {
			continue
		}
		port := cfg.Ports[p]
		if port < 1 || port > 65535 {
			return errors.New("protocol is not installed")
		}
		endpoint := cfg.Domain
		if endpoint == "" {
			if p != "reality" || net.ParseIP(cfg.PublicHost) == nil {
				return errors.New("missing deployment endpoint")
			}
			endpoint = cfg.PublicHost
		}
		host := net.JoinHostPort(endpoint, strconv.Itoa(port))
		name := "Nexus " + p
		u := url.URL{Scheme: p, Host: host, Fragment: name}
		sni := cfg.Domain
		q := url.Values{}
		if p == "reality" {
			if len(cfg.RealityServerNames) == 0 || cfg.RealityServerNames[0] == "" {
				return errors.New("missing Reality server name")
			}
			sni = cfg.RealityServerNames[0]
			u.Scheme = "vless"
			u.User = url.User(c.UUID)
			q = url.Values{"encryption": {"none"}, "flow": {"xtls-rprx-vision"}, "security": {"reality"}, "type": {"tcp"}, "fp": {"chrome"}, "pbk": {c.PublicKey}, "sid": {c.ShortID}}
		} else {
			u.User = url.UserPassword(c.UserID, c.Password)
		}
		q.Set("sni", sni)
		u.RawQuery = q.Encode()
		if format == "links" {
			fmt.Fprintln(&doc, u.String())
			continue
		}
		fmt.Fprintf(&doc, "  - name: %q\n    type: %s\n    server: %q\n    port: %d\n    udp: true\n", name, u.Scheme, endpoint, port)
		if p == "reality" {
			fmt.Fprintf(&doc, "    network: tcp\n    tls: true\n    uuid: %q\n    flow: xtls-rprx-vision\n    servername: %q\n    client-fingerprint: chrome\n    reality-opts:\n      public-key: %q\n      short-id: %q\n", c.UUID, sni, c.PublicKey, c.ShortID)
		} else {
			fmt.Fprintf(&doc, "    password: %q\n    sni: %q\n    skip-cert-verify: false\n", c.UserID+":"+c.Password, sni)
		}
	}
	_, err := io.WriteString(out, doc.String())
	return err
}
