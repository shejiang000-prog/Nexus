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
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/caddyserver/caddy/v2/modules/filestorage"
	"github.com/caddyserver/certmagic"
	"github.com/shejiang000-prog/Nexus/v2/compat"
	"github.com/shejiang000-prog/Nexus/v2/internal/installer"
	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/server"
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
		protocol := args[1]
		return true, compat.Serve(ctx, "/", protocol, func(ctx context.Context) (compat.Snapshot, error) {
			const path = "/var/lib/nexus-v2/ledger.db"
			if _, err := os.Stat(path); err != nil {
				return compat.Snapshot{}, err
			}
			l, err := server.OpenLedger(path)
			if err != nil {
				return compat.Snapshot{}, err
			}
			defer l.Close()
			return l.CompatSnapshot(ctx, protocol)
		})
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
	var protocols, names string
	var ask func(string, *string) error
	var ports = map[string]*int{}
	if action == "install" {
		f.BoolVar(&interactive, "interactive", false, "prompt for installation settings")
		f.BoolVar(&unattended, "non-interactive", false, "use flags only")
		f.BoolVar(&o.SkipDNSCheck, "skip-dns-check", false, "continue if the domain HTTP callback fails; ACME issuance is still required")
		f.StringVar(&protocols, "protocols", "", "comma-separated v2,reality,hysteria2")
		f.StringVar(&o.Domain, "domain", "", "node DNS name (optional for V2 and Reality; required for Hysteria2)")
		f.StringVar(&o.PublicHost, "public-host", "", "node IP for a domainless V2 / Reality installation")
		f.StringVar(&names, "reality-server-names", "", "comma-separated target certificate DNS names (default: target hostname)")
		f.StringVar(&o.ACMECA, "acme-ca", "", "HTTPS ACME directory (default: Let's Encrypt)")
		f.StringVar(&o.ACMERoot, "acme-root", "", "absolute PEM trust bundle for a private ACME authority")
		f.StringVar(&o.CertFile, "cert", "", "absolute PEM certificate-chain file")
		f.StringVar(&o.KeyFile, "key", "", "absolute PEM private-key file")
		f.StringVar(&o.RealityTarget, "reality-target", "", realityTargetHint+" (default: www.microsoft.com:443)")
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
	defaults := func() {
		if protocols == "" {
			protocols = "v2"
			if o.Domain == "" {
				protocols = "v2,reality"
			}
		}
	}
	if interactive || (!unattended && len(args) == 0) {
		r := bufio.NewReader(input)
		ask = func(label string, dest *string) error {
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
		o.ConfirmDNSFailure = func() (bool, error) {
			answer := "no"
			if err := ask("Continue with ACME despite the failed callback? (yes/no)", &answer); err != nil {
				return false, err
			}
			return strings.EqualFold(answer, "yes") || strings.EqualFold(answer, "y"), nil
		}
		if err := ask("Domain (blank for IP mode; Hysteria2 requires a domain)", &o.Domain); err != nil {
			return o, err
		}
		defaults()
		if err := ask("Protocols (v2,reality,hysteria2)", &protocols); err != nil {
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
		if o.Domain == "" {
			if _, selected := o.Ports["hysteria2"]; selected {
				return o, errors.New("Hysteria2 requires a domain; IP mode supports V2 and Reality")
			}
			if err := ask("Node IP for V2 and Reality", &o.PublicHost); err != nil {
				return o, err
			}
		}
		if _, ok := o.Ports["reality"]; ok {
			if o.RealityTarget == "" {
				o.RealityTarget = defaultRealityTarget
			}
			if err := ask(realityTargetHint, &o.RealityTarget); err != nil {
				return o, err
			}
			if err := ask("Reality server names (default: target hostname)", &names); err != nil {
				return o, err
			}
		}
		_, v := o.Ports["v2"]
		_, h := o.Ports["hysteria2"]
		if (v || h) && o.Domain != "" {
			if err := ask("Certificate PEM (blank for automatic ACME)", &o.CertFile); err != nil {
				return o, err
			}
			if o.CertFile != "" {
				if err := ask("Private key PEM", &o.KeyFile); err != nil {
					return o, err
				}
			}
		}
	}
	defaults()
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
	_, v := o.Ports["v2"]
	_, h := o.Ports["hysteria2"]
	if o.Domain == "" {
		if h {
			return o, errors.New("Hysteria2 requires a domain; IP mode supports V2 and Reality")
		}
		ip := net.ParseIP(o.PublicHost)
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			return o, errors.New("provide the node IP with --public-host")
		}
		o.PublicHost = ip.String()
		if o.CertFile != "" || o.KeyFile != "" || o.ACMECA != "" || o.ACMERoot != "" {
			return o, errors.New("IP mode uses its own persistent TLS identity")
		}
	} else if !dnsName(o.Domain) || o.PublicHost != "" {
		return o, errors.New("use --domain with a DNS name or --public-host with a node IP")
	}
	if (o.CertFile == "") != (o.KeyFile == "") || o.CertFile != "" && (!filepath.IsAbs(o.CertFile) || !filepath.IsAbs(o.KeyFile)) {
		return o, errors.New("manual certificates require both absolute --cert and --key")
	}
	if o.CertFile != "" && (o.ACMECA != "" || o.ACMERoot != "") {
		return o, errors.New("choose ACME or manual PEM files")
	}
	if !v && !h && (o.CertFile != "" || o.ACMECA != "" || o.ACMERoot != "") {
		return o, errors.New("Reality alone does not use local certificate options")
	}
	if o.ACMECA != "" {
		u, e := url.Parse(o.ACMECA)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return o, errors.New("invalid HTTPS ACME directory")
		}
	}
	if o.ACMERoot != "" && (!filepath.IsAbs(o.ACMERoot) || o.ACMECA == "") {
		return o, errors.New("--acme-root requires an absolute PEM path and a custom ACME directory")
	}
	if _, ok := o.Ports["reality"]; ok {
		if ask == nil {
			fmt.Fprintln(out, realityTargetHint)
		}
		if o.RealityTarget == "" {
			o.RealityTarget = defaultRealityTarget
		}
		for {
			var err error
			o.RealityServerNames, err = checkReality(o.RealityTarget, names, nil)
			if err == nil {
				break
			}
			if ask == nil {
				return o, err
			}
			fmt.Fprintln(out, err)
			if err := ask("Choose another TLS target", &o.RealityTarget); err != nil {
				return o, err
			}
			if err := ask("Target certificate DNS names", &names); err != nil {
				return o, err
			}
		}
	} else if o.RealityTarget != "" || names != "" {
		return o, errors.New("Reality options require selecting reality")
	}
	return o, nil
}

