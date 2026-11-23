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
	if o.Domain == "" || len(o.Domain) > 253 || net.ParseIP(o.Domain) != nil || !strings.Contains(o.Domain, ".") {
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
	Domain        string
	RealityTarget string
	Ports         map[string]int
	Token         string
	UID, GID      int
	ConfigFiles   []string
	Installed     bool
}

func (d deployment) units() []string {
	names := []string{"nexus-v2-firewall.service"}
	for _, p := range protocols {
		if _, ok := d.Ports[p]; ok {
			names = append(names, unitName(p))
		}
	}
	return names
}
func unitText(name string) string {
	if name == "nexus-v2-firewall.service" {
		return "[Unit]\nDescription=Nexus V2 ingress rules\nAfter=nftables.service\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=" + base + "/bin/nexus-node firewall-apply\n\n[Install]\nWantedBy=multi-user.target\n"
	}
	cmd := "run --config " + cfg + "/v2.json"
	if name != unitName("v2") {
		cmd = "compat-serve " + strings.TrimSuffix(strings.TrimPrefix(name, "nexus-v2-"), ".service")
	}
	return "[Unit]\nDescription=Nexus private node " + name + "\nRequires=nexus-v2-firewall.service\nAfter=network-online.target nexus-v2-firewall.service\n\n[Service]\nUser=" + account + "\nGroup=" + account + "\nUMask=0077\nWorkingDirectory=" + state + "\nExecStart=" + base + "/bin/nexus-node " + cmd + "\nAmbientCapabilities=CAP_NET_BIND_SERVICE\nCapabilityBoundingSet=CAP_NET_BIND_SERVICE\nNoNewPrivileges=yes\nProtectSystem=strict\nReadWritePaths=" + state + "\nRestart=on-failure\n\n[Install]\nWantedBy=multi-user.target\n"
}

type runner func(context.Context, string, ...string) ([]byte, error)
type host struct {
	root    string
	command runner
	probe   func(context.Context, deployment, string) error
	out     io.Writer
}

func (h host) path(p string) string { return filepath.Join(h.root, p) }
