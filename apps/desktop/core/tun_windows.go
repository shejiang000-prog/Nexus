package main

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/xtls/xray-core/transport/internet"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/miekg/dns"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/app/router"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	routesession "github.com/xtls/xray-core/features/routing/session"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/proxy/tun"
)

const tunMTU = 1500

var errPacketSession = errors.New("packet session closed")

type packetDevice struct {
	pipe      io.ReadWriteCloser
	done      chan struct{}
	writes    sync.Mutex
	closeOnce sync.Once
}

func (d *packetDevice) Close() error {
	d.closeOnce.Do(func() { close(d.done); d.pipe.Close() })
	return nil
}
func (*packetDevice) Start() error          { return nil }
func (*packetDevice) Name() (string, error) { return "Nexus", nil }
func (*packetDevice) Index() (int, error)   { return 0, errors.New("packet device is owned by helper") }

func (d *packetDevice) ReadPacket(b []byte) ([]byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(d.pipe, head[:]); err != nil {
		d.Close()
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(head[:]))
	if n > tunMTU || n > len(b) || n == 0 {
		d.Close()
		return nil, errPacketSession
	}
	p := b[:n]
	if _, err := io.ReadFull(d.pipe, p); err != nil {
		d.Close()
		return nil, err
	}
	if p[0]>>4 != 4 && p[0]>>4 != 6 {
		d.Close()
		return nil, errPacketSession
	}
	return p, nil
}

func (d *packetDevice) WritePacket(header, payload []byte) error {
	n := len(header) + len(payload)
	if n == 0 || n > tunMTU {
		return errors.New("invalid packet length")
	}
	wire := make([]byte, 2+n)
	binary.BigEndian.PutUint16(wire, uint16(n))
	copy(wire[2:], header)
	copy(wire[2+len(header):], payload)
	d.writes.Lock()
	defer d.writes.Unlock()
	written, err := d.pipe.Write(wire)
	if err == nil && written != len(wire) {
		err = io.ErrShortWrite
	}
	if err != nil {
		d.Close()
	}
	return err
}

var testNetworks = [...]netip.Prefix{
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
}

