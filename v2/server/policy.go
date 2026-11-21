package server

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/shejiang000-prog/Nexus/v2/proof"
)

var denied = errors.New("destination denied")
var reserved = []netip.Prefix{}

func init() {
	for _, s := range strings.Fields("0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.168.0.0/16 192.0.0.0/24 192.0.2.0/24 192.88.99.0/24 198.18.0.0/15 198.51.100.0/24 203.0.113.0/24 224.0.0.0/4 240.0.0.0/4 168.63.129.16/32 2002::/16 2001::/23 2001:db8::/32 3fff::/20") {
		reserved = append(reserved, netip.MustParsePrefix(s))
	}
}
func public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, p := range reserved {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// Resolve once. A mixed public/private answer fails closed. Dial only the checked literal.
func target(ctx context.Context, authority string, allow map[netip.AddrPort]bool, lookup func(context.Context, string, string) ([]netip.Addr, error)) (string, error) {
	a, e := proof.Authority(authority)
	if e != nil {
		return "", denied
	}
	h, p, _ := net.SplitHostPort(a)
	port, _ := strconv.Atoi(p)
	var ips []netip.Addr
	if ip, e := netip.ParseAddr(h); e == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, e = lookup(ctx, "ip", h)
		if e != nil {
			return "", e
		}
	}
	if len(ips) == 0 {
		return "", denied
	}
	for _, ip := range ips {
		ap := netip.AddrPortFrom(ip.Unmap(), uint16(port))
		if !public(ip) && !allow[ap] {
			return "", denied
		}
	}
	return netip.AddrPortFrom(ips[0].Unmap(), uint16(port)).String(), nil
}
