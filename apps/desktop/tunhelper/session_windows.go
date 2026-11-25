package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"sync"
	"time"

	dnsguard "github.com/xtls/libxray/cmd/nexus-tun-helper/dnsguard"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.zx2c4.com/wintun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// A reply from the ordinary user's core may be delivered only to an address
// assigned to this session's adapter. Never accept arbitrary injected IP.
func validPacket(p []byte, mode string, reply bool) bool {
	if len(p) < 20 || len(p) > wireMTU {
		return false
	}
	var dst netip.Addr
	switch p[0] >> 4 {
	case 4:
		hdr := int(p[0]&15) * 4
		if hdr < 20 || hdr > len(p) || int(binary.BigEndian.Uint16(p[2:4])) != len(p) {
			return false
		}
		dst = netip.AddrFrom4([4]byte(p[16:20]))
	case 6:
		if mode == "test" || len(p) < 40 || 40+int(binary.BigEndian.Uint16(p[4:6])) != len(p) {
			return false
		}
		dst = netip.AddrFrom16([16]byte(p[24:40]))
	default:
		return false
	}
	if reply {
		if mode == "test" {
			return dst == netip.MustParseAddr("192.0.2.1")
		}
		return dst == netip.MustParseAddr("198.18.0.1") || dst == netip.MustParseAddr("fd6e:6578:7573::1")
	}
	if mode == "test" {
		for _, v := range testRoutes {
			if netip.MustParsePrefix(v).Contains(dst) {
				return true
			}
		}
		return false
	}
	return true
}
func writeRing(ctx context.Context, s wintun.Session, pipe io.Reader, mode string) error {
	var buf [wireMTU]byte
	for ctx.Err() == nil {
		if _, err := io.ReadFull(pipe, buf[:2]); err != nil {
			return err
		}
		n := int(binary.BigEndian.Uint16(buf[:2]))
		if n > len(buf) || n < 20 {
			return unsafeNetwork
		}
		p := buf[:n]
		if _, err := io.ReadFull(pipe, p); err != nil {
			return err
		}
		if !validPacket(p, mode, true) {
			return unsafeNetwork
		}
		packet, err := s.AllocateSendPacket(n)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if err != nil {
			return err
		}
		copy(packet, p)
		s.SendPacket(packet)
	}
	return ctx.Err()
}
func readRing(ctx context.Context, s wintun.Session, pipe io.Writer, mode string) error {
	var frame [2 + wireMTU]byte
	for ctx.Err() == nil {
		packet, err := s.ReceivePacket()
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			_, err = windows.WaitForSingleObject(s.ReadWaitEvent(), 150)
			if err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		n := len(packet)
		ok := validPacket(packet, mode, false)
		if ok {
			copy(frame[2:], packet)
		}
		s.ReleaseReceivePacket(packet)
		if !ok {
			continue
		}
		binary.BigEndian.PutUint16(frame[:2], uint16(n))
		count, err := pipe.Write(frame[:2+n])
		if err != nil {
			return err
		}
		if count != 2+n {
			return io.ErrShortWrite
		}
	}
	return ctx.Err()
}

func recoverLocked(k registry.Key, owner string) error {
	r, err := loadJournal(k)
	if err != nil {
		return err
	}
	return finishRecovery(r, owner, removeOwnedAdapter, func() error { return commitJournal(k, nil) })
}

func finishRecovery(r *netRecord, owner string, remove func(*netRecord) error, clear func() error) error {
	if r == nil {
		return nil
	}
	if r.OwnerSID != owner {
		return staleNetwork
	}
	if err := remove(r); err != nil {
		return errors.Join(staleNetwork, err)
	}
	return clear()
}
func recoverNetwork(ownerSID string) error {
	unlock, err := lockNetwork()
	if err != nil {
		return err
	}
	defer unlock()
	k, err := openJournal()
	if err != nil {
		return err
	}
	defer k.Close()
	return recoverLocked(k, ownerSID)
}
func runNetwork(ctx context.Context, mode, ownerSID string, pipe io.ReadWriteCloser, ready func() error) (result error) {
	if mode != "full" && mode != "test" {
		return unsafeNetwork
	}
	unlock, err := lockNetwork()
	if err != nil {
		return err
	}
	defer unlock()
	k, err := openJournal()
	if err != nil {
		return err
	}
	defer k.Close()
	if err = recoverLocked(k, ownerSID); err != nil {
		return err
	}
	r, err := makeRecord(ownerSID, mode)
	if err != nil {
		return err
	}
	if err = commitJournal(k, &r); err != nil {
		return err
	}
	var closeGuard func()
	defer func() {
		result = errors.Join(result, recoverLocked(k, ownerSID))
		if closeGuard != nil {
			closeGuard()
		}
	}()
	guid, _ := windows.GUIDFromString(r.GUID)
	adapter, err := wintun.CreateAdapter(aliasFor(&r), "Nexus", &guid)
	if err != nil {
		return err
	}
	defer adapter.Close()
	r.LUID = adapter.LUID()
	id := winipcfg.LUID(r.LUID)
	actual, err := id.GUID()
	if err != nil || *actual != guid {
		return staleNetwork
	}
	if err = commitJournal(k, &r); err != nil {
		return err
	}
	if mode == "full" {
		closeGuard, err = dnsguard.OpenDesktopDNSGuard(id)
		if err != nil {
			return errors.Join(unsafeNetwork, err)
		}
	}
	s, err := adapter.StartSession(8 << 20)
	if err != nil {
		return err
	}
	defer s.End()
	changed := make(chan struct{}, 1)
	note := func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	if mode == "full" {
		iface, err := winipcfg.RegisterInterfaceChangeCallback(func(_ winipcfg.MibNotificationType, _ *winipcfg.MibIPInterfaceRow) { note() })
		if err != nil {
			return err
		}
		defer iface.Unregister()
		route, err := winipcfg.RegisterRouteChangeCallback(func(_ winipcfg.MibNotificationType, _ *winipcfg.MibIPforwardRow2) { note() })
		if err != nil {
			return err
		}
		defer route.Unregister()
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// PnP may publish the Wintun handle before the IP stack has attached.
	until := time.Now().Add(4 * time.Second)
	for {
		_, e := id.IPInterface(windows.AF_INET)
		if e == nil && mode == "full" {
			_, e = id.IPInterface(windows.AF_INET6)
		}
		if e == nil {
			break
		}
		if !absent(e) || time.Now().After(until) {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(80 * time.Millisecond):
		}
	}
	if err = configure(k, &r); err != nil {
		return err
	}
	if err = ready(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 2)
	var tasks sync.WaitGroup
	tasks.Add(2)
	go func() { defer tasks.Done(); done <- readRing(ctx, s, pipe, mode) }()
	go func() { defer tasks.Done(); done <- writeRing(ctx, s, pipe, mode) }()
	defer func() { cancel(); pipe.Close(); tasks.Wait() }()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-done:
			if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, windows.ERROR_BROKEN_PIPE) {
				return nil
			}
			return err
		case <-changed:
		case <-tick.C:
			if mode != "full" {
				continue
			}
		}
		// A Wi-Fi/Ethernet handover can temporarily leave no default route.
		// Keep capture and DNS protection until the next network notification.
		if err := configure(k, &r); err != nil && !errors.Is(err, noPhysicalDefault) {
			return err
		}
	}
}
