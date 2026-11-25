// nexus-core is a single-session child process of the desktop shell.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	mobile "github.com/xtls/libxray/nexusmobile"
	"github.com/xtls/libxray/v2vpn"
	"github.com/xtls/xray-core/app/dispatcher"
	xlog "github.com/xtls/xray-core/app/log"
	"github.com/xtls/xray-core/app/proxyman"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/blackhole"
	"github.com/xtls/xray-core/proxy/http"
	"github.com/xtls/xray-core/proxy/socks"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
)

func proxyConfig() *core.Config {
	loopback := xnet.NewIPOrDomain(xnet.LocalHostIP)
	listen := func(port xnet.Port) *serial.TypedMessage {
		return serial.ToTypedMessage(&proxyman.ReceiverConfig{
			Listen: loopback, PortList: &xnet.PortList{Range: []*xnet.PortRange{xnet.SinglePortRange(port)}},
		})
	}
	return &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&xlog.Config{}), // Keep access logs and diagnostics off the status pipe.
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
		},
		Inbound: []*core.InboundHandlerConfig{
			{Tag: "socks", ReceiverSettings: listen(21808),
				ProxySettings: serial.ToTypedMessage(&socks.ServerConfig{Address: loopback, UdpEnabled: true})},
			{Tag: "http", ReceiverSettings: listen(21809),
				ProxySettings: serial.ToTypedMessage(&http.ServerConfig{})},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{Tag: "selected", ProxySettings: serial.ToTypedMessage(&blackhole.Config{})},
		},
	}
}

func run(invite string) error {
	fmt.Println("connecting")
	d, err := loadDevice(invite)
	if err != nil {
		return errors.New("device enrollment or lookup failed")
	}
	// The process owns the CNG handle until exit, including in-flight transport signing.
	client, err := mobile.StartPinned(d.Authority, d.Endpoint, "auto", d.CA, d.ID, d.Hello, d.Signer, nil, nil, nil, d.Pin)
	if err != nil {
		return errors.New("carrier connection failed")
	}
	defer client.Stop()
	if err := client.Confirm(); err != nil {
		return errors.New("device confirmation failed")
	}
	session, err := v2vpn.StartConfig(proxyConfig(), client)
	if err != nil {
		return errors.New("loopback proxy startup failed")
	}
	defer session.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	for {
		lane := client.State()
		if (lane != "h2" && lane != "h3") || session.ErrorCode() != 0 {
			return errors.New("carrier stopped or authorization failed")
		}
		if lane != last {
			if last != "" {
				if err := client.Confirm(); err != nil {
					return errors.New("carrier confirmation failed")
				}
			}
			fmt.Println("ready:" + lane)
			last = lane
		}
		<-ticker.C
	}
}

func main() {
	input := bufio.NewReaderSize(os.Stdin, 64*1024)
	line, err := input.ReadSlice('\n')
	var start struct {
		Invite *string `json:"invite"`
	}
	if err != nil || json.Unmarshal(line, &start) != nil || start.Invite == nil {
		fmt.Println("error")
		os.Exit(1)
	}
	// EOF is the stop command. Parent exit must release listeners even while
	// enrollment, the first handshake or an active stream is blocked.
	go func() {
		io.Copy(io.Discard, input)
		os.Exit(0)
	}()
	if err := run(*start.Invite); err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Println("error")
		os.Exit(1)
	}
}
