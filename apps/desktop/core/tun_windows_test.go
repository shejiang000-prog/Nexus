package main

import (
	"context"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"

	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy/tun"
	"github.com/xtls/xray-core/transport"
	"golang.org/x/sys/windows"
)

func TestDesktopTunMaintenance(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	exe := filepath.Join(app, "nexus-core.exe")
	file := filepath.Join(root, "maintenance.lock")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	first, err := installationLock(exe, app)
	if err != nil || first == 0 {
		t.Fatal("installed core lock", err)
	}
	second, err := installationLock(exe, app)
	if err != nil {
		t.Fatal(err)
	}
	windows.CloseHandle(second)
	name, _ := windows.UTF16PtrFromString(file)
	exclusive := func() (windows.Handle, error) {
		return windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	}
	if h, err := exclusive(); err != windows.ERROR_SHARING_VIOLATION {
		windows.CloseHandle(h)
		t.Fatal("installer could lock during core lifetime", err)
	}
	windows.CloseHandle(first)
	h, err := exclusive()
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if _, err := installationLock(exe, app); err != windows.ERROR_SHARING_VIOLATION {
		t.Fatal("core started during maintenance", err)
	}
	if h, err := installationLock(filepath.Join(t.TempDir(), "nexus-core.exe"), app); h != 0 || err != nil {
		t.Fatal("development checkout used installed lock")
	}
}

// Both endpoints of this test are in-process pinned Xray packet stacks. No
// adapter, route, physical resolver or listening socket is used.
type echoOutbound struct {
	splitDNS
	tag string
}

func (h *echoOutbound) Tag() string { return h.tag }
func (h *echoOutbound) Dispatch(ctx context.Context, link *transport.Link) {
	defer common.Interrupt(link.Reader)
	defer common.Close(link.Writer)
	outs := session.OutboundsFromContext(ctx)
	for {
		mb, err := link.Reader.ReadMultiBuffer()
		for len(mb) > 0 {
			var b *buf.Buffer
			mb, b = buf.SplitFirst(mb)
			dest := outs[len(outs)-1].Target
			if b.UDP != nil {
				dest = *b.UDP
			}
			payload := []byte(h.tag)
			if dest.Port == 53 {
				payload = fakeDNSAnswer(b.Bytes())
			}
			b.Release()
			reply := buf.FromBytes(payload)
			reply.UDP = &dest
			if link.Writer.WriteMultiBuffer(buf.MultiBuffer{reply}) != nil {
				buf.ReleaseMulti(mb)
				return
			}
		}
		if err != nil {
			return
		}
	}
}
func fakeDNSAnswer(wire []byte) []byte {
	var q dns.Msg
	if q.Unpack(wire) != nil {
		return nil
	}
	a := new(dns.Msg).SetReply(&q)
	a.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 15}, A: net.IPv4(8, 8, 4, 4)}}
	p, _ := a.Pack()
	return p
}