func isTestNetwork(ip netip.Addr) bool {
	for _, prefix := range testNetworks {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

type packetSession struct {
	mode       string
	device     *packetDevice
	mu         sync.Mutex
	paths      physicalRoutes
	sockets    map[net.Conn]syscall.RawConn
	lastSweep  time.Time
	generation uint64
	learned    map[netip.Addr]time.Time
}

func newPacketSession(mode string) (*packetSession, error) {
	if mode == "" {
		return nil, nil
	}
	if err := protectTunEgress(); err != nil {
		return nil, err
	}
	s := &packetSession{mode: mode, paths: currentPhysicalRoutes(), learned: make(map[netip.Addr]time.Time), sockets: make(map[net.Conn]syscall.RawConn)}
	internet.DesktopDialStarted = s.trackDial
	return s, nil
}

func (s *packetSession) configure(cfg *core.Config, mode string) error {
	if s == nil {
		return nil
	}
	cfg.Inbound = append(cfg.Inbound, &core.InboundHandlerConfig{Tag: "packet-tun", ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{}), ProxySettings: serial.ToTypedMessage(&tun.Config{Name: "Nexus", MTU: tunMTU})})
	for i, app := range cfg.App {
		if app.Type != serial.ToTypedMessage(&router.Config{}).Type {
			continue
		}
		value, err := app.GetInstance()
		if err != nil {
			return err
		}
		r := value.(*router.Config)
		if mode == "global" {
			split, err := desktopRouter("split")
			if err != nil {
				return err
			}
			domains := split.Rule[1]
			domains.InboundTag = []string{"tun-question"}
			r.Rule = append([]*router.RoutingRule{domains}, r.Rule...)
		}
		cfg.App[i] = serial.ToTypedMessage(r)
		return nil
	}
	return errConfig
}

func (s *packetSession) setup(instance *core.Instance, mode string) error {
	return s.setupWith(instance, mode, openTunSession)
}
func (s *packetSession) setupWith(instance *core.Instance, mode string, open func(string) (io.ReadWriteCloser, error)) error {
	if mode == "split" {
		physical := dialPhysicalDNS
		if s != nil {
			physical = dialPreferredDNS
		}
		h := &splitDNS{instance: instance, physical: physical}
		if err := instance.GetFeature(outbound.ManagerType()).(outbound.Manager).AddHandler(context.Background(), h); err != nil {
			return err
		}
	}
	if s == nil {
		return nil
	}
	in, err := instance.GetFeature(inbound.ManagerType()).(inbound.Manager).GetHandler(context.Background(), "packet-tun")
	if err != nil {
		return err
	}
	h := in.(interface{ GetInbound() proxy.Inbound }).GetInbound().(*tun.Handler)
	r := instance.GetFeature(routing.RouterType()).(routing.Router)
	dnsHandler := &splitDNS{instance: instance, physical: dialPreferredDNS, packet: s, learn: mode == "split"}
	if err := instance.GetFeature(outbound.ManagerType()).(outbound.Manager).AddHandler(context.Background(), dnsHandler); err != nil {
		return err
	}
	pipe, err := open(s.mode)
	if err != nil {
		return err
	}
	s.device = &packetDevice{pipe: pipe, done: make(chan struct{})}
	h.UsePacketDevice(s.device, func(dst xnet.Destination) string {
		if dst.Port == 53 {
			return "tun-dns"
		}
		if s.mode == "test" && dst.Address.Family().IsIP() {
			ip, _ := netip.AddrFromSlice(dst.Address.IP())
			if isTestNetwork(ip.Unmap()) {
				return "selected"
			}
		}
		if mode == "split" && dst.Address.Family().IsIP() {
			addr, _ := netip.AddrFromSlice(dst.Address.IP())
			s.mu.Lock()
			deadline, ok := s.learned[addr.Unmap()]
			if ok && !time.Now().Before(deadline) {
				delete(s.learned, addr.Unmap())
				ok = false
			}
			s.mu.Unlock()
			if ok {
				return "direct"
			}
		}
		route, err := r.PickRoute(&routesession.Context{Inbound: &session.Inbound{Tag: "packet-tun"}, Outbound: &session.Outbound{Target: dst}})
		if err == nil {
			return route.GetOutboundTag()
		}
		return "selected"
	})
	return nil
}

func (s *packetSession) done() <-chan struct{} {
	if s == nil || s.device == nil {
		return nil
	}
	return s.device.done
}

// Retain the original concrete Xray connection (including UDP PacketConn).
// Closing old pooled transports on an interface change forces a fresh, bound
// dial even for QUIC clients that otherwise retain their previous socket.
func (s *packetSession) trackDial() func(net.Conn) {
	epoch := s.epoch()
	return func(conn net.Conn) {
		rawConn, ok := conn.(syscall.Conn)
		if udp, isUDP := conn.(*xnet.PacketConnWrapper); isUDP {
			rawConn, ok = udp.PacketConn.(syscall.Conn)
		}
		if !ok {
			conn.Close()
			return
		}
		raw, err := rawConn.SyscallConn()
		if err != nil {
			conn.Close()
			return
		}
		s.mu.Lock()
		if epoch == s.generation {
			s.sockets[conn] = raw
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		conn.Close()
	}
}

func (s *packetSession) networkChanged() bool {
	if s == nil {
		return false
	}
	paths := currentPhysicalRoutes()
	s.mu.Lock()
	if paths == s.paths {
		if time.Since(s.lastSweep) >= 10*time.Second {
			s.lastSweep = time.Now()
			for conn, raw := range s.sockets {
				if raw.Control(func(uintptr) {}) != nil {
					delete(s.sockets, conn)
				}
			}
		}
		s.mu.Unlock()
		return false
	}
	s.paths = paths
	s.generation++
	clear(s.learned)
	old := s.sockets
	s.sockets = make(map[net.Conn]syscall.RawConn)
	s.mu.Unlock()
	for conn := range old {
		conn.Close()
	}
	return true
}

func (s *packetSession) epoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

// Learn only A/AAAA in the answer section for this exact question or its
// CNAME chain. Never retain an address longer than its shortest chain TTL.
func (s *packetSession) remember(epoch uint64, query, answer []byte) {
	var q, a dns.Msg
	if q.Unpack(query) != nil || a.Unpack(answer) != nil || !a.Response || a.Truncated || a.Opcode != dns.OpcodeQuery || a.Rcode != dns.RcodeSuccess ||
		q.Id != a.Id || len(q.Question) != 1 || len(a.Question) != 1 {
		return
	}
	ask, echo := q.Question[0], a.Question[0]
	if ask.Qclass != dns.ClassINET || ask.Qclass != echo.Qclass || ask.Qtype != echo.Qtype || !strings.EqualFold(ask.Name, echo.Name) || ask.Qtype != dns.TypeA && ask.Qtype != dns.TypeAAAA {
		return
	}
	name := strings.ToLower(ask.Name)
	ttl := uint32(600)
	for steps := 0; steps <= 16; steps++ {
		next := ""
		for _, rr := range a.Answer {
			if c, ok := rr.(*dns.CNAME); ok && c.Hdr.Class == dns.ClassINET && strings.EqualFold(c.Hdr.Name, name) {
				target := strings.ToLower(c.Target)
				if next != "" && next != target {
					return
				}
				next = target
				ttl = min(ttl, c.Hdr.Ttl)
			}
		}
		if next == "" {
			break
		}
		if next == name || steps == 16 {
			return
		}
		name = next
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch != s.generation {
		return
	}
	now := time.Now()
	for _, rr := range a.Answer {
		h := rr.Header()
		if h.Class != dns.ClassINET || h.Rrtype != ask.Qtype || !strings.EqualFold(h.Name, name) {
			continue
		}
		var addr netip.Addr
		switch v := rr.(type) {
		case *dns.A:
			addr, _ = netip.AddrFromSlice(v.A.To4())
		case *dns.AAAA:
			addr, _ = netip.AddrFromSlice(v.AAAA)
		}
		if !addr.IsValid() {
			continue
		}
		addr = addr.Unmap()
		life := min(ttl, h.Ttl)
		if life == 0 {
			delete(s.learned, addr)
			continue
		}
		if len(s.learned) >= 8192 {
			// Drop stale entries as a batch. A full cache cannot widen direct routing.
			for ip, end := range s.learned {
				if !now.Before(end) {
					delete(s.learned, ip)
				}
			}
			if _, exists := s.learned[addr]; !exists && len(s.learned) >= 8192 {
				continue
			}
		}
		s.learned[addr] = now.Add(time.Duration(life) * time.Second)
	}
}
