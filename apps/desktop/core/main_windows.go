// nexus-core is a single-session child process of the desktop shell.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/xtls/xray-core/core"
	"io"
	"log"
	"net"
	"os"
	"time"

	mobile "github.com/xtls/libxray/nexusmobile"
	"github.com/xtls/libxray/v2vpn"
	xlog "github.com/xtls/xray-core/common/log"
	"golang.org/x/sys/windows"
)

var errPort = errors.New("port")
var errConfig = errors.New("config")

func reservePort(preferred int) (*net.TCPListener, error) {
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: preferred}
	ln, err := net.ListenTCP("tcp4", addr)
	if err != nil && preferred != 0 {
		addr.Port = 0
		return net.ListenTCP("tcp4", addr)
	}
	return ln, err
}

func listenProxy(mode string, start func(*core.Config) error) (int, int, error) {
	// Hold both reservations until the fixed Xray configuration is built. A
	// competing process can still win the handoff; retry the actual bind.
	for attempt := 0; attempt < 3; attempt++ {
		socks, err := reservePort(21808)
		if err != nil {
			return 0, 0, errPort
		}
		http, err := reservePort(21809)
		if err != nil {
			socks.Close()
			return 0, 0, errPort
		}
		s, h := socks.Addr().(*net.TCPAddr).Port, http.Addr().(*net.TCPAddr).Port
		cfg, err := proxyConfig(s, h, mode)
		socks.Close()
		http.Close()
		if err != nil {
			return 0, 0, errConfig
		}
		err = start(cfg)
		if err == nil {
			return s, h, nil
		}
		if !errors.Is(err, windows.WSAEADDRINUSE) && !errors.Is(err, windows.WSAEACCES) {
			return 0, 0, err
		}
	}
	return 0, 0, errPort
}

func run(invite, mode string) error {
	fmt.Println("connecting")
	d, err := loadDevice(invite)
	if err != nil {
		return err
	}
	client, err := mobile.StartPinned(d.Authority, d.Endpoint, "auto", d.CA, d.ID, d.Hello, d.Signer, nil, nil, nil, d.Pin)
	if err != nil {
		return err
	}
	defer client.Stop()
	if err := client.Confirm(); err != nil {
		return err
	}
	var session *v2vpn.Session
	socks, http, err := listenProxy(mode, func(cfg *core.Config) error {
		var err error
		session, err = startProxy(cfg, client, mode)
		return err
	})
	if err != nil {
		return err
	}
	defer session.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	for {
		lane := client.State()
		if lane != "h2" && lane != "h3" {
			return errors.New("session stopped")
		}
		if session.ErrorCode() != 0 {
			return errors.New("device authentication failed")
		}
		if lane != last {
			if last != "" {
				if err := client.Confirm(); err != nil {
					return err
				}
			}
			fmt.Printf("ready:%s:%d:%d\n", lane, socks, http)
			last = lane
		}
		<-ticker.C
	}
}

func command() string {
	const maxCommand = 3<<20 + 4096
	input := bufio.NewReader(io.LimitReader(os.Stdin, maxCommand+1))
	line, err := input.ReadBytes('\n')
	var req struct {
		Action         string  `json:"action"`
		Expected       string  `json:"expected"`
		Invite         *string `json:"invite"`
		Mode           string  `json:"mode"`
		URL            string  `json:"url"`
		ID             string  `json:"id"`
		Source         string  `json:"source"`
		SubscriptionID string  `json:"subscriptionId"`
		NodeID         string  `json:"nodeId"`
	}
	if err != nil || len(line) > maxCommand || json.Unmarshal(line, &req) != nil {
		return "invalid_request"
	}
	switch req.Action {
	case "subscriptions", "import_subscription", "update_subscription", "remove_subscription":
		return subscriptionCommand(req.Action, req.URL, req.ID)
	case "identity":
		label, alias, err := currentIdentity()
		if err != nil {
			return errorCode(err)
		}
		fmt.Println("identity:" + alias + "|" + label)
		return ""
	case "remove":
		if err := removeDevice(req.Expected); err != nil {
			return errorCode(err)
		}
		fmt.Println("removed")
		return ""
	case "", "start":
		if req.Source == "subscription" {
			if req.Invite != nil && *req.Invite != "" || !validSourceID(req.SubscriptionID) || !validSourceID(req.NodeID) {
				return "invalid_request"
			}
		} else if (req.Source != "" && req.Source != "private") || req.Invite == nil || len(*req.Invite) > 64*1024 {
			return "invalid_request"
		}
		if req.Mode == "" {
			req.Mode = "global"
		} // PC-M0 pipe compatibility.
		if req.Mode != "split" && req.Mode != "global" {
			return "invalid_request"
		}
	default:
		return "invalid_request"
	}
	// Only the command is size-limited. A full command cannot synthesize a stop.
	go func() { io.Copy(io.Discard, os.Stdin); os.Exit(0) }()
	if req.Source == "subscription" {
		return runSubscription(req.SubscriptionID, req.NodeID, req.Mode)
	}
	if err := run(*req.Invite, req.Mode); err != nil {
		if errors.Is(err, errPort) {
			return "local_port"
		}
		if errors.Is(err, errConfig) {
			return "routing"
		}
		return errorCode(err)
	}
	return ""
}

type discardXrayLog struct{}

func (discardXrayLog) Handle(xlog.Message) {}

func main() {
	// Share conversion can log before an Xray instance installs its no-log config.
	// The inherited pipe is exclusively for public responses and status codes.
	xlog.RegisterHandler(discardXrayLog{})
	log.SetOutput(io.Discard)
	if code := command(); code != "" {
		fmt.Println("error:" + code)
		os.Exit(1)
	}
}
