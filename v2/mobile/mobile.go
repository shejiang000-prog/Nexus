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

	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/transport"
)

type Signer interface {
	Sign(message []byte) ([]byte, error)
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
func Start(authority, endpoint, mode string, ca, id, hello []byte, signer Signer, observer Observer) (*Client, error) {
	if len(id) != 16 || signer == nil {
		return nil, errors.New("device ID and signer required")
	}
	var roots *x509.CertPool
	if len(ca) > 0 {
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			return nil, errors.New("invalid trust anchor")
		}
	}
	cfg := transport.Config{Authority: authority, Endpoint: endpoint, Mode: mode, Roots: roots, Device: proof.ID(id), ClientHello: append([]byte(nil), hello...), Sign: func(m []byte) ([]byte, error) {
		der, e := signer.Sign(m)
		if e != nil {
			return nil, e
		}
		return rawSignature(der)
	}}
	if observer != nil {
		cfg.OnState = observer.StateChanged
	}
	ctx, cancel := context.WithCancel(context.Background())
	c, e := transport.Start(ctx, cfg)
	if e != nil {
		cancel()
		return nil, e
	}
	return &Client{c, ctx, cancel}, nil
}
func (c *Client) Stop()         { c.cancel(); c.client.Stop() }
func (c *Client) State() string { return c.client.State() }
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
func (s *Stream) Close() error                { return s.s.Close() }
