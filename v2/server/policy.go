package server

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

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
func target(ctx context.Context, authority string, allow map[netip.AddrPort]bool, lookup func(context.Context, string, string) ([]netip.Addr, error)) ([]string, error) {
	a, e := proof.Authority(authority)
	if e != nil {
		return nil, denied
	}
	h, p, _ := net.SplitHostPort(a)
	port, _ := strconv.Atoi(p)
	var ips []netip.Addr
	if ip, e := netip.ParseAddr(h); e == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, e = lookup(ctx, "ip", h)
		if e != nil {
			return nil, e
		}
	}
	if len(ips) == 0 {
		return nil, denied
	}
	addrs := make([]string, 0, len(ips))
	for _, ip := range ips {
		ap := netip.AddrPortFrom(ip.Unmap(), uint16(port))
		addrs = append(addrs, ap.String())
		if !public(ip) && !allow[ap] {
			return nil, denied
		}
	}
	return addrs, nil
}

func dialTargets(ctx context.Context, network string, addrs []string, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	var last error
	for i, addr := range addrs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		attempt := ctx
		cancel := func() {}
		if end, ok := ctx.Deadline(); ok && len(addrs)-i > 1 {
			attempt, cancel = context.WithTimeout(ctx, time.Until(end)/time.Duration(len(addrs)-i))
		}
		conn, e := dial(attempt, network, addr)
		cancel()
		if e == nil {
			return conn, nil
		}
		last = e
	}
	return nil, last
}
