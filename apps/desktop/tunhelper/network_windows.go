package main

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

const wireMTU = 1500

var noPhysicalDefault = errors.New("waiting for a physical default route")

func aliasFor(r *netRecord) string { return "Nexus-" + r.GUID[1:37] }

var fullAddr = []string{"198.18.0.1/32", "fd6e:6578:7573::1/128"}
var fullDNS = []string{"1.1.1.1", "2606:4700:4700::1111"}
var fullRoutes = []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"}
var testRoutes = []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24"}

func changesFor(mode string, resolvers []netip.Addr) ([]string, error) {
	if mode == "test" {
		return append([]string{"interface:4", "address:192.0.2.1/32"}, prefixed("route:", testRoutes)...), nil
	}
	if mode != "full" {
		return nil, unsafeNetwork
	}
	out := []string{"interface:4", "interface:6"}
	out = append(out, prefixed("address:", fullAddr)...)
	routes := map[netip.Prefix]bool{}
	for _, ip := range resolvers {
		ip = ip.Unmap()
		if !ip.IsValid() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
			return nil, unsafeNetwork
		}
		routes[netip.PrefixFrom(ip, ip.BitLen())] = true
	}
	if len(routes) > 128 {
		return nil, unsafeNetwork
	}
	extra := make([]string, 0, len(routes))
	for n := range routes {
		extra = append(extra, n.String())
	}
	slices.Sort(extra)
	out = append(out, prefixed("route:", extra)...)
	out = append(out, prefixed("route:", fullRoutes)...)
	return append(out, prefixed("dns:", fullDNS)...), nil
}
func prefixed(label string, values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = label + v
	}
	return out
}

type peerInterface struct {
	up, physical     bool
	metric4, metric6 uint32
	subnets          []netip.Prefix
}

func peerNetwork(ours winipcfg.LUID) (map[winipcfg.LUID]peerInterface, []netip.Addr, error) {
	all, err := winipcfg.GetAdaptersAddresses(windows.AF_UNSPEC, winipcfg.GAAFlagIncludeAllInterfaces)
	if err != nil {
		return nil, nil, err
	}
	peers := make(map[winipcfg.LUID]peerInterface, len(all))
	var ips []netip.Addr
	for _, a := range all {
		if a.LUID == ours {
			continue
		}
		p := peerInterface{up: a.OperStatus == winipcfg.IfOperStatusUp, metric4: a.Ipv4Metric, metric6: a.Ipv6Metric}
		if !p.up {
			peers[a.LUID] = p
			continue
		}
		if a.IfType == winipcfg.IfTypeEthernetCSMACD || a.IfType == winipcfg.IfTypeIEEE80211 {
			row, err := a.LUID.Interface()
			if err != nil {
				return nil, nil, err
			}
			p.physical = row.InterfaceAndOperStatusFlags&winipcfg.IAOSFHardwareInterface != 0
		}
		if p.physical {
			for u := a.FirstUnicastAddress; u != nil; u = u.Next {
				ip, ok := netip.AddrFromSlice(u.Address.IP())
				if !ok {
					return nil, nil, unsafeNetwork
				}
				n := netip.PrefixFrom(ip.Unmap(), int(u.OnLinkPrefixLength)).Masked()
				if n.IsValid() && n.Bits() >= 16 {
					p.subnets = append(p.subnets, n)
				}
			}
		}
		peers[a.LUID] = p
		for d := a.FirstDNSServerAddress; d != nil; d = d.Next {
			ip, ok := netip.AddrFromSlice(d.Address.IP())
			if !ok {
				return nil, nil, unsafeNetwork
			}
			// Windows exposes these defunct site-local placeholders on adapters without DNS.
			if ip.String() == "fec0:0:0:ffff::1" || ip.String() == "fec0:0:0:ffff::2" || ip.String() == "fec0:0:0:ffff::3" {
				continue
			}
			ips = append(ips, ip.Unmap())
		}
	}
	return peers, ips, nil
}
func nextHop(p netip.Prefix) netip.Addr {
	if p.Addr().Is4() {
		return netip.IPv4Unspecified()
	}
	return netip.IPv6Unspecified()
}
func absent(err error) bool {
	return errors.Is(err, windows.ERROR_NOT_FOUND) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_DEVICE_NOT_CONNECTED)
}

// Windows has connected LAN, loopback, and multicast rows on most machines.
// These ranges may stay on their original interfaces.
var localPrefixes = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("255.255.255.255/32"),
	netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("fe80::/10"), netip.MustParsePrefix("ff00::/8"),
}

