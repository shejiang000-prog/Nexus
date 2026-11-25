package main

import (
	"context"
	gotls "crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/apernet/quic-go"
	v2transport "github.com/xtls/libxray/internal/nexusv2/transport"
	"github.com/xtls/libxray/subscriptions"
	"github.com/xtls/xray-core/app/proxyman"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/reality"
	"github.com/xtls/xray-core/transport/internet/tls"
	"golang.org/x/sys/windows"
)

const nodeTimeout = 3 * time.Second

// One short-lived core reads a single snapshot. Its process-local dial controls
// are independent of a connected session, including a session capturing traffic.
func probeNodes(sourceID string) string {
	budget := 9 * time.Second // leave time for the shell to start the process and read JSON
	if sourceID == "" {
		budget = nodeTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	base := os.Getenv("LOCALAPPDATA")
	if !filepath.IsAbs(base) {
		return "subscription_storage"
	}
	dir := filepath.Join(base, "Nexus", "desktop")
	var nodes []subscriptions.Node
	rows := [][2]any{{"v2", nil}}
	if sourceID != "" {
		// Writers atomically replace the encrypted file. No writable store lock
		// or refresh is needed to see one complete revision.
		source, err := loadSource(filepath.Join(dir, "subscriptions"), sourceID)
		if err != nil {
			return subscriptionError(err)
		}
		nodes = source.Nodes
		rows = make([][2]any, len(nodes))
		for i, node := range nodes {
			rows[i][0] = choiceID(sourceID, node)
		}
	}
	if protectTunEgress() == nil {
		measureNodes(ctx, rows, func(ctx context.Context, index int) error {
			if sourceID == "" {
				return probeRegisteredV2(ctx, dir)
			}
			return probeSubscription(ctx, nodes[index])
		})
	}
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		return "generic"
	}
	return ""
}

func measureNodes(ctx context.Context, rows [][2]any, probe func(context.Context, int) error) {
	type measurement struct {
		index int
		ms    *int64
	}
	jobs := make(chan int, len(rows))
	for i := range rows {
		jobs <- i
	}
	close(jobs)
	done := make(chan measurement, len(rows))
	for range min(8, len(rows)) {
		go func() {
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				start := time.Now()
				node, cancel := context.WithTimeout(ctx, nodeTimeout)
				m := measurement{index: i}
				deadline, _ := node.Deadline()
				if err := probe(node, i); err == nil && time.Now().Before(deadline) {
					ms := time.Since(start).Milliseconds()
					m.ms = &ms
				}
				cancel()
				done <- m
			}
		}()
	}
	for range rows {
		select {
		case m := <-done:
			rows[m.index][1] = m.ms
		case <-ctx.Done():
			return
		}
	}
}

func probeRegisteredV2(ctx context.Context, dir string) error {
	// OPEN_EXISTING even for the enrollment gate. An empty profile stays empty.
	gate, err := openRecord(filepath.Join(dir, "enrollment.lock"), windows.GENERIC_READ, 0)
	if err != nil {
		return err
	}
	d, err := loadLockedDevice(dir, "")
	windows.CloseHandle(gate)
	if err != nil {
		return err
	}
	// As in a connected core, the process owns the key and lease until exit:
	// the losing carrier can still finish a CNG operation after selection.
	client, err := v2transport.StartPinned(ctx, v2transport.Config{
		Authority: d.Authority, Endpoint: d.Endpoint, Device: [16]byte(d.ID), Sign: d.Signer.signRaw,
	}, d.Pin)
	if err != nil {
		return err
	}
	client.Stop()
	return nil
}

func probeSubscription(ctx context.Context, node subscriptions.Node) error {
	var config conf.OutboundDetourConfig
	out, err := buildSubscriptionOutbound(node, &config)
	if err != nil {
		return err
	}
	dest, err := subscriptionEndpoint(out)
	if err != nil {
		return err
	}
	sender, err := out.SenderSettings.GetInstance()
	if err != nil {
		return err
	}
	stream, err := internet.ToMemoryStreamConfig(sender.(*proxyman.SenderConfig).StreamSettings)
	if err != nil {
		return err
	}
	secure := tls.ConfigFromStreamSettings(stream)
	if stream.ProtocolName == "hysteria" || stream.ProtocolName == "splithttp" && secure != nil && slices.Equal(secure.NextProtocol, []string{"h3"}) {
		if secure == nil {
			return errSubFormat
		}
		return probeQUIC(ctx, dest, stream, secure.GetTLSConfig(tls.WithDestination(dest)))
	}
	conn, err := internet.DialSystem(ctx, dest, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	if r := reality.ConfigFromStreamSettings(stream); r != nil {
		return reality.ProbeHandshake(conn, r, ctx, dest)
	}
	if secure == nil {
		return nil // plaintext stream transports have only TCP at this layer
	}
	options := []tls.Option{tls.WithDestination(dest)}
	websocket := stream.ProtocolName == "websocket" || stream.ProtocolName == "httpupgrade"
	if websocket {
		options = append(options, tls.WithNextProto("http/1.1"))
	}
	cfg := secure.GetTLSConfig(options...)
	if fp := tls.GetFingerprint(secure.Fingerprint); fp != nil {
		client := tls.UClient(conn, cfg, fp).(*tls.UConn)
		if websocket || slices.Equal(cfg.NextProtos, []string{"http/1.1"}) {
			return client.WebsocketHandshakeContext(ctx)
		}
		return client.HandshakeContext(ctx)
	}
	return gotls.Client(conn, cfg).HandshakeContext(ctx)
}

func probeQUIC(ctx context.Context, dest xnet.Destination, stream *internet.MemoryStreamConfig, cfg *gotls.Config) error {
	dest.Network = xnet.Network_UDP
	if dest.Address.Family().IsDomain() {
		// Xray's UDP socket opens with ResolveUDPAddr, which has no context.
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", dest.Address.Domain())
		if err != nil || len(ips) == 0 {
			return errors.New("probe DNS failed")
		}
		dest.Address = xnet.IPAddress(ips[0].AsSlice())
	}
	var conn net.Conn
	var err error
	if stream.FinalMask != nil {
		conn, err = stream.FinalMask.DialUDP(ctx, dest)
	} else {
		conn, err = internet.DialSystem(ctx, dest, nil)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	packet, ok := conn.(*xnet.PacketConnWrapper)
	if !ok {
		return errSubFormat
	}
	cfg.NextProtos = []string{"h3"}
	cfg.GetCertificate = nil
	tr := quic.Transport{Conn: packet.PacketConn, ConnectionIDGenerator: quic.ZeroLengthConnectionIDGenerator{}}
	defer tr.Close()
	q, err := tr.Dial(ctx, packet.RemoteAddr(), cfg, &quic.Config{
		EnableDatagrams: true, ChromeParrot: true, OmitMaxDatagramFrameSize: true, DisablePathManager: true,
	})
	if err != nil {
		return err
	}
	return q.CloseWithError(0, "")
}
