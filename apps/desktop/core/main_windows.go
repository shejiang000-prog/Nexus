// nexus-core is a single-session child process of the desktop shell.
package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/xtls/xray-core/core"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	mobile "github.com/xtls/libxray/nexusmobile"
	"github.com/xtls/libxray/tuntrust"
	"github.com/xtls/libxray/v2vpn"
	"github.com/xtls/xray-core/app/dispatcher"
	xlog "github.com/xtls/xray-core/common/log"
	"golang.org/x/sys/windows"
)

var errPort = errors.New("port")
var errConfig = errors.New("config")

//go:embed download_node.sh
var ownerDownloadScript string

func ownerCommand(line []byte) string {
	var req ownerRequest
	if len(line) > 32*1024 || json.Unmarshal(line, &req) != nil {
		return "invalid_request"
	}
	clear(line)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The shell owns the write end until completion. EOF also covers a shell crash.
	go func() {
		io.Copy(io.Discard, os.Stdin)
		cancel()
	}()
	result, err := runOwner(ctx, req, func(phase string) {
		fmt.Println("progress:" + phase)
	})
	if err != nil {
		if ctx.Err() != nil {
			return "cancelled"
		}
		var code ownerError
		if errors.As(err, &code) {
			return string(code)
		}
		return "owner_failed"
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "owner_failed"
	}
	fmt.Printf("result:%s\n", data)
	return ""
}

func reservePort(preferred int) (*net.TCPListener, error) {
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: preferred}
	ln, err := net.ListenTCP("tcp4", addr)
	if err != nil && preferred != 0 {
		addr.Port = 0
		return net.ListenTCP("tcp4", addr)
	}
	return ln, err
}

func listenProxy(mode string, packets *packetSession, start func(*core.Config) error) (int, int, error) {
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
		// A bind race after elevation rolls back the adapter. A second helper
		// would prompt for UAC twice during the same connection attempt.
		if packets != nil && packets.device != nil {
			return 0, 0, errPort
		}
	}
	return 0, 0, errPort
}

func run(invite, mode string, packets *packetSession) error {
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
	socks, http, err := listenProxy(mode, packets, func(cfg *core.Config) error {
		var err error
		session, err = startProxy(cfg, client, mode, packets)
		return err
	})
	if err != nil {
		return err
	}
	defer session.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	reconnecting := false
	var traffic trafficSample
	var halfSeconds uint64
	for {
		if packets.networkChanged() {
			client.RefreshNetwork()
			reconnecting = true
			if last != "" {
				fmt.Println("connecting")
				last = ""
			}
		}
		if reconnecting {
			if err := client.Confirm(); err != nil {
				if code := mobile.ErrorCode(err); code == 2 || code == 3 {
					return err
				}
				select {
				case <-packets.done():
					return errPacketSession
				case <-ticker.C:
					continue
				}
			}
			reconnecting = false
		}
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
		select {
		case <-packets.done():
			return errPacketSession
		case <-ticker.C:
			halfSeconds++
			if halfSeconds%2 == 0 {
				traffic.publish()
			}
		}
	}
}

func command() (result string) {
	const maxCommand = 3<<20 + 4096
	input := bufio.NewReader(io.LimitReader(os.Stdin, maxCommand+1))
	line, err := input.ReadBytes('\n')
	var req struct {
		Action         string  `json:"action"`
		Expected       string  `json:"expected"`
		Invite         *string `json:"invite"`
		Mode           string  `json:"mode"`
		Tun            bool    `json:"tun"`
		TrafficStats   bool    `json:"trafficStats"`
		URL            string  `json:"url"`
		ID             string  `json:"id"`
		Source         string  `json:"source"`
		SubscriptionID string  `json:"subscriptionId"`
		V2             *bool   `json:"v2"`
		NodeID         string  `json:"nodeId"`
	}
	if err != nil || len(line) > maxCommand || json.Unmarshal(line, &req) != nil {
		return "invalid_request"
	}
	switch req.Action {
	case "owner":
		return ownerCommand(line)
	case "probe_nodes":
		if req.V2 != nil && (!*req.V2 || req.SubscriptionID != "") || req.V2 == nil && !validSourceID(req.SubscriptionID) {
			return "invalid_request"
		}
		return probeNodes(req.SubscriptionID)
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
	case "recover_tun":
		return recoverTunCommand()
	case "", "start", "start_test_tun":
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
	dispatcher.DesktopTraffic.Enabled = req.TrafficStats
	// Only the command is size-limited. A full command cannot synthesize a stop.
	stopping := make(chan struct{})
	go func() {
		io.Copy(io.Discard, os.Stdin)
		close(stopping)
		if err := closeTunOnStop(); err != nil {
			fmt.Println("error:tun_recovery")
			os.Exit(1)
		}
		os.Exit(0)
	}()
	takeover := ""
	if req.Action == "start_test_tun" {
		takeover = "test"
	} else if req.Tun {
		takeover = "full"
	}
	packets, err := newPacketSession(takeover)
	if err != nil {
		return "tun_session"
	}
	if packets != nil {
		defer func() {
			// During a requested stop the stdin watcher owns the final status.
			select {
			case <-stopping:
				select {}
			default:
			}
			if closeTunOnStop() != nil {
				result = "tun_recovery"
			}
		}()
	}
	if req.Source == "subscription" {
		return runSubscription(req.SubscriptionID, req.NodeID, req.Mode, packets)
	}
	if err := run(*req.Invite, req.Mode, packets); err != nil {
		if code := tunErrorCode(err); code != "" {
			return code
		}
		if errors.Is(err, errPacketSession) {
			return "tun_session"
		}
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

func installationLock(exe, appDir string) (windows.Handle, error) {
	if !strings.EqualFold(filepath.Dir(exe), appDir) {
		// A shortcut can use a short name or a junction to the installed directory.
		actual, e1 := os.Stat(filepath.Dir(exe))
		expected, e2 := os.Stat(appDir)
		if e1 != nil || e2 != nil || !os.SameFile(actual, expected) {
			return 0, nil
		}
	}
	name, err := windows.UTF16PtrFromString(filepath.Join(filepath.Dir(appDir), "maintenance.lock"))
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
}

func main() {
	exe, err := os.Executable()
	helper, folderErr := tuntrust.InstalledHelper()
	if err != nil || folderErr != nil {
		fmt.Println("error:maintenance")
		os.Exit(1)
	}
	lock, err := installationLock(exe, filepath.Dir(helper))
	if err != nil {
		fmt.Println("error:maintenance")
		os.Exit(1)
	}
	if lock != 0 {
		defer windows.CloseHandle(lock)
	}
	// Share conversion can log before an Xray instance installs its no-log config.
	// The inherited pipe is exclusively for public responses and status codes.
	xlog.RegisterHandler(discardXrayLog{})
	log.SetOutput(io.Discard)
	if code := command(); code != "" {
		fmt.Println("error:" + code)
		os.Exit(1)
	}
}
