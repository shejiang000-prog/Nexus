package server

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestDestination(t *testing.T) {
	for _, s := range []string{"127.255.255.255", "169.254.169.254", "168.63.129.16", "::ffff:10.0.0.1", "fc00::1", "fe80::1", "2001:db8::1", "100.127.255.255", "::", "ff02::1", "192.0.2.1"} {
		if public(netip.MustParseAddr(s)) {
			t.Error(s)
		}
	}
	allowed := netip.MustParseAddrPort("192.0.2.9:8443")
	allow := map[netip.AddrPort]bool{allowed: true}
	good := netip.AddrFrom4([4]byte{11, 0, 0, 1})
	calls := 0
	lookup := func(context.Context, string, string) ([]netip.Addr, error) { calls++; return []netip.Addr{good}, nil }
	ip, e := target(t.Context(), "site.test:443", nil, lookup)
	if e != nil || len(ip) != 1 || ip[0] != netip.AddrPortFrom(good, 443).String() || calls != 1 {
		t.Fatal("not pinned", e)
	}
	_, e = target(t.Context(), "mixed.test:443", nil, func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{good, allowed.Addr()}, nil
	})
	if e == nil {
		t.Fatal("mixed DNS")
	}
	if _, e = target(t.Context(), allowed.String(), allow, nil); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"192.0.2.9:8444", "[fe80::1%eth0]:443", "site.test:0", "invalid/name:80"} {
		if _, e := target(t.Context(), s, allow, nil); e == nil {
			t.Error(s)
		}
	}
}

func TestDialSecondValidatedAddress(t *testing.T) {
	allow := map[netip.AddrPort]bool{netip.MustParseAddrPort("[::1]:443"): true, netip.MustParseAddrPort("127.0.0.1:443"): true}
	addresses, e := target(t.Context(), "dual.test:443", allow, func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.IPv6Loopback(), netip.MustParseAddr("127.0.0.1")}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	c, e := dialTargets(t.Context(), "tcp", addresses, func(ctx context.Context, network, addr string) (net.Conn, error) {
		count++
		if addr == "[::1]:443" {
			return nil, errors.New("no route")
		}
		if addr != "127.0.0.1:443" {
			t.Fatal("unchecked target", addr)
		}
		c, p := net.Pipe()
		p.Close()
		return c, nil
	})
	if e != nil || count != 2 {
		t.Fatalf("count %d: %v", count, e)
	}
	c.Close()
}