func within(routes []netip.Prefix, n netip.Prefix) bool {
	return slices.ContainsFunc(routes, func(v netip.Prefix) bool { return n.Bits() >= v.Bits() && v.Contains(n.Addr()) })
}

func checkRoutes(id winipcfg.LUID, planned, existing []string, all []winipcfg.MibIPforwardRow2, peers map[winipcfg.LUID]peerInterface) error {
	found := map[string]bool{}
	type defaultPath struct {
		metric   uint64
		physical bool
	}
	best := map[bool]defaultPath{}
	for _, row := range all {
		n := row.DestinationPrefix.Prefix().Masked()
		if !n.IsValid() {
			return unsafeNetwork
		}
		key := "route:" + n.String()
		if row.InterfaceLUID != id && peers != nil {
			p, ok := peers[row.InterfaceLUID]
			if !ok {
				return routeConflict
			}
			if !p.up {
				continue
			}
			if n.Bits() == 0 {
				is4 := n.Addr().Is4()
				metric := uint64(row.Metric)
				if is4 {
					metric += uint64(p.metric4)
				} else {
					metric += uint64(p.metric6)
				}
				v, exists := best[is4]
				if !exists || metric < v.metric || metric == v.metric && !p.physical {
					best[is4] = defaultPath{metric, p.physical}
				}
			} else if !within(localPrefixes, n) && !(p.physical && row.NextHop.Addr().IsUnspecified() && within(p.subnets, n)) {
				// Includes VPN split-default /2, v6 /3 and public host exceptions via a gateway.
				return routeConflict
			}
		}
		if row.InterfaceLUID != id && peers == nil {
			for _, v := range testRoutes {
				target := netip.MustParsePrefix(v)
				if n.Bits() >= target.Bits() && target.Contains(n.Addr()) {
					return routeConflict
				}
			}
		}
		if !slices.Contains(planned, key) {
			continue
		}
		// Never displace another interface's equal-prefix route or adopt a foreign row.
		if row.InterfaceLUID != id || !slices.Contains(existing, key) || row.Metric != 0 || !row.NextHop.Addr().IsUnspecified() {
			return routeConflict
		}
		found[key] = true
	}
	if peers != nil {
		if len(best) == 0 {
			return noPhysicalDefault
		}
		for _, v := range best {
			if !v.physical {
				return routeConflict
			}
		}
	}
	for _, k := range existing {
		if strings.HasPrefix(k, "route:") && slices.Contains(planned, k) && !found[k] {
			return unsafeNetwork
		}
	}
	return nil
}
func applyChange(id winipcfg.LUID, op string) error {
	k, v, _ := strings.Cut(op, ":")
	switch k {
	case "address":
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return err
		}
		return id.AddIPAddress(p)
	case "route":
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return err
		}
		return id.AddRoute(p, nextHop(p), 0)
	case "interface":
		f := winipcfg.AddressFamily(windows.AF_INET)
		if v == "6" {
			f = windows.AF_INET6
		}
		row, err := id.IPInterface(f)
		if err != nil {
			return err
		}
		row.NLMTU = wireMTU
		row.UseAutomaticMetric = false
		row.Metric = 1
		// SetIpInterfaceEntry requires a zero IPv4 SitePrefixLength.
		if f == windows.AF_INET {
			row.SitePrefixLength = 0
		}
		return row.Set()
	case "dns":
		guid, err := id.GUID()
		if err != nil {
			return err
		}
		str, err := windows.UTF16PtrFromString(v)
		if err != nil {
			return err
		}
		ds := winipcfg.DnsInterfaceSettings{Version: winipcfg.DnsInterfaceSettingsVersion1, Flags: winipcfg.DnsInterfaceSettingsFlagNameserver, NameServer: str}
		ip, err := netip.ParseAddr(v)
		if err != nil {
			return err
		}
		if ip.Is6() {
			ds.Flags |= winipcfg.DnsInterfaceSettingsFlagIPv6
		}
		// Set only NameServer on our ephemeral adapter, preserving search lists.
		return winipcfg.SetInterfaceDnsSettings(*guid, &ds)
	}
	return unsafeNetwork
}

