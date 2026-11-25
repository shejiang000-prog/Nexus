// nexus-core is a single-session child process of the desktop shell.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	mobile "github.com/xtls/libxray/nexusmobile"
	"github.com/xtls/libxray/v2vpn"
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

func listenProxy(client *mobile.Client, mode string) (*v2vpn.Session, int, int, error) {
	// Hold both reservations until the fixed Xray configuration is built. A
	// competing process can still win the handoff; retry the actual bind.
	for attempt := 0; attempt < 3; attempt++ {
		socks, err := reservePort(21808)
		if err != nil {
			return nil, 0, 0, errPort
		}
		http, err := reservePort(21809)
		if err != nil {
			socks.Close()
			return nil, 0, 0, errPort
		}
		s, h := socks.Addr().(*net.TCPAddr).Port, http.Addr().(*net.TCPAddr).Port
		cfg, err := proxyConfig(s, h, mode)
		socks.Close()
		http.Close()
		if err != nil {
			return nil, 0, 0, errConfig
		}
		session, err := startProxy(cfg, client, mode)
		if err == nil {
			return session, s, h, nil
		}
		if !errors.Is(err, windows.WSAEADDRINUSE) && !errors.Is(err, windows.WSAEACCES) {
			return nil, 0, 0, err
		}
	}
	return nil, 0, 0, errPort
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
	session, socks, http, err := listenProxy(client, mode)
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
	input := bufio.NewReaderSize(os.Stdin, 64*1024)
	line, err := input.ReadSlice('\n')
	var req struct {
		Action   string  `json:"action"`
		Expected string  `json:"expected"`
		Invite   *string `json:"invite"`
		Mode     string  `json:"mode"`
	}
	if err != nil || json.Unmarshal(line, &req) != nil {
		return "invalid_request"
	}
	switch req.Action {
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
		if req.Invite == nil {
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
	go func() { io.Copy(io.Discard, input); os.Exit(0) }()
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

func main() {
	if code := command(); code != "" {
		fmt.Println("error:" + code)
		os.Exit(1)
	}
}
