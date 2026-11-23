// Package mobile is the gomobile surface. The Java signer retains its Keystore key.
package mobile

import (
	"context"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"io"
	"math/big"
	"net/netip"
	"strings"

	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/transport"
)

type Signer interface {
	Sign(message []byte) ([]byte, error)
}
type Resolver interface {
	Resolve(host string) (string, error)
}
type Protector interface{ Protect(fd int32) bool }

// ErrorCode maps a gomobile exception back to its original Go error.
func ErrorCode(err error) int32 {
	var e *transport.Error
	if errors.As(err, &e) {
		return int32(e.Code)
	}
	if err != nil {
		return int32(transport.Interrupted)
	}
	return 0
}

type Observer interface{ StateChanged(state string) }
type Client struct {
	client *transport.Client
	ctx    context.Context
	cancel context.CancelFunc
}
type Association struct{ a *transport.Association }
type Stream struct{ s io.ReadWriteCloser }

func rawSignature(b []byte) ([]byte, error) {
	var p struct{ R, S *big.Int }
	rest, e := asn1.Unmarshal(b, &p)
	if e != nil || len(rest) > 0 || p.R == nil || p.S == nil || p.R.Sign() <= 0 || p.S.Sign() <= 0 || p.R.Cmp(elliptic.P256().Params().N) >= 0 || p.S.Cmp(elliptic.P256().Params().N) >= 0 {
		return nil, errors.New("invalid P-256 DER signature")
	}
	out := make([]byte, 64)
	p.R.FillBytes(out[:32])
	p.S.FillBytes(out[32:])
	return out, nil
}
func Start(authority, endpoint, mode string, ca, id, hello []byte, signer Signer, protector Protector, resolver Resolver, observer Observer) (*Client, error) {
	return start(authority, endpoint, mode, ca, id, hello, signer, protector, resolver, observer, nil, false)
}

// StartPinned checks the optional SHA-256 leaf SPKI pin on every carrier. IP nodes require it.
func StartPinned(authority, endpoint, mode string, ca, id, hello []byte, signer Signer, protector Protector, resolver Resolver, observer Observer, pin []byte) (*Client, error) {
	return start(authority, endpoint, mode, ca, id, hello, signer, protector, resolver, observer, pin, true)
}
func start(authority, endpoint, mode string, ca, id, hello []byte, signer Signer, protector Protector, resolver Resolver, observer Observer, pin []byte, pinnedAPI bool) (*Client, error) {
	if len(id) != 16 || signer == nil {
		return nil, &transport.Error{Code: transport.Configuration, Cause: errors.New("device ID and signer required")}
	}
	var roots *x509.CertPool
	if len(ca) > 0 {
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			return nil, &transport.Error{Code: transport.Configuration, Cause: errors.New("invalid trust anchor")}
		}
	}
	cfg := transport.Config{Authority: authority, Endpoint: endpoint, Mode: mode, Roots: roots, Device: proof.ID(id), ClientHello: append([]byte(nil), hello...), Sign: func(m []byte) ([]byte, error) {
		der, e := signer.Sign(m)
		if e != nil {
			return nil, e
		}
		return rawSignature(der)
	}}
	if protector != nil {
		cfg.Protect = func(fd int) bool { return protector.Protect(int32(fd)) }
	}
	if resolver != nil {
		cfg.Resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			list, err := resolver.Resolve(host)
			if err != nil {
				return nil, err
			}
			var ips []netip.Addr
			for field := range strings.SplitSeq(list, ",") {
				ip, err := netip.ParseAddr(strings.TrimSpace(field))
				if err != nil {
					return nil, errors.New("invalid resolver address")
				}
				ips = append(ips, ip)
			}
			return ips, ctx.Err()
		}
	}
	if observer != nil {
		cfg.OnState = observer.StateChanged
	}
	ctx, cancel := context.WithCancel(context.Background())
	var c *transport.Client
	var e error
	if pinnedAPI {
		c, e = transport.StartPinned(ctx, cfg, pin)
	} else {
		c, e = transport.Start(ctx, cfg)
	}
	if e != nil {
		cancel()
		return nil, e
	}
	return &Client{c, ctx, cancel}, nil
}
func (c *Client) Stop()                           { c.cancel(); c.client.Stop() }
func (c *Client) State() string                   { return c.client.State() }
func (c *Client) Confirm() error                  { return c.client.Confirm() }
func (c *Client) AccountExpiry() int64            { return c.client.AccountExpiry() }
func (c *Client) SetRecoveryAllowed(allowed bool) { c.client.SetRecoveryAllowed(allowed) }
func (c *Client) OpenUDP(target string) (*Association, error) {
	a, e := c.client.OpenUDP(c.ctx, target)
	if e != nil {
		return nil, e
	}
	return &Association{a}, nil
}
func (a *Association) ReadPacket() ([]byte, error) { return a.a.ReadPacket() }
func (a *Association) WritePacket(p []byte) error  { return a.a.WritePacket(p) }
func (a *Association) Close() error                { return a.a.Close() }
func (c *Client) DialTCP(target string, initial []byte) (*Stream, error) {
	s, e := c.client.DialTCP(c.ctx, target, initial)
	if e != nil {
		return nil, e
	}
	return &Stream{s}, nil
}
func (s *Stream) Read(max int) ([]byte, error) {
	if max < 1 || max > 65536 {
		return nil, errors.New("invalid read size")
	}
	p := make([]byte, max)
	n, e := s.s.Read(p)
	if n > 0 {
		return p[:n], nil
	}
	return nil, e
}
func (s *Stream) Write(p []byte) (int, error) { return s.s.Write(p) }

// CloseWrite ends the CONNECT request while keeping the response readable until Close.
func (s *Stream) CloseWrite() error { return s.s.(interface{ CloseWrite() error }).CloseWrite() }
func (s *Stream) Close() error      { return s.s.Close() }
