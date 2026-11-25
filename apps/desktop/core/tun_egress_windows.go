package main

import (
	"context"
	"errors"
	"math/bits"
	"net"
	"net/netip"
	"strings"
	"syscall"

	v2transport "github.com/xtls/libxray/internal/nexusv2/transport"
	"github.com/xtls/xray-core/transport/internet"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

type physicalPath struct {
	index   uint32
	gateway netip.Addr
}

type physicalRoutes struct{ ipv4, ipv6 physicalPath }

// The TUN owns more-specific capture routes. Pick the lowest real physical
// default independently for each address family without consulting those routes.
func currentPhysicalRoutes() physicalRoutes {
	var routes physicalRoutes
	table, err := winipcfg.GetIPForwardTable2(windows.AF_UNSPEC)
	if err != nil {
		return routes
	}
	var cost4, cost6 uint64
	for _, r := range table {
		if r.DestinationPrefix.PrefixLength != 0 || r.Loopback || r.ValidLifetime == 0 {
			continue
		}
		hw := windows.MibIfRow2{InterfaceLuid: uint64(r.InterfaceLUID)}
		if windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &hw) != nil ||
			hw.OperStatus != windows.IfOperStatusUp || hw.InterfaceAndOperStatusFlags&1 == 0 {
			continue
		}
		family := r.DestinationPrefix.RawPrefix.Family
		info, err := r.InterfaceLUID.IPInterface(winipcfg.AddressFamily(family))
		if err != nil || !info.Connected || info.WeakHostSend || info.ForwardingEnabled {
			continue
		}
		path, cost := &routes.ipv4, &cost4
		if family == windows.AF_INET6 {
			path, cost = &routes.ipv6, &cost6
		}
		total := uint64(info.Metric) + uint64(r.Metric)
		if path.index == 0 || total < *cost || total == *cost && r.InterfaceIndex < path.index {
			*path, *cost = physicalPath{r.InterfaceIndex, r.NextHop.Addr()}, total
		}
	}
	return routes
}

func physicalSocket(network, address string, raw syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return errors.New("unresolved physical peer")
	}
	peer := ap.Addr().Unmap()
	routes := currentPhysicalRoutes()
	if peer.IsLoopback() {
		// Even a UDP socket initially addressed to loopback may later receive
		// another datagram destination. Restrict it to the actual loopback NIC.
		ifaces, err := net.Interfaces()
		if err != nil {
			return err
		}
		routes = physicalRoutes{}
		for _, iface := range ifaces {
			if iface.Flags&net.FlagLoopback != 0 {
				routes.ipv4.index, routes.ipv6.index = uint32(iface.Index), uint32(iface.Index)
				break
			}
		}
	}
	if peer.Is4() && routes.ipv4.index == 0 || peer.Is6() && routes.ipv6.index == 0 {
		return errors.New("no physical route for peer")
	}
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		h := windows.Handle(fd)
		// Go passes the actual descriptor family (including dual-stack udp6).
		if strings.HasSuffix(network, "4") {
			sockErr = windows.SetsockoptInt(h, windows.IPPROTO_IP, 31, int(bits.ReverseBytes32(routes.ipv4.index)))
			return
		}
		if !strings.HasSuffix(network, "6") {
			sockErr = errors.New("unknown socket family")
			return
		}
		// A dual-stack socket may address both families after initial dispatch.
		// Bind both or let pinned Xray retry with a single-family descriptor.
		index6 := routes.ipv6.index
		if index6 == 0 {
			index6 = routes.ipv4.index
		}
		if sockErr = windows.SetsockoptInt(h, windows.IPPROTO_IPV6, 31, int(index6)); sockErr != nil {
			return
		}
		var only int
		only, sockErr = windows.GetsockoptInt(h, windows.IPPROTO_IPV6, windows.IPV6_V6ONLY)
		if sockErr != nil || only != 0 {
			return
		}
		if routes.ipv4.index == 0 {
			sockErr = windows.SetsockoptInt(h, windows.IPPROTO_IPV6, windows.IPV6_V6ONLY, 1)
		} else {
			sockErr = windows.SetsockoptInt(h, windows.IPPROTO_IP, 31, int(bits.ReverseBytes32(routes.ipv4.index)))
		}
	})
	if err != nil {
		return err
	}
	if sockErr != nil {
		return internet.NewBindError(sockErr)
	}
	return nil
}

// A session has its own process. Register all controls before the first node
// socket opens and before asking the helper to install capture routes.
func protectTunEgress() error {
	if err := internet.RegisterDialerController(physicalSocket); err != nil {
		return err
	}
	v2transport.DesktopSocketControl = physicalSocket
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialPreferredDNS(ctx, network)
	}}
	return nil
}

func dialPreferredDNS(ctx context.Context, network string) (net.Conn, error) {
	paths := currentPhysicalRoutes()
	return dialAdapterDNS(ctx, network, &paths)
}