func TestDesktopTunPackets(t *testing.T) {
	for _, mode := range []string{"split", "global"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			s := &packetSession{mode: "test", learned: make(map[netip.Addr]time.Time)}
			cfg, err := proxyConfig(21808, 21809, mode)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Inbound = nil
			if err = s.configure(cfg, mode); err != nil {
				t.Fatal(err)
			}
			inst, err := core.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Close()
			manager := inst.GetFeature(outbound.ManagerType()).(outbound.Manager)
			for _, tag := range []string{"selected", "direct"} {
				old := manager.GetHandler(tag)
				if err = manager.RemoveHandler(ctx, tag); err != nil {
					t.Fatal(err)
				}
				old.Close()
				if err = manager.AddHandler(ctx, &echoOutbound{tag: tag}); err != nil {
					t.Fatal(err)
				}
			}
			local, peer := net.Pipe()
			defer peer.Close()
			err = s.setupWith(inst, mode, func(string) (io.ReadWriteCloser, error) { return local, nil })
			if err != nil {
				t.Fatal(err)
			}
			domestic := make(chan struct{}, 1)
			manager.GetHandler("tun-dns").(*splitDNS).physical = func(_ context.Context, network string) (net.Conn, error) {
				left, right := net.Pipe()
				go func() {
					defer right.Close()
					var q []byte
					if network == "tcp" {
						q, _ = readFrame(right)
					} else {
						b := make([]byte, 512)
						n, _ := right.Read(b)
						q = b[:n]
					}
					if queryName(q) != "www.baidu.com" {
						return
					}
					domestic <- struct{}{}
					if network == "tcp" {
						writeFrame(right, fakeDNSAnswer(q))
					} else {
						right.Write(fakeDNSAnswer(q))
					}
				}()
				return left, nil
			}
			if err = inst.Start(); err != nil {
				t.Fatal(err)
			}
			remoteDev := &packetDevice{pipe: peer, done: make(chan struct{})}
			remote, err := tun.NewStack(ctx, tun.StackOptions{Tun: remoteDev, MTU: tunMTU, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer remote.Close()
			if err = remote.Start(); err != nil {
				t.Fatal(err)
			}
			udp, err := remote.DialUDP()
			if err != nil {
				t.Fatal(err)
			}
			defer udp.Close()
			exchange := func(dst string, p []byte) []byte {
				t.Helper()
				addr := net.UDPAddrFromAddrPort(netip.MustParseAddrPort(dst))
				if _, err := udp.WriteTo(p, addr); err != nil {
					t.Fatal(err)
				}
				type response struct {
					p   []byte
					src net.Addr
					err error
				}
				result := make(chan response, 1)
				go func() { b := make([]byte, 512); n, src, err := udp.ReadFrom(b); result <- response{b[:n], src, err} }()
				select {
				case r := <-result:
					if r.err != nil || r.src.String() != dst {
						t.Fatalf("reply from %v: %v", r.src, r.err)
					}
					return r.p
				case <-ctx.Done():
					t.Fatalf("no packet reply for %s", dst)
					return nil
				}
			}
			// Reuse one source port across selected, direct, remote DNS and learned IP.
			for _, dst := range []string{"192.0.2.8:1001", "198.51.100.9:1001", "203.0.113.10:1001"} {
				if got := string(exchange(dst, []byte{1})); got != "selected" {
					t.Fatalf("test prefix went %s", got)
				}
			}
			if got := string(exchange("192.168.3.1:1001", []byte{2})); got != "direct" {
				t.Fatalf("LAN went %s", got)
			}
			q := new(dns.Msg).SetQuestion("remote.invalid.", dns.TypeA)
			p, _ := q.Pack()
			var a dns.Msg
			if a.Unpack(exchange("198.51.100.9:53", p)) != nil || a.Id != q.Id {
				t.Fatal("remote DNS failed")
			}
			q.SetQuestion("www.baidu.com.", dns.TypeA)
			p, _ = q.Pack()
			answer := []byte(nil)
			if mode == "global" {
				c, err := remote.DialTCP(ctx, netip.MustParseAddrPort("198.51.100.9:53"))
				if err != nil {
					t.Fatal(err)
				}
				stop := context.AfterFunc(ctx, func() { c.Close() })
				if err = writeFrame(c, p); err == nil {
					answer, err = readFrame(c)
				}
				stop()
				c.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else {
				answer = exchange("198.51.100.9:53", p)
			}
			if a.Unpack(answer) != nil || a.Id != q.Id {
				t.Fatal("domestic DNS failed")
			}
			select {
			case <-domestic:
			default:
				t.Fatal("domestic DNS was not physically routed")
			}
			want := "selected"
			if mode == "split" {
				want = "direct"
			}
			if got := string(exchange("8.8.4.4:1001", []byte{3})); got != want {
				t.Fatalf("learned IP went %s want %s", got, want)
			}
			peer.Close()
			select {
			case <-s.done():
			case <-ctx.Done():
				t.Fatal("helper EOF did not close the session")
			}
		})
	}
}