// All changes live on one freshly created GUID; no physical-interface value is
// mutated. Write intent before any mutation. Crash recovery removes exactly
// that device, which owns the IP Helper address/route/DNS rows.
func reconcile(current, desired []string, save func([]string) error, apply func(string) error, drop func(string) error) error {
	if slices.Equal(current, desired) {
		return nil
	}
	intent := slices.Clone(desired)
	for _, op := range current {
		if !slices.Contains(intent, op) {
			intent = append(intent, op)
		}
	}
	if err := save(intent); err != nil {
		return err
	}
	for _, op := range desired {
		if !slices.Contains(current, op) {
			if err := apply(op); err != nil {
				return err
			}
		}
	}
	for _, op := range current {
		if !slices.Contains(desired, op) {
			if err := drop(op); err != nil {
				return err
			}
		}
	}
	if len(intent) != len(desired) {
		return save(desired)
	}
	return nil
}
func configure(k registry.Key, r *netRecord) error {
	id := winipcfg.LUID(r.LUID)
	var peers map[winipcfg.LUID]peerInterface
	var dns []netip.Addr
	var err error
	if r.Mode == "full" {
		peers, dns, err = peerNetwork(id)
		if err != nil {
			return err
		}
	}
	wanted, err := changesFor(r.Mode, dns)
	if err != nil {
		return err
	}
	rows, err := winipcfg.GetIPForwardTable2(windows.AF_UNSPEC)
	if err != nil {
		return err
	}
	if err := checkRoutes(id, wanted, r.Changes, rows, peers); err != nil {
		return err
	}
	return reconcile(r.Changes, wanted, func(x []string) error { r.Changes = x; return commitJournal(k, r) }, func(op string) error { return applyChange(id, op) }, func(op string) error {
		_, v, _ := strings.Cut(op, ":")
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return err
		}
		// Only obsolete resolver host routes can leave the plan while running.
		if p.Bits() != p.Addr().BitLen() {
			return unsafeNetwork
		}
		row, err := id.Route(p, nextHop(p))
		if absent(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.Metric != 0 {
			return unsafeNetwork
		}
		return id.DeleteRoute(p, nextHop(p))
	})
}

// A Wintun created by this process is normally removed when its owner closes.
// If an abrupt exit left a PnP device, require the stored GUID, LUID (when
// assigned), expected alias and Wintun hardware ID before removing it.
func removeOwnedAdapter(r *netRecord) error {
	guid, e := windows.GUIDFromString(r.GUID)
	if e != nil {
		return e
	}
	luid, e := winipcfg.LUIDFromGUID(&guid)
	if e != nil && !absent(e) {
		return e
	}
	if e == nil {
		if r.LUID != 0 && uint64(luid) != r.LUID {
			return staleNetwork
		}
		row, err := luid.Interface()
		if err != nil && !absent(err) {
			return err
		}
		if err == nil && row.Alias() != aliasFor(r) {
			return staleNetwork
		}
	}
	netClass, _ := windows.GUIDFromString("{4D36E972-E325-11CE-BFC1-08002BE10318}")
	devices, err := windows.SetupDiGetClassDevsEx(&netClass, "", 0, 0, 0, "")
	if err != nil {
		return err
	}
	defer devices.Close()
	for i := 0; ; i++ {
		d, err := devices.EnumDeviceInfo(i)
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			break
		}
		if err != nil {
			return err
		}
		hw, err := devices.DeviceRegistryProperty(d, windows.SPDRP_HARDWAREID)
		if errors.Is(err, windows.ERROR_INVALID_DATA) || absent(err) {
			continue
		}
		if err != nil {
			return err
		}
		ids, ok := hw.([]string)
		if !ok || !slices.ContainsFunc(ids, func(s string) bool { return strings.EqualFold(s, "Wintun") }) {
			continue
		}
		k, err := devices.OpenDevRegKey(d, windows.DICS_FLAG_GLOBAL, 0, windows.DIREG_DRV, registry.QUERY_VALUE)
		if absent(err) || errors.Is(err, windows.ERROR_KEY_DOES_NOT_EXIST) {
			continue
		}
		if err != nil {
			return err
		}
		key := registry.Key(k)
		val, _, err := key.GetStringValue("NetCfgInstanceId")
		key.Close()
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !strings.EqualFold(val, r.GUID) {
			continue
		}
		params := windows.RemoveDeviceParams{ClassInstallHeader: *windows.MakeClassInstallHeader(windows.DIF_REMOVE), Scope: windows.DI_REMOVEDEVICE_GLOBAL}
		if err := devices.SetClassInstallParams(d, &params.ClassInstallHeader, uint32(unsafe.Sizeof(params))); err != nil {
			return err
		}
		if err := devices.CallClassInstaller(windows.DIF_REMOVE, d); err != nil && !absent(err) {
			return err
		}
		break
	}
	// PnP removal can be asynchronous or pending a reboot. Keep the
	// journal until the IP interface and its routing state have disappeared.
	deadline := time.Now().Add(3 * time.Second)
	for {
		id, err := winipcfg.LUIDFromGUID(&guid)
		if err == nil {
			_, err = id.Interface()
		}
		if absent(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return staleNetwork
		}
		time.Sleep(100 * time.Millisecond)
	}
}
