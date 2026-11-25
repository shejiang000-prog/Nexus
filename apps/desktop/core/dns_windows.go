package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math/bits"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/miekg/dns"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	routesession "github.com/xtls/xray-core/features/routing/session"
	"github.com/xtls/xray-core/transport"
	"golang.org/x/sys/windows"
)

type splitDNS struct {
	instance *core.Instance
	physical func(context.Context, string) (net.Conn, error)
	packet   *packetSession
	learn    bool
}

func (h *splitDNS) Tag() string {
	if h.packet != nil {
		return "tun-dns"
	}
	return "split-dns"
}
func (*splitDNS) Start() error                         { return nil }
func (*splitDNS) Close() error                         { return nil }
func (*splitDNS) SenderSettings() *serial.TypedMessage { return nil }
func (*splitDNS) ProxySettings() *serial.TypedMessage  { return nil }

// Like Android's domestic_question, only classify one ordinary IN question,
// optionally with a single EDNS OPT. Forward ambiguous wire data through V2.
func queryName(p []byte) string {
	if len(p) < 12 || binary.BigEndian.Uint16(p[2:])&0xf800 != 0 ||
		binary.BigEndian.Uint16(p[4:]) != 1 || binary.BigEndian.Uint32(p[6:]) != 0 {
		return ""
	}
	name, off, err := dns.UnpackDomainName(p, 12)
	if err != nil || off+4 > len(p) || binary.BigEndian.Uint16(p[off+2:]) != 1 {
		return ""
	}
	off += 4
	switch binary.BigEndian.Uint16(p[10:]) {
	case 0:
	case 1:
		if off+11 > len(p) || p[off] != 0 || binary.BigEndian.Uint16(p[off+1:]) != 41 {
			return ""
		}
		off += 11 + int(binary.BigEndian.Uint16(p[off+9:]))
	default:
		return ""
	}
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if off != len(p) || len(name) > 253 || strings.ContainsAny(name, "\\:") {
		return ""
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return ""
		}
	}
	return name
}

// Use an up hardware adapter's configured DNS and pin this socket to its
// interface. This does not modify any adapter, routing table or system DNS.
func dialPhysicalDNS(ctx context.Context, network string) (net.Conn, error) {
	return dialAdapterDNS(ctx, network, nil)
}

func dialAdapterDNS(ctx context.Context, network string, paths *physicalRoutes) (net.Conn, error) {
	size := uint32(15 * 1024)
	var data []byte
	var first *windows.IpAdapterAddresses
	for {
		data = make([]byte, size)
		first = (*windows.IpAdapterAddresses)(unsafe.Pointer(&data[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, 0, 0, first, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return nil, err
		}
		break
	}
	for a := first; a != nil; a = a.Next {
		row := windows.MibIfRow2{InterfaceLuid: a.Luid}
		if a.OperStatus != windows.IfOperStatusUp || windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row) != nil || row.InterfaceAndOperStatusFlags&1 == 0 {
			continue
		}
		for d := a.FirstDnsServerAddress; d != nil; d = d.Next {
			ip := d.Address.IP()
			if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
				continue
			}
			if paths != nil && (ip.To4() != nil && a.IfIndex != paths.ipv4.index || ip.To4() == nil && a.Ipv6IfIndex != paths.ipv6.index) {
				continue
			}
			host := ip.String()
			level, index := windows.IPPROTO_IP, bits.ReverseBytes32(a.IfIndex)
			if ip.To4() == nil {
				if ip[0] == 0xfe && ip[1]&0xc0 == 0xc0 { // Windows legacy site-local placeholder
					continue
				}
				level, index = windows.IPPROTO_IPV6, a.Ipv6IfIndex
				if ip.IsLinkLocalUnicast() {
					host += "%" + strconv.FormatUint(uint64(index), 10)
				}
			}
			dialer := net.Dialer{Control: func(_, _ string, raw syscall.RawConn) error {
				var sockErr error
				err := raw.Control(func(fd uintptr) {
					// IP_UNICAST_IF and IPV6_UNICAST_IF both use option 31.
					sockErr = windows.SetsockoptInt(windows.Handle(fd), level, 31, int(index))
				})
				return errors.Join(err, sockErr)
			}}
			if conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(host, "53")); err == nil {
				return conn, nil
			}
		}
	}
	return nil, errors.New("physical DNS unavailable")
}

