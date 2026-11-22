package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"strconv"
	"syscall"
	"time"

	quic "github.com/apernet/quic-go"
	utls "github.com/refraction-networking/utls"
	"github.com/shejiang000-prog/Nexus/v2/internal/congestion/bbr"
)

// Control runs while the descriptor is owned by the dialer, before connect or bind.
func socketControl(protect func(int) bool) func(string, string, syscall.RawConn) error {
	if protect == nil {
		return nil
	}
	return func(_, _ string, raw syscall.RawConn) error {
		ok := false
		if err := raw.Control(func(fd uintptr) { ok = protect(int(fd)) }); err != nil {
			return &Error{Configuration, err}
		}
		if !ok {
			return &Error{Configuration, errors.New("socket protection refused")}
		}
		return nil
	}
}
func dialTCP(ctx context.Context, addr, name string, raw []byte, roots *x509.CertPool, protect func(int) bool) (*utls.UConn, error) {
	var spec *utls.ClientHelloSpec
	var e error
	if len(raw) == 0 {
		var preset utls.ClientHelloSpec
		// Keep the Chrome base explicit across desktop and Android builds.
		preset, e = utls.UTLSIdToSpec(utls.HelloChrome_133)
		if e == nil {
			for _, ext := range preset.Extensions {
				if sigs, ok := ext.(*utls.SignatureAlgorithmsExtension); ok {
					sigs.SupportedSignatureAlgorithms = append([]utls.SignatureScheme(nil), chrome157Signatures[:]...)
					// uTLS writes signature IDs verbatim, including the GREASE slot.
					sigs.SupportedSignatureAlgorithms[0] = utls.SignatureScheme(0x0a0a + 0x1010*rand.Uint32N(16))
				}
			}
			// Preserve the trailing GREASE extension when shuffling the preset.
			i := len(preset.Extensions) - 1
			preset.Extensions = append(preset.Extensions[:i], &utls.GenericExtension{Id: 0xca34, Data: chrome157Anchors[:]}, preset.Extensions[i])
		}
		spec = &preset
	} else {
		fp := utls.Fingerprinter{AllowBluntMimicry: true}
		spec, e = fp.FingerprintClientHello(raw)
	}
	if e != nil {
		return nil, e
	}
	for _, ext := range spec.Extensions {
		if r, ok := ext.(*utls.RenegotiationInfoExtension); ok {
			r.Renegotiation = utls.RenegotiateNever
		}
		if g, ok := ext.(*utls.GenericExtension); ok && g.Id != 0xca34 {
			return nil, fmt.Errorf("unrecognized captured extension %x", g.Id)
		}
	}
	spec.Extensions = utls.ShuffleChromeTLSExtensions(spec.Extensions)
	sock, e := (&net.Dialer{Control: socketControl(protect)}).DialContext(ctx, "tcp", addr)
	if e != nil {
		return nil, e
	}
	c := utls.UClient(sock, &utls.Config{ServerName: name, RootCAs: roots}, utls.HelloCustom)
	if e = c.ApplyPreset(spec); e == nil {
		e = c.HandshakeContext(ctx)
	}
	if e != nil {
		sock.Close()
		return nil, e
	}
	if c.ConnectionState().NegotiatedProtocol != "h2" || c.ConnectionState().Version != tls.VersionTLS13 {
		sock.Close()
		return nil, errors.New("TLS 1.3 and h2 required")
	}
	return c, nil
}

func dialQUIC(ctx context.Context, peer *net.UDPAddr, name string, roots *x509.CertPool, protect func(int) bool) (*quic.Conn, func(), error) {
	family, bind := "udp4", "0.0.0.0:0"
	if peer.IP.To4() == nil {
		family, bind = "udp6", "[::]:0"
	}
	sock, e := (&net.ListenConfig{Control: socketControl(protect)}).ListenPacket(ctx, family, bind)
	if e != nil {
		return nil, nil, e
	}
	tr := &quic.Transport{Conn: sock, ConnectionIDGenerator: quic.ZeroLengthConnectionIDGenerator{}}
	closeAll := func() { tr.Close(); sock.Close() }
	c, e := tr.Dial(ctx, peer, &tls.Config{RootCAs: roots, ServerName: name, NextProtos: []string{"h3"}}, &quic.Config{EnableDatagrams: true, ChromeParrot: true})
	if e != nil {
		closeAll()
		return nil, nil, e
	}
	size := bbr.GetInitialPacketSize(c.RemoteAddr())
	if x := c.InitialPacketSize(); x > 0 {
		size = min(size, x)
	}
	c.SetCongestionControl(bbr.NewBbrSender(bbr.DefaultClock{}, size, bbr.ProfileStandard))
	return c, closeAll, nil
}

func dialEndpoint(ctx context.Context, c Config, dial func(context.Context, netip.AddrPort) (*transportLane, error)) (*transportLane, error) {
	host, port, err := net.SplitHostPort(c.Endpoint)
	if err != nil {
		return nil, err
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(host)
	if err == nil {
		return dial(ctx, netip.AddrPortFrom(ip, uint16(n)))
	}
	var ips []netip.Addr
	if c.Resolve != nil {
		ips, err = c.Resolve(ctx, host)
	} else {
		ips, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
	if err != nil {
		return nil, &Error{Interrupted, err}
	}
	if len(ips) == 0 {
		return nil, &Error{Interrupted, errors.New("empty node address list")}
	}
	for _, ip := range ips {
		if !ip.IsValid() {
			return nil, &Error{Interrupted, errors.New("invalid node address")}
		}
	}
	for i, ip := range ips {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		attempt := ctx
		cancel := func() {}
		if end, ok := ctx.Deadline(); ok && len(ips)-i > 1 {
			attempt, cancel = context.WithTimeout(ctx, time.Until(end)/time.Duration(len(ips)-i))
		}
		lane, e := dial(attempt, netip.AddrPortFrom(ip, uint16(n)))
		cancel()
		if e == nil || terminalDialError(e) {
			return lane, e
		}
		err = e
	}
	return nil, err
}
