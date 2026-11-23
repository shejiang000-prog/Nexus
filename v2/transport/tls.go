package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"github.com/shejiang000-prog/Nexus/v2/proof"
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

// For DNS names normal PKI runs first. For an invited IP the exact SPKI is the
// trust anchor. Still require a matching IP SAN, current validity and server usage.
func pinVerifier(name string, pin []byte) (bool, func([][]byte, [][]*x509.Certificate) error) {
	if len(pin) == 0 {
		return false, nil
	}
	ip := net.ParseIP(name) != nil
	return ip, func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) == 0 {
			return &Error{Certificate, errors.New("missing peer certificate")}
		}
		leaf, err := x509.ParseCertificate(raw[0])
		if err != nil {
			return &Error{Certificate, err}
		}
		digest := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
		if !bytes.Equal(pin, digest[:]) {
			return &Error{Certificate, errors.New("certificate pin mismatch")}
		}
		if ip {
			roots := x509.NewCertPool()
			roots.AddCert(leaf)
			if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: name}); err != nil {
				return &Error{Certificate, err}
			}
		}
		return nil
	}
}

// DialEnrollment opens the same H2/TLS carrier as a business connection and
// returns the exporter of that particular handshake for the invitation proof.
func DialEnrollment(ctx context.Context, addr, name string, roots *x509.CertPool, pin []byte) (net.Conn, proof.Exporter, error) {
	if (len(pin) != 0 && len(pin) != sha256.Size) || (net.ParseIP(name) != nil && len(pin) != sha256.Size) {
		return nil, nil, &Error{Certificate, errors.New("invalid certificate pin")}
	}
	conn, err := dialTCP(ctx, addr, name, nil, roots, nil, pin)
	if err != nil {
		return nil, nil, networkError(err)
	}
	state := conn.ConnectionState()
	return conn, state.ExportKeyingMaterial, nil
}
func dialTCP(ctx context.Context, addr, name string, raw []byte, roots *x509.CertPool, protect func(int) bool, pin []byte) (*utls.UConn, error) {
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
	pinnedIP, verify := pinVerifier(name, pin)
	c := utls.UClient(sock, &utls.Config{ServerName: name, RootCAs: roots, InsecureSkipVerify: pinnedIP, VerifyPeerCertificate: verify}, utls.HelloCustom)
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

func dialQUIC(ctx context.Context, peer *net.UDPAddr, name string, roots *x509.CertPool, protect func(int) bool, pin []byte) (*quic.Conn, func(), func() packetCounters, error) {
	family, bind := "udp4", "0.0.0.0:0"
	if peer.IP.To4() == nil {
		family, bind = "udp6", "[::]:0"
	}
	sock, e := (&net.ListenConfig{Control: socketControl(protect)}).ListenPacket(ctx, family, bind)
	if e != nil {
		return nil, nil, nil, e
	}
	tr := &quic.Transport{Conn: sock, ConnectionIDGenerator: quic.ZeroLengthConnectionIDGenerator{}}
	closeAll := func() { tr.Close(); sock.Close() }
	pinnedIP, verify := pinVerifier(name, pin)
	c, e := tr.Dial(ctx, peer, &tls.Config{RootCAs: roots, ServerName: name, InsecureSkipVerify: pinnedIP, VerifyPeerCertificate: verify, NextProtos: []string{"h3"}}, &quic.Config{EnableDatagrams: true, ChromeParrot: true})
	if e != nil {
		closeAll()
		return nil, nil, nil, e
	}
	size := bbr.GetInitialPacketSize(c.RemoteAddr())
	if x := c.InitialPacketSize(); x > 0 {
		size = min(size, x)
	}
	sender := bbr.NewBbrSender(bbr.DefaultClock{}, size, bbr.ProfileStandard)
	c.SetCongestionControl(sender)
	packets := func() packetCounters {
		return packetCounters{c.ConnectionStats().PacketsSent, sender.DeclaredLostPackets()}
	}
	return c, closeAll, packets, nil
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
