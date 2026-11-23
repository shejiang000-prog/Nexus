package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/compat"
	"github.com/shejiang000-prog/Nexus/v2/internal/installer"
	"github.com/shejiang000-prog/Nexus/v2/proof"
)

func platformCommand(args []string, input io.Reader, output io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	action := args[0]
	switch action {
	case "install", "upgrade", "status", "uninstall", "compat-serve", "firewall-apply":
	default:
		return false, nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if action == "compat-serve" {
		if len(args) != 2 || (args[1] != "reality" && args[1] != "hysteria2") {
			return true, errors.New("usage: compat-serve reality|hysteria2")
		}
		return true, compat.Serve(ctx, "/", args[1])
	}
	opts, err := deploymentOptions(action, args[1:], input, output)
	if err != nil {
		return true, err
	}
	err = installer.Run(ctx, action, opts, renderDeployment, output)
	if err == nil && action == "install" {
		fmt.Fprintln(output, "Manage private users as the service account:")
		fmt.Fprintln(output, "  runuser -u nexus-v2 -- /opt/nexus-v2/bin/nexus-node ledger /var/lib/nexus-v2/ledger.db create-user")
	}
	return true, err
}

func deploymentOptions(action string, args []string, input io.Reader, out io.Writer) (installer.Options, error) {
	var o installer.Options
	f := flag.NewFlagSet(action, flag.ContinueOnError)
	f.SetOutput(out)
	var interactive, unattended bool
	var protocols string
	var ports = map[string]*int{}
	if action == "install" {
		f.BoolVar(&interactive, "interactive", false, "prompt for installation settings")
		f.BoolVar(&unattended, "non-interactive", false, "use flags only")
		f.StringVar(&protocols, "protocols", "v2", "comma-separated v2,reality,hysteria2")
		f.StringVar(&o.Domain, "domain", "", "DNS name for clients")
		f.StringVar(&o.CertFile, "cert", "", "absolute PEM certificate-chain file")
		f.StringVar(&o.KeyFile, "key", "", "absolute PEM private-key file")
		f.StringVar(&o.RealityTarget, "reality-target", "", "operator-controlled TLS target host:port serving *.<domain>")
		for _, name := range []string{"v2", "reality", "hysteria2"} {
			ports[name] = f.Int(name+"-port", 0, "public port (0 for automatic assignment)")
		}
	}
	if action == "install" || action == "upgrade" {
		f.StringVar(&o.AssetDir, "asset-dir", "", "verified upstream release cache (NexusLab)")
	}
	if action == "uninstall" {
		f.BoolVar(&o.Purge, "purge", false, "remove managed ledger and backups")
	}
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 {
		return o, fmt.Errorf("unexpected positional argument for %s", action)
	}
	if action != "install" {
		return o, nil
	}
	if interactive && unattended {
		return o, errors.New("choose one input mode")
	}
	if interactive || (!unattended && len(args) == 0) {
		r := bufio.NewReader(input)
		ask := func(label string, dest *string) error {
			if _, err := fmt.Fprintf(out, "%s [%s]: ", label, *dest); err != nil {
				return err
			}
			line, err := r.ReadString('\n')
			if err != nil {
				return fmt.Errorf("reading %s: %w", label, err)
			}
			if line = strings.TrimSpace(line); line != "" {
				*dest = line
			}
			return nil
		}
		if err := ask("Protocols (v2,reality,hysteria2)", &protocols); err != nil {
			return o, err
		}
		if err := ask("Domain", &o.Domain); err != nil {
			return o, err
		}
		o.Ports = make(map[string]int)
		for _, p := range strings.Split(protocols, ",") {
			name := strings.TrimSpace(p)
			dest, ok := ports[name]
			if !ok {
				return o, errors.New("unknown protocol")
			}
			s := strconv.Itoa(*dest)
			if err := ask(name+" port (0=auto)", &s); err != nil {
				return o, err
			}
			n, err := strconv.Atoi(s)
			if err != nil {
				return o, errors.New("invalid port")
			}
			*dest = n
			o.Ports[name] = n
		}
		if _, ok := o.Ports["reality"]; ok {
			if err := ask("Wildcard HTTPS target host:port", &o.RealityTarget); err != nil {
				return o, err
			}
		}
		_, v := o.Ports["v2"]
		_, h := o.Ports["hysteria2"]
		if v || h {
			if err := ask("Certificate PEM", &o.CertFile); err != nil {
				return o, err
			}
			if err := ask("Private key PEM", &o.KeyFile); err != nil {
				return o, err
			}
		}
	}
	o.Ports = make(map[string]int)
	for _, raw := range strings.Split(protocols, ",") {
		name := strings.TrimSpace(raw)
		p, ok := ports[name]
		if !ok {
			return o, errors.New("unknown protocol")
		}
		if _, dup := o.Ports[name]; dup {
			return o, errors.New("duplicate protocol")
		}
		if *p < 0 || *p > 65535 {
			return o, fmt.Errorf("invalid port for %s", name)
		}
		o.Ports[name] = *p
	}
	for name, p := range ports {
		if _, ok := o.Ports[name]; !ok && *p != 0 {
			return o, fmt.Errorf("port for unselected protocol %s", name)
		}
	}
	o.Domain = strings.ToLower(o.Domain)
	if _, err := proof.Authority(net.JoinHostPort(o.Domain, "443")); err != nil || net.ParseIP(o.Domain) != nil || !strings.Contains(o.Domain, ".") {
		return o, errors.New("a DNS domain is required")
	}
	if _, ok := o.Ports["reality"]; ok {
		if len(o.Domain)+33 > 253 {
			return o, errors.New("domain too long for per-user Reality SNI")
		}
		if _, err := proof.Authority(o.RealityTarget); err != nil {
			return o, errors.New("provide --reality-target host:port")
		}
	}
	_, v := o.Ports["v2"]
	_, h := o.Ports["hysteria2"]
	if (v || h) && (!filepath.IsAbs(o.CertFile) || !filepath.IsAbs(o.KeyFile)) {
		return o, errors.New("provide absolute --cert and --key paths")
	}
	return o, nil
}

// Paths in the generated configurations refer to their installed locations.
func renderDeployment(ctx context.Context, o installer.Options, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	write := func(name string, b []byte) error { return os.WriteFile(filepath.Join(dir, name), b, 0600) }
	type object = map[string]any
	writeJSON := func(name string, v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		return write(name, append(b, '\n'))
	}
	_, v := o.Ports["v2"]
	_, h := o.Ports["hysteria2"]
	if v || h {
		cert, err := os.ReadFile(o.CertFile)
		if err != nil {
			return err
		}
		key, err := os.ReadFile(o.KeyFile)
		if err != nil {
			return err
		}
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return errors.New("invalid certificate/key pair")
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return err
		}
		if err = leaf.VerifyHostname(o.Domain); err != nil {
			return err
		}
		now := time.Now()
		if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
			return errors.New("certificate outside validity period")
		}
		if len(leaf.ExtKeyUsage) > 0 {
			valid := false
			for _, usage := range leaf.ExtKeyUsage {
				if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
					valid = true
				}
			}
			if !valid {
				return errors.New("certificate does not permit TLS server authentication")
			}
		}
		if err = write("cert.pem", cert); err != nil {
			return err
		}
		if err = write("key.pem", key); err != nil {
			return err
		}
	}
	if err := writeJSON("deployment.json", object{"domain": o.Domain, "ports": o.Ports, "reality_target": o.RealityTarget}); err != nil {
		return err
	}
	if !v {
		return nil
	}
	port := strconv.Itoa(o.Ports["v2"])
	handler := object{"handler": "nexus", "authority": net.JoinHostPort(o.Domain, port), "ledger_path": "/var/lib/nexus-v2/ledger.db"}
	site := object{"handler": "static_response", "headers": object{"Content-Type": []string{"text/plain; charset=utf-8"}}, "body": "Welcome.\n"}
	srv := object{"listen": []string{":" + port}, "enable_datagrams": true, "automatic_https": object{"disable": true}, "tls_connection_policies": []object{{"protocol_min": "tls1.3"}}, "routes": []object{{"handle": []object{handler}}, {"match": []object{{"path": []string{"/"}, "method": []string{"HEAD", "GET"}}}, "handle": []object{site}}, {"handle": []object{{"handler": "static_response", "status_code": 404}}}}}
	return writeJSON("v2.json", object{"admin": object{"listen": "unix//var/lib/nexus-v2/admin.sock"}, "apps": object{"tls": object{"certificates": object{"load_files": []object{{"certificate": "/etc/nexus-v2/cert.pem", "key": "/etc/nexus-v2/key.pem"}}}}, "http": object{"servers": object{"node": srv}}}})
}
