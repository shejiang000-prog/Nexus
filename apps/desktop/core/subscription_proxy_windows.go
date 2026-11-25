package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/xtls/libxray/subscriptions"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/infra/conf"
	_ "github.com/xtls/xray-core/proxy/hysteria"
	_ "github.com/xtls/xray-core/proxy/shadowsocks"
	ss2022 "github.com/xtls/xray-core/proxy/shadowsocks_2022"
	_ "github.com/xtls/xray-core/proxy/trojan"
	vless "github.com/xtls/xray-core/proxy/vless/outbound"
	vmess "github.com/xtls/xray-core/proxy/vmess/outbound"
	"github.com/xtls/xray-core/transport/internet"
	_ "github.com/xtls/xray-core/transport/internet/grpc"
	_ "github.com/xtls/xray-core/transport/internet/httpupgrade"
	_ "github.com/xtls/xray-core/transport/internet/splithttp"
	_ "github.com/xtls/xray-core/transport/internet/websocket"
)

// Read the endpoint from Xray's built configuration. Only that endpoint may be
// contacted before exposing the session as ready.
func subscriptionEndpoint(out *core.OutboundHandlerConfig) (xnet.Destination, error) {
	p, err := out.ProxySettings.GetInstance()
	if err != nil {
		return xnet.Destination{}, err
	}
	var endpoint *protocol.ServerEndpoint
	switch c := p.(type) {
	case interface {
		GetServer() *protocol.ServerEndpoint
	}:
		endpoint = c.GetServer()
	case *vless.Config:
		endpoint = c.GetVnext()
	case *vmess.Config:
		endpoint = c.GetReceiver()
	case *ss2022.ClientConfig:
		endpoint = &protocol.ServerEndpoint{Address: c.GetAddress(), Port: c.GetPort()}
	}
	if endpoint == nil || endpoint.Address == nil || endpoint.Port == 0 || endpoint.Port > 65535 {
		return xnet.Destination{}, errors.New("invalid subscription endpoint")
	}
	return xnet.TCPDestination(endpoint.Address.AsAddress(), xnet.Port(endpoint.Port)), nil
}

func runSubscription(sourceID, nodeID, mode string) string {
	fmt.Println("connecting")
	n, err := subscriptionNode(sourceID, nodeID)
	if err != nil {
		return subscriptionError(err)
	}
	if err := subscriptions.ValidateOutbound(n.Outbound); err != nil {
		return "subscription_format"
	}
	var config conf.OutboundDetourConfig
	if err := json.Unmarshal(n.Outbound, &config); err != nil {
		return "subscription_format"
	}
	config.Tag = "selected"
	out, err := config.Build()
	if err != nil {
		return "subscription_format"
	}
	endpoint, err := subscriptionEndpoint(out)
	if err != nil {
		return "subscription_format"
	}

	var instance *core.Instance
	socks, httpPort, err := listenProxy(mode, func(cfg *core.Config) error {
		cfg.Outbound[0] = out
		var err error
		instance, err = core.New(cfg)
		if err != nil {
			return err
		}
		if mode == "split" {
			err = installSplitDNS(instance)
		}
		if err == nil {
			err = instance.Start()
		}
		if err != nil {
			instance.Close()
		}
		return err
	})
	if err != nil {
		if errors.Is(err, errPort) {
			return "local_port"
		}
		return "routing"
	}
	defer instance.Close()

	// RAW/REALITY/TLS and Hysteria perform their transport handshake here.
	// Some HTTP stream transports connect lazily, so check the endpoint's
	// TCP reachability there. Proxy credentials are used on real traffic.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	network := "tcp"
	if config.StreamSetting != nil && config.StreamSetting.Network != nil {
		network = string(*config.StreamSetting.Network)
	}
	var conn net.Conn
	switch network {
	case "", "tcp", "raw", "hysteria":
		handler := instance.GetFeature(outbound.ManagerType()).(outbound.Manager).GetHandler("selected")
		dialer, ok := handler.(internet.Dialer)
		if !ok {
			return "routing"
		}
		conn, err = dialer.Dial(ctx, endpoint)
	default:
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", endpoint.NetAddr())
	}
	if err != nil {
		return "node_unreachable"
	}
	conn.Close()
	cancel()
	fmt.Printf("ready:subscription:%d:%d\n", socks, httpPort)
	// The parent owns this single-session process via its inherited stdin.
	select {}
}