func (h *splitDNS) exchange(ctx context.Context, dest xnet.Destination, query []byte) (reply []byte, result error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r := h.instance.GetFeature(routing.RouterType()).(routing.Router)
	var domestic bool
	if name := queryName(query); name != "" {
		// Reuse the same compiled domain matcher as HTTP and SOCKS. Port 80
		// avoids the port 53 intercept; AsIs never resolves an unmatched name.
		var in *session.Inbound
		if h.packet != nil {
			in = &session.Inbound{Tag: "tun-question"}
		}
		route, err := r.PickRoute(&routesession.Context{Inbound: in, Outbound: &session.Outbound{Target: xnet.TCPDestination(xnet.DomainAddress(name), 80)}})
		domestic = err == nil && route.GetOutboundTag() == "direct"
	}
	var conn net.Conn
	var err error
	if domestic {
		if h.learn {
			epoch := h.packet.epoch()
			defer func() {
				if result == nil {
					h.packet.remember(epoch, query, reply)
				}
			}()
		}
		conn, err = h.physical(ctx, dest.Network.SystemString())
	} else {
		// A remote exit cannot use a physical LAN resolver. Preserve an
		// explicit public destination and the isolated test resolver.
		if h.packet != nil && h.packet.mode == "full" && dest.Address.Family().IsIP() {
			ip := dest.Address.IP()
			if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				if ip.To4() != nil {
					dest.Address = xnet.ParseAddress("1.1.1.1")
				} else {
					dest.Address = xnet.ParseAddress("2606:4700:4700::1111")
				}
			}
		}
		ctx = session.ContextWithContent(ctx, &session.Content{})
		ctx = session.ContextWithOutbounds(ctx, nil)
		// The application-facing DNS link already accounts for these bytes.
		ctx = session.ContextWithInbound(ctx, nil)
		conn, err = core.Dial(session.SetForcedOutboundTagToContext(ctx, "selected"), h.instance, dest)
	}
	if err != nil {
		return nil, err
	}
	closeConn := sync.OnceFunc(func() { conn.Close() })
	stop := context.AfterFunc(ctx, closeConn)
	defer stop()
	defer closeConn()
	if dest.Network == xnet.Network_TCP {
		if err := writeFrame(conn, query); err != nil {
			return nil, err
		}
		return readFrame(conn)
	}
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}
	answer := make([]byte, 65535)
	n, err := conn.Read(answer)
	return answer[:n], err
}

func writeFrame(w io.Writer, p []byte) error {
	wire := binary.BigEndian.AppendUint16(nil, uint16(len(p)))
	_, err := w.Write(append(wire, p...))
	return err
}

func readFrame(r io.Reader) ([]byte, error) {
	var size [2]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	p := make([]byte, binary.BigEndian.Uint16(size[:]))
	_, err := io.ReadFull(r, p)
	return p, err
}

func (h *splitDNS) Dispatch(ctx context.Context, link *transport.Link) {
	defer common.Interrupt(link.Reader)
	defer common.Close(link.Writer)
	outs := session.OutboundsFromContext(ctx)
	dest := outs[len(outs)-1].Target
	if dest.Network == xnet.Network_TCP {
		reader := &buf.BufferedReader{Reader: link.Reader}
		defer reader.Close()
		writer := buf.NewBufferedWriter(link.Writer)
		for {
			query, err := readFrame(reader)
			if err != nil {
				return
			}
			answer, err := h.exchange(ctx, dest, query)
			if err != nil || writeFrame(writer, answer) != nil || writer.Flush() != nil {
				return
			}
		}
	}
	for {
		batch, err := link.Reader.ReadMultiBuffer()
		for len(batch) > 0 {
			var b *buf.Buffer
			batch, b = buf.SplitFirst(batch)
			source := dest
			if b.UDP != nil {
				source = *b.UDP
			}
			answer, err := h.exchange(ctx, source, b.Bytes())
			b.Release()
			if err == nil {
				reply := buf.FromBytes(answer)
				reply.UDP = &source
				if err := link.Writer.WriteMultiBuffer(buf.MultiBuffer{reply}); err != nil {
					buf.ReleaseMulti(batch)
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}
