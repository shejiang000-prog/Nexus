package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
)

type Options struct {
	Domain, RealityTarget, CertFile, KeyFile, NodePath, AssetDir string
	PublicHost, ACMECA, ACMERoot                                 string
	RealityServerNames                                           []string
	Ports                                                        map[string]int
	Purge                                                        bool
}
type RenderFunc func(context.Context, Options, string) error

const (
	base    = "/opt/nexus-v2"
	cfg     = "/etc/nexus-v2"
	state   = "/var/lib/nexus-v2"
	unitDir = "/etc/systemd/system"
	account = "nexus-v2"
)

var protocols = []string{"v2", "reality", "hysteria2"}

func normalize(o Options) (Options, error) {
	o.Domain = strings.TrimSuffix(strings.ToLower(o.Domain), ".")
	if len(o.Ports) == 0 {
		o.Ports = map[string]int{"v2": 0}
	}
	ports := map[string]int{}
	claimed := map[int]bool{}
	for p, n := range o.Ports {
		if p != "v2" && p != "reality" && p != "hysteria2" || n < 0 || n > 65535 || n != 0 && claimed[n] {
			return o, fmt.Errorf("unknown protocol or invalid/conflicting port: %s %d", p, n)
		}
		if len(o.Ports) > 1 && (p != "v2" && n == 443 || p == "v2" && n != 443 && n != 0) {
			return o, errors.New("multiple protocols reserve 443 for V2")
		}
		if n != 0 {
			claimed[n] = true
		}
		ports[p] = n
	}
	for _, p := range protocols {
		n, ok := ports[p]
		if !ok || n != 0 {
			continue
		}
		n = 443
		if len(ports) > 1 && p != "v2" {
			n = 8443
			if p == "hysteria2" {
				n = 9443
			}
			for claimed[n] {
				n++
			}
		}
		ports[p] = n
		claimed[n] = true
	}
	if o.Domain == "" {
		ip := net.ParseIP(o.PublicHost)
		if len(ports) != 1 || ports["reality"] == 0 || ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			return o, errors.New("a domain is required, except for Reality alone with a node IP")
		}
		o.PublicHost = ip.String()
	} else {
		if o.PublicHost != "" {
			return o, errors.New("public-host is only valid without a domain")
		}
		if len(o.Domain) > 253 || net.ParseIP(o.Domain) != nil || !strings.Contains(o.Domain, ".") {
			return o, errors.New("a DNS domain name is required")
		}
		for _, label := range strings.Split(o.Domain, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return o, errors.New("invalid DNS domain")
			}
			for _, r := range label {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
					return o, errors.New("invalid DNS domain")
				}
			}
		}
	}
	if (o.CertFile == "") != (o.KeyFile == "") {
		return o, errors.New("certificate and private key must be supplied together")
	}
	if o.CertFile != "" && (o.ACMERoot != "" || o.ACMECA != "") {
		return o, errors.New("manual certificates and ACME settings conflict")
	}
	if ports["v2"] == 0 && ports["hysteria2"] == 0 && (o.CertFile != "" || o.ACMERoot != "" || o.ACMECA != "") {
		return o, errors.New("certificate options require V2 or Hysteria2")
	}
	if (ports["v2"] != 0 || ports["hysteria2"] != 0) && o.CertFile == "" && (ports["reality"] == 80 || ports["reality"] == 443 || ports["v2"] == 80) {
		return o, errors.New("ACME needs 80/tcp and 443/tcp for Caddy")
	}
	o.Ports = ports
	return o, nil
}

func unitName(p string) string {
	if p == "v2" {
		return "nexus-v2.service"
	}
	return "nexus-v2-" + p + ".service"
}

type deployment struct {
	Domain                       string
	RealityTarget                string
	Ports                        map[string]int
	Layout                       int
	CertMode                     string
	PublicHost, ACMECA, ACMERoot string
	CertFile, KeyFile            string
	RealityServerNames           []string
	Firewall                     string
	Token                        string
	UID, GID                     int
	ConfigFiles                  []string
	Installed                    bool
}

func (o Options) deployment() deployment {
	mode := "none"
	if o.Ports["v2"] != 0 || o.Ports["hysteria2"] != 0 {
		mode = "acme"
		if o.CertFile != "" {
			mode = "manual"
		}
	}
	return deployment{Layout: 2, CertMode: mode, Domain: o.Domain, RealityTarget: o.RealityTarget, Ports: o.Ports,
		PublicHost: o.PublicHost, RealityServerNames: o.RealityServerNames, ACMECA: o.ACMECA, ACMERoot: o.ACMERoot, CertFile: o.CertFile, KeyFile: o.KeyFile}
}
func (d deployment) uses(p string) bool {
	return d.Ports[p] != 0 || p == "v2" && d.Layout == 2 && d.Ports["hysteria2"] != 0
}
func (d deployment) units() []string {
	names := []string{"nexus-v2-firewall.service"}
	for _, p := range protocols {
		if d.uses(p) {
			names = append(names, unitName(p))
		}
	}
	return names
}
func (d deployment) unitText(name string) string {
	if name == "nexus-v2-firewall.service" {
		after := "nftables.service"
		if d.Firewall == "ufw" {
			after = "ufw.service"
		}
		return "[Unit]\nDescription=Nexus V2 ingress rules\nAfter=" + after + "\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=" + base + "/bin/nexus-node firewall-apply\n\n[Install]\nWantedBy=multi-user.target\n"
	}
	cmd := "run --config " + cfg + "/v2.json"
	if name != unitName("v2") {
		cmd = "compat-serve " + strings.TrimSuffix(strings.TrimPrefix(name, "nexus-v2-"), ".service")
	}
	required := "nexus-v2-firewall.service"
	if d.Layout == 2 && name == unitName("hysteria2") {
		required += " nexus-v2.service"
	}
	return "[Unit]\nDescription=Nexus private node " + name + "\nRequires=" + required + "\nAfter=network-online.target " + required + "\n\n[Service]\nUser=" + account + "\nGroup=" + account + "\nUMask=0077\nWorkingDirectory=" + state + "\nExecStart=" + base + "/bin/nexus-node " + cmd + "\nAmbientCapabilities=CAP_NET_BIND_SERVICE\nCapabilityBoundingSet=CAP_NET_BIND_SERVICE\nNoNewPrivileges=yes\nProtectSystem=strict\nReadWritePaths=" + state + "\nRestart=on-failure\n\n[Install]\nWantedBy=multi-user.target\n"
}

type runner func(context.Context, string, ...string) ([]byte, error)
type host struct {
	root     string
	command  runner
	probe    func(context.Context, deployment, string) error
	dnsCheck func(context.Context, string) error
	out      io.Writer
}

func (h host) path(p string) string { return filepath.Join(h.root, p) }