const realityTargetHint = "Reality TLS target (host:port): prefer a site on the same network or in the same region as the VPS, supporting TLS 1.3 and X25519"
const defaultRealityTarget = "www.microsoft.com:443"

func dnsName(name string) bool {
	_, err := proof.Authority(net.JoinHostPort(name, "443"))
	return err == nil && net.ParseIP(name) == nil && strings.Contains(name, ".")
}

func checkReality(target, names string, roots *x509.CertPool) ([]string, error) {
	if _, err := proof.Authority(target); err != nil {
		return nil, errors.New("Reality target must be host:port")
	}
	if names == "" {
		names, _, _ = net.SplitHostPort(target)
	}
	var result []string
	for _, part := range strings.Split(names, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if !dnsName(name) {
			return nil, errors.New("Reality server name must be a certificate DNS name")
		}
		d := tls.Dialer{Config: &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, CurvePreferences: []tls.CurveID{tls.X25519}, ServerName: name, RootCAs: roots}}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		conn, err := d.DialContext(ctx, "tcp", target)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("Reality target %s with SNI %s failed TLS 1.3/X25519 certificate check: %w", target, name, err)
		}
		conn.Close()
		result = append(result, name)
	}
	return result, nil
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
	if (v || h) && o.CertFile != "" {
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
	managed := (v || h) && o.Domain != "" && o.CertFile == ""
	host := o.Domain
	if host == "" {
		host = o.PublicHost
	}
	store := "/var/lib/nexus-v2/caddy"
	chain, key := "/etc/nexus-v2/cert.pem", "/etc/nexus-v2/key.pem"
	tlsApp := object{"certificates": object{"load_files": []object{{"certificate": chain, "key": key}}}}
	if v && o.Domain == "" {
		chain, key = "/var/lib/nexus-v2/ip-cert.pem", "/var/lib/nexus-v2/ip-key.pem"
		tlsApp = object{"automation": object{"policies": []object{{"get_certificate": []object{{"via": "nexus_ip", "ip": host, "directory": "/var/lib/nexus-v2"}}}}}}
	}
	if managed {
		ca := o.ACMECA
		if ca == "" {
			ca = certmagic.LetsEncryptProductionCA
		}
		issuer := object{"module": "acme", "ca": ca}
		if o.ACMERoot != "" {
			b, err := os.ReadFile(o.ACMERoot)
			if err != nil {
				return err
			}
			if !x509.NewCertPool().AppendCertsFromPEM(b) {
				return errors.New("invalid private ACME root bundle")
			}
			if err := write("acme-root.pem", b); err != nil {
				return err
			}
			issuer["trusted_roots_pem_files"] = []string{"/etc/nexus-v2/acme-root.pem"}
		}
		id := (&certmagic.ACMEIssuer{CA: ca}).IssuerKey()
		chain = filepath.Join(store, certmagic.StorageKeys.SiteCert(id, o.Domain))
		key = filepath.Join(store, certmagic.StorageKeys.SitePrivateKey(id, o.Domain))
		tlsApp = object{"certificates": object{"automate": []string{o.Domain}}, "automation": object{"policies": []object{{"subjects": []string{o.Domain}, "issuers": []object{issuer}}}}}
	}
	siteSock := "/var/lib/nexus-v2/site.sock"
	if err := writeJSON("deployment.json", object{"domain": o.Domain, "public_host": o.PublicHost, "ports": o.Ports, "reality_target": o.RealityTarget, "reality_server_names": o.RealityServerNames, "certificate": chain, "key_file": key}); err != nil {
		return err
	}
	if !v && !h {
		return nil
	}
	site := object{"handler": "static_response", "headers": object{"Content-Type": []string{"text/plain; charset=utf-8"}}, "body": "Welcome.\n"}
	routes := []object{
		{"match": []object{{"path": []string{"/"}, "method": []string{"HEAD", "GET"}}}, "handle": []object{site}},
		{"handle": []object{{"handler": "static_response", "status_code": 404}}},
	}
	servers := object{}
	public := func(port int, siteRoutes []object) object {
		return object{"listen": []string{":" + strconv.Itoa(port)}, "protocols": []string{"h1", "h2"}, "automatic_https": object{"disable_redirects": true}, "tls_connection_policies": []object{{"protocol_min": "tls1.3"}}, "routes": siteRoutes}
	}
	if v {
		port := o.Ports["v2"]
		handler := object{"handler": "nexus", "authority": net.JoinHostPort(host, strconv.Itoa(port)), "ledger_path": "/var/lib/nexus-v2/ledger.db"}
		node := public(port, append([]object{{"handle": []object{handler}}}, routes...))
		node["protocols"] = []string{"h1", "h2", "h3"}
		node["enable_datagrams"] = true
		if o.Domain == "" {
			node["automatic_https"] = object{"disable": true}
			node["tls_connection_policies"] = []object{{"protocol_min": "tls1.3", "default_sni": host}}
		}
		servers["node"] = node
	}
	if h {
		servers["site"] = object{"listen": []string{"unix/" + siteSock + "|0600"}, "protocols": []string{"h1"}, "automatic_https": object{"disable": true}, "routes": routes}
	}
	if managed {
		servers["validation"] = object{"listen": []string{":80"}, "protocols": []string{"h1"}, "automatic_https": object{"disable_redirects": true}, "routes": routes}
		if o.Ports["v2"] != 443 {
			servers["https"] = public(443, routes)
		}
	}
	return writeJSON("v2.json", object{"storage": object{"module": "file_system", "root": store}, "admin": object{"listen": "unix//var/lib/nexus-v2/admin.sock"}, "apps": object{"tls": tlsApp, "http": object{"servers": servers}}})
}
