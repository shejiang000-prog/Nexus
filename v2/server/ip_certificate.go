package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
)

func init() { caddy.RegisterModule(ipCertificate{}) }

// ipCertificate serves one installed IP identity. Certificate renewal and Caddy
// reloads retain the original private key, including after an ordinary uninstall.
type ipCertificate struct {
	IP        string `json:"ip"`
	Directory string `json:"directory"`
	identity  *ipIdentity
}
type ipIdentity struct {
	mu   sync.Mutex
	key  *ecdsa.PrivateKey
	cert *tls.Certificate
}

func (ipCertificate) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "tls.get_certificate.nexus_ip", New: func() caddy.Module { return new(ipCertificate) }}
}
func (c *ipCertificate) Provision(caddy.Context) error {
	if ip := net.ParseIP(c.IP); ip == nil || ip.IsUnspecified() || ip.IsMulticast() || !filepath.IsAbs(c.Directory) {
		return errors.New("invalid IP certificate configuration")
	}
	keyPath := filepath.Join(c.Directory, "ip-key.pem")
	certPath := filepath.Join(c.Directory, "ip-cert.pem")
	data, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		if _, e := os.Lstat(certPath); !errors.Is(e, os.ErrNotExist) {
			if _, ke := os.Stat(keyPath); ke != nil {
				return errors.New("existing IP certificate has no matching private key")
			}
		}
		k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return e
		}
		der, e := x509.MarshalECPrivateKey(k)
		if e != nil {
			return e
		}
		if e = writeIdentity(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), true); e != nil && !errors.Is(e, os.ErrExist) {
			return e
		}
		data, err = os.ReadFile(keyPath)
	}
	if err != nil {
		return err
	}
	if st, e := os.Lstat(keyPath); e != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return errors.New("unsafe IP identity key permissions")
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return errors.New("invalid IP identity key")
	}
	k, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil || k.Curve != elliptic.P256() {
		return errors.New("invalid IP identity key")
	}
	c.identity = &ipIdentity{key: k}
	data, err = os.ReadFile(certPath)
	if err == nil {
		pair, e := tls.X509KeyPair(data, pem.EncodeToMemory(block))
		if e != nil {
			return errors.New("stored IP certificate and key do not match")
		}
		pair.Leaf, e = x509.ParseCertificate(pair.Certificate[0])
		if e != nil {
			return e
		}
		c.identity.cert = &pair
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err = c.certificate(time.Now())
	return err
}
func (c *ipCertificate) GetCertificate(context.Context, *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return c.certificate(time.Now())
}
func (c *ipCertificate) certificate(now time.Time) (*tls.Certificate, error) {
	id := c.identity
	id.mu.Lock()
	defer id.mu.Unlock()
	if id.cert != nil && id.cert.Leaf.VerifyHostname(c.IP) == nil && !now.Before(id.cert.Leaf.NotBefore) && id.cert.Leaf.NotAfter.After(now.Add(30*24*time.Hour)) {
		return id.cert, nil
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	leaf := &x509.Certificate{SerialNumber: serial, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(365 * 24 * time.Hour), IPAddresses: []net.IP{net.ParseIP(c.IP)}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &id.key.PublicKey, id.key)
	if err != nil {
		return nil, err
	}
	leaf, err = x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	if err = writeIdentity(filepath.Join(c.Directory, "ip-cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), false); err != nil {
		return nil, err
	}
	id.cert = &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: id.key, Leaf: leaf}
	return id.cert, nil
}

// A reload can race a first start. Link the complete key file once; never replace it.
func writeIdentity(path string, data []byte, once bool) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err = errors.Join(err, f.Sync(), f.Close()); err != nil {
		return err
	}
	if once {
		return os.Link(f.Name(), path)
	}
	return os.Rename(f.Name(), path)
}
