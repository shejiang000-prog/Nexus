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
		for target, split := range map[string]string{
			"tcp:myip.ipip.net:443": "direct", "tcp:www.cloudflare.com:443": "selected",
			"tcp:www.baidu.com:443": "direct", "tcp:baidu.com.example.org:443": "selected",
			"tcp:223.5.5.5:80": "direct", "udp:8.8.8.8:443": "selected",
			"tcp:192.168.1.10:80": "direct", "tcp:[2400:3200::1]:80": "direct",
			"udp:[2606:4700:4700::1111]:443": "selected", "tcp:router.home.arpa:80": "direct",
			"udp:192.168.1.1:53": "split-dns", "tcp:223.5.5.5:53": "split-dns",
		} {
			dest, err := xnet.ParseDestination(target)
			if err != nil {
				t.Fatal(err)
			}
			got, want := fallback, split
			if route, err := r.PickRoute(&routesession.Context{Outbound: &session.Outbound{Target: dest}}); err == nil {
				got = route.GetOutboundTag()
			}
			if mode == "global" {
				want = "selected"
			}
			if got != want {
				t.Errorf("%s %s: got %s, want %s", mode, target, got, want)
			}
		}
	}
	msg := new(dns.Msg).SetQuestion("WWW.BAIDU.COM.", dns.TypeAAAA)
	p, err := msg.SetEdns0(1232, false).Pack()
	if err != nil || queryName(p) != "www.baidu.com" || queryName(append(p, 0)) != "" {
		t.Fatal("DNS classification did not require one complete IN question and optional OPT")
	}
}
