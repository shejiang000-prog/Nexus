// Package proof binds an authenticated device to one completed TLS 1.3 connection.
package proof

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
)

const Label = "EXPORTER-Nexus-V2"
const prefix = "Nexus2 "

type ID [16]byte
type Exporter func(string, []byte, int) ([]byte, error)

// Sign receives the message before SHA-256 and returns P-256 r||s (64 bytes).
type Sign func([]byte) ([]byte, error)
type Devices interface{ Lookup(ID) *ecdsa.PublicKey }

func Authority(s string) (string, error) {
	host, port, e := net.SplitHostPort(s)
	n, e2 := strconv.ParseUint(port, 10, 16)
	if e != nil || e2 != nil || n == 0 || host == "" {
		return "", errors.New("invalid authority")
	}
	host = strings.ToLower(host)
	if ip, e := netip.ParseAddr(host); e == nil {
		if ip.Zone() != "" {
			return "", errors.New("scoped address")
		}
		host = ip.String()
	} else {
		if len(host) > 253 {
			return "", errors.New("invalid host")
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", errors.New("invalid DNS label")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return "", errors.New("invalid DNS name")
				}
			}
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(int(n))), nil
}

func Secret(authority string, export Exporter) ([]byte, error) {
	a, err := Authority(authority)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(a))
	b, err := export(Label, h[:], 32)
	if err == nil && len(b) != 32 {
		return nil, errors.New("invalid exporter length")
	}
	return b, err
}
func Message(secret []byte, id ID) []byte {
	msg := append([]byte("NexusV2\x02"), secret...)
	return append(msg, id[:]...)
}
func Create(secret []byte, id ID, sign Sign) (string, error) {
	if len(secret) != 32 || sign == nil {
		return "", errors.New("invalid signing input")
	}
	sig, err := sign(Message(secret, id))
	if err != nil {
		return "", err
	}
	if len(sig) != 64 {
		return "", errors.New("invalid signature length")
	}
	b := append([]byte{2}, id[:]...)
	b = append(b, sig...)
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}
func Verify(secret []byte, id ID, pub *ecdsa.PublicKey, sig []byte) bool {
	if len(secret) != 32 || len(sig) != 64 || pub == nil || pub.Curve != elliptic.P256() {
		return false
	}
	h := sha256.Sum256(Message(secret, id))
	return ecdsa.Verify(pub, h[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
}

type Binding struct {
	mu     sync.Mutex
	header string
	id     ID
}

// Accept serializes the first batch. The caller supplies the exporter of this connection only.
func (b *Binding) Accept(header string, devices Devices, secret func() ([]byte, error)) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.header != "" {
		return (header == "" || header == b.header) && devices.Lookup(b.id) != nil
	}
	if len(header) != 115 || !strings.HasPrefix(header, prefix) {
		return false
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(header[len(prefix):])
	if err != nil || len(data) != 81 || data[0] != 2 {
		return false
	}
	id := ID(data[1:17])
	pub := devices.Lookup(id)
	if pub == nil {
		return false
	}
	key, err := secret()
	if err != nil || !Verify(key, id, pub, data[17:]) {
		return false
	}
	b.header = header
	b.id = id
	return true
}

// RedeemMessage encodes the invitation proof transcript; the account store owns redemption.
func RedeemMessage(secret []byte, request ID, inviteHash [32]byte, public []byte) ([]byte, error) {
	if len(secret) != 32 {
		return nil, errors.New("invalid exporter")
	}
	if x, _ := elliptic.UnmarshalCompressed(elliptic.P256(), public); x == nil {
		return nil, errors.New("invalid public key")
	}
	var b bytes.Buffer
	b.WriteString("NexusV2\x01")
	b.Write(secret)
	b.Write(request[:])
	b.Write(make([]byte, 16))
	b.Write([]byte{0, 65})
	b.Write(inviteHash[:])
	b.Write(public)
	return b.Bytes(), nil
}
