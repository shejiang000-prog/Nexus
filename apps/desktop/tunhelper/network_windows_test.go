package main

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

func TestCompetingRoutes(t *testing.T) {
	const own, wifi, vpn winipcfg.LUID = 5, 10, 20
	row := func(id winipcfg.LUID, cidr, hop string, metric uint32) winipcfg.MibIPforwardRow2 {
		r := winipcfg.MibIPforwardRow2{InterfaceLUID: id, Metric: metric}
		if err := r.DestinationPrefix.SetPrefix(netip.MustParsePrefix(cidr)); err != nil {
			t.Fatal(err)
		}
		if err := r.NextHop.SetAddr(netip.MustParseAddr(hop)); err != nil {
			t.Fatal(err)
		}
		return r
	}
	peers := map[winipcfg.LUID]peerInterface{
		wifi: {up: true, physical: true, metric4: 25, metric6: 20, subnets: []netip.Prefix{netip.MustParsePrefix("2001:db8:d::/64")}},
		vpn:  {up: true, metric4: 5, metric6: 1},
	}
	plan, _ := changesFor("full", nil)
	base := []winipcfg.MibIPforwardRow2{
		row(wifi, "0.0.0.0/0", "192.168.1.1", 10), row(wifi, "::/0", "fe80::1", 0),
		row(vpn, "0.0.0.0/0", "10.0.1.1", 40), row(vpn, "172.28.0.0/20", "0.0.0.0", 0),
		row(wifi, "2001:db8:d::/64", "::", 0), row(wifi, "ff00::/8", "::", 0),
	}
	if err := checkRoutes(own, plan, nil, base, peers); err != nil {
		t.Fatal("ordinary upstream and LAN", err)
	}
	if err := checkRoutes(own, plan, nil, nil, peers); !errors.Is(err, noPhysicalDefault) {
		t.Fatal("temporary loss of the upstream during a handover", err)
	}
	for _, other := range []winipcfg.MibIPforwardRow2{
		row(vpn, "0.0.0.0/0", "10.0.1.1", 30), row(vpn, "::/0", "fe80::2", 0),
		row(vpn, "0.0.0.0/2", "0.0.0.0", 0), row(vpn, "2000::/3", "::", 0),
		row(wifi, "8.8.8.8/32", "192.168.1.1", 0),
	} {
		if err := checkRoutes(own, plan, nil, append(slices.Clone(base), other), peers); !errors.Is(err, routeConflict) {
			t.Fatal("competing route accepted", other.DestinationPrefix.Prefix(), err)
		}
	}
	testPlan, _ := changesFor("test", nil)
	if err := checkRoutes(own, testPlan, nil, base, nil); err != nil {
		t.Fatal("test mode changed unrelated traffic", err)
	}
	if err := checkRoutes(own, testPlan, nil, []winipcfg.MibIPforwardRow2{row(vpn, "198.51.100.128/25", "0.0.0.0", 0)}, nil); !errors.Is(err, routeConflict) {
		t.Fatal("already-owned test range accepted")
	}
}

func TestFixedNetworkOwnership(t *testing.T) {
	initial, err := changesFor("test", []netip.Addr{netip.MustParseAddr("127.0.0.9")})
	if err != nil || !slices.Equal(initial, []string{"interface:4", "address:192.0.2.1/32", "route:192.0.2.0/24", "route:198.51.100.0/24", "route:203.0.113.0/24"}) {
		t.Fatal("test mode widened")
	}
	if _, err = changesFor("full", []netip.Addr{netip.MustParseAddr("fe80::1%8")}); !errors.Is(err, unsafeNetwork) {
		t.Fatal("uncapturable resolver accepted")
	}
	before, _ := changesFor("full", []netip.Addr{netip.MustParseAddr("192.168.1.1")})
	after, _ := changesFor("full", []netip.Addr{netip.MustParseAddr("10.0.0.1")})
	var journal, add, del []string
	save := func(v []string) error { journal = slices.Clone(v); return nil }
	apply := func(v string) error {
		if !slices.Contains(journal, v) {
			t.Fatal("write before journal")
		}
		add = append(add, v)
		return nil
	}
	drop := func(v string) error {
		if !slices.Contains(journal, v) {
			t.Fatal("delete before journal")
		}
		del = append(del, v)
		return nil
	}
	if err = reconcile(before, after, save, apply, drop); err != nil || !slices.Equal(journal, after) || !slices.Equal(add, []string{"route:10.0.0.1/32"}) || !slices.Equal(del, []string{"route:192.168.1.1/32"}) {
		t.Fatal("network switch changed unrelated rows")
	}
	if err = reconcile(nil, initial, func([]string) error { return errors.New("disk") }, func(string) error { t.Fatal("did not fail closed"); return nil }, drop); err == nil {
		t.Fatal("unjournaled mutation")
	}
	if err = reconcile(before, after, save, func(string) error { return errors.New("driver") }, drop); err == nil || !slices.Contains(journal, "route:192.168.1.1/32") || !slices.Contains(journal, "route:10.0.0.1/32") {
		t.Fatal("lost intent during partial write")
	}
	p := make([]byte, 20)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:], 20)
	copy(p[16:], []byte{192, 0, 2, 1})
	if !validPacket(p, "test", true) {
		t.Fatal("correct reply rejected")
	}
	p[19] = 99
	if validPacket(p, "test", true) || !validPacket(p, "test", false) {
		t.Fatal("injection boundary")
	}
	p[16] = 8
	if validPacket(p, "test", false) {
		t.Fatal("test mode exported unrelated traffic")
	}
}

func TestRecoveryOwner(t *testing.T) {
	r := &netRecord{OwnerSID: "owner", LUID: 17}
	deleted := false
	clear := func() error { deleted = true; return nil }
	fail := func(*netRecord) error { return errors.New("device") }
	if err := finishRecovery(r, "other", func(*netRecord) error { t.Fatal("wrong owner"); return nil }, clear); !errors.Is(err, staleNetwork) || deleted {
		t.Fatal("foreign recovery accepted")
	}
	if err := finishRecovery(r, "owner", fail, clear); !errors.Is(err, staleNetwork) || deleted {
		t.Fatal("lost recovery record")
	}
	if err := finishRecovery(r, "owner", func(v *netRecord) error {
		if v.LUID != 17 {
			t.Fatal("wrong interface")
		}
		return nil
	}, clear); err != nil || !deleted {
		t.Fatal("owner recovery failed")
	}
	if err := finishRecovery(nil, "owner", fail, clear); err != nil {
		t.Fatal(err)
	}
}
