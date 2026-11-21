package server

import (
	"context"
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
	if e != nil || ip != netip.AddrPortFrom(good, 443).String() || calls != 1 {
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
