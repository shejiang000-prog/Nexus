package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"

	quic "github.com/apernet/quic-go"
	utls "github.com/refraction-networking/utls"
	"github.com/shejiang000-prog/Nexus/v2/internal/congestion/bbr"
)

func dialTCP(ctx context.Context, addr, name string, raw []byte, roots *x509.CertPool) (*utls.UConn, error) {
	var spec *utls.ClientHelloSpec
	var e error
	if len(raw) == 0 {
		var preset utls.ClientHelloSpec
		preset, e = utls.UTLSIdToSpec(utls.HelloChrome_Auto)
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
	sock, e := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
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

func dialQUIC(ctx context.Context, addr, name string, roots *x509.CertPool) (*quic.Conn, func(), error) {
	host, port, e := net.SplitHostPort(addr)
	if e != nil {
		return nil, nil, e
	}
	ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if e == nil && len(ips) == 0 {
		e = errors.New("empty address list")
	}
	if e != nil {
		return nil, nil, e
	}
	n, _ := strconv.Atoi(port)
	peer := &net.UDPAddr{IP: net.IP(ips[0].AsSlice()), Port: n}
	family := "udp4"
	if peer.IP.To4() == nil {
		family = "udp6"
	}
	sock, e := net.ListenUDP(family, nil)
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
