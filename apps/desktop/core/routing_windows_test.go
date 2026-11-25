package main

import (
	"testing"

	"github.com/miekg/dns"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	routesession "github.com/xtls/xray-core/features/routing/session"
)

func TestDesktopRouting(t *testing.T) {
	for _, mode := range []string{"split", "global"} {
		cfg, err := proxyConfig(21808, 21809, mode)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Inbound = nil // Route decisions need no sockets.
		instance, err := core.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer instance.Close()
		r := instance.GetFeature(routing.RouterType()).(routing.Router)
		fallback := instance.GetFeature(outbound.ManagerType()).(outbound.Manager).GetDefaultHandler().Tag()
		for _, tc := range []struct{ target, split, global string }{
			{"tcp:myip.ipip.net:443", "direct", "selected"},
			{"tcp:www.cloudflare.com:443", "selected", "selected"},
			{"tcp:www.baidu.com:443", "direct", "selected"},
			{"tcp:baidu.com.example.org:443", "selected", "selected"},
			{"tcp:223.5.5.5:80", "direct", "selected"},
			{"udp:8.8.8.8:443", "selected", "selected"},
			{"tcp:[2400:3200::1]:80", "direct", "selected"},
			{"udp:[2606:4700:4700::1111]:443", "selected", "selected"},
			{"tcp:192.168.1.10:80", "direct", "direct"},
			{"tcp:127.0.0.2:80", "direct", "direct"},
			{"tcp:169.254.1.2:80", "direct", "direct"},
			{"udp:[::1]:80", "direct", "direct"},
			{"udp:[fd12::1]:80", "direct", "direct"},
			{"udp:[fe80::1]:80", "direct", "direct"},
			{"tcp:localhost:80", "direct", "direct"},
			{"tcp:printer.local:80", "direct", "direct"},
			{"tcp:router.home.arpa:80", "direct", "direct"},
			{"tcp:router.home.arpa.example.org:80", "selected", "selected"},
			{"udp:192.168.1.1:53", "split-dns", "selected"},
			{"tcp:223.5.5.5:53", "split-dns", "selected"},
		} {
			dest, err := xnet.ParseDestination(tc.target)
			if err != nil {
				t.Fatal(err)
			}
			got, want := fallback, tc.split
			if route, err := r.PickRoute(&routesession.Context{Outbound: &session.Outbound{Target: dest}}); err == nil {
				got = route.GetOutboundTag()
			}
			if mode == "global" {
				want = tc.global
			}
			if got != want {
				t.Errorf("%s %s: got %s, want %s", mode, tc.target, got, want)
			}
		}
	}
	msg := new(dns.Msg).SetQuestion("WWW.BAIDU.COM.", dns.TypeAAAA)
	p, err := msg.SetEdns0(1232, false).Pack()
	if err != nil || queryName(p) != "www.baidu.com" || queryName(append(p, 0)) != "" {
		t.Fatal("DNS classification did not require one complete IN question and optional OPT")
	}
}
