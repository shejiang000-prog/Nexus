package main

import (
	"context"
	_ "embed"
	"errors"
	"net/netip"
	"strings"

	mobile "github.com/xtls/libxray/nexusmobile"
	"github.com/xtls/libxray/v2vpn"
	"github.com/xtls/xray-core/app/dispatcher"
	xlog "github.com/xtls/xray-core/app/log"
	"github.com/xtls/xray-core/app/proxyman"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/geodata"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy/blackhole"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/proxy/http"
	"github.com/xtls/xray-core/proxy/socks"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
)

//go:embed split-rules.txt
var splitRules string

func desktopRouter(mode string) (*router.Config, error) {
	var rules, dnsTag string
	switch mode {
	case "split":
		rules, dnsTag = strings.TrimSpace(splitRules), "split-dns"
	case "global":
		// Keep explicit DNS inside V2 even if the application names a LAN resolver.
		dnsTag = "selected"
		rules = `ip:127.0.0.0/8
ip:10.0.0.0/8
ip:172.16.0.0/12
ip:192.168.0.0/16
ip:169.254.0.0/16
ip:::1/128
ip:fc00::/7
ip:fe80::/10`
	default:
		return nil, errors.New("unknown routing mode")
	}
	target := &router.RoutingRule_Tag{Tag: "direct"}
	names := &router.RoutingRule{TargetTag: target}
	ips := &router.RoutingRule{TargetTag: target}
	types := map[string]geodata.Domain_Type{
		"domain": geodata.Domain_Domain, "full": geodata.Domain_Full,
		"regexp": geodata.Domain_Regex, "keyword": geodata.Domain_Substr,
	}
	for line := range strings.SplitSeq("full:localhost\ndomain:local\ndomain:home.arpa\n"+rules, "\n") {
		kind, value, ok := strings.Cut(line, ":")
		if !ok || value == "" {
			return nil, errors.New("invalid split rule")
		}
		if kind == "ip" {
			p, err := netip.ParsePrefix(value)
			if err != nil {
				return nil, err
			}
			cidr := &geodata.CIDR{Ip: p.Addr().AsSlice(), Prefix: uint32(p.Bits())}
			ips.Ip = append(ips.Ip, &geodata.IPRule{Value: &geodata.IPRule_Custom{Custom: &geodata.CIDRRule{Cidr: cidr}}})
		} else if typ, ok := types[kind]; ok {
			d := &geodata.Domain{Type: typ, Value: value}
			names.Domain = append(names.Domain, &geodata.DomainRule{Value: &geodata.DomainRule_Custom{Custom: d}})
		} else {
			return nil, errors.New("unsupported split rule")
		}
	}
	return &router.Config{DomainStrategy: router.Config_AsIs, Rule: []*router.RoutingRule{
		{TargetTag: &router.RoutingRule_Tag{Tag: dnsTag}, PortList: &xnet.PortList{Range: []*xnet.PortRange{xnet.SinglePortRange(53)}}},
		names, ips,
	}}, nil
}

func proxyConfig(socksPort, httpPort int, mode string) (*core.Config, error) {
	if socksPort < 1 || httpPort < 1 || socksPort > 65535 || httpPort > 65535 || socksPort == httpPort {
		return nil, errors.New("invalid proxy ports")
	}
	r, err := desktopRouter(mode)
	if err != nil {
		return nil, err
	}
	loopback := xnet.NewIPOrDomain(xnet.LocalHostIP)
	listen := func(port int) *serial.TypedMessage {
		return serial.ToTypedMessage(&proxyman.ReceiverConfig{Listen: loopback,
			PortList: &xnet.PortList{Range: []*xnet.PortRange{xnet.SinglePortRange(xnet.Port(port))}}})
	}
	cfg := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&xlog.Config{}),
			serial.ToTypedMessage(&dispatcher.Config{}), serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(r),
		},
		Inbound: []*core.InboundHandlerConfig{
			{Tag: "socks", ReceiverSettings: listen(socksPort), ProxySettings: serial.ToTypedMessage(&socks.ServerConfig{Address: loopback, UdpEnabled: true})},
			{Tag: "http", ReceiverSettings: listen(httpPort), ProxySettings: serial.ToTypedMessage(&http.ServerConfig{})},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{Tag: "selected", ProxySettings: serial.ToTypedMessage(&blackhole.Config{})},
			{Tag: "direct", ProxySettings: serial.ToTypedMessage(&freedom.Config{})},
		},
	}
	return cfg, nil
}

func startProxy(cfg *core.Config, client *mobile.Client, mode string) (*v2vpn.Session, error) {
	if mode == "global" {
		return v2vpn.StartConfig(cfg, client)
	}
	return v2vpn.StartWithSetup(cfg, client, func(instance *core.Instance) error {
		manager := instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
		return manager.AddHandler(context.Background(), &splitDNS{instance: instance, physical: dialPhysicalDNS})
	})
}
