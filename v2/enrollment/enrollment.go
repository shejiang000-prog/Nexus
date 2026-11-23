// Package enrollment binds one invitation proof to its originating TLS session.
package enrollment

import (
	"bytes"
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shejiang000-prog/Nexus/v2/proof"
	"golang.org/x/net/http2"
)

const prefix = "nexus://invite/v2#"

var invalidInvite = errors.New("V2 邀请格式无效")
var b64 = base64.RawURLEncoding.Strict()

type Invitation struct {
	host, authority, name string
	token                 []byte
}

func (i *Invitation) Host() string      { return i.host }
func (i *Invitation) Authority() string { return i.authority }
func (i *Invitation) Name() string      { return i.name }

// LocalID lets a retry recover the original key and request ID without retaining the bearer.
func (i *Invitation) LocalID() string {
	d := sha256.Sum256(append([]byte(i.authority+"\x00"), i.token...))
	return hex.EncodeToString(d[:])
}

func from64(s string) ([]byte, error) {
	b, e := b64.DecodeString(s)
	if e != nil || b64.EncodeToString(b) != s {
		return nil, invalidInvite
	}
	return b, nil
}

func Parse(link string) (*Invitation, error) {
	encoded, ok := strings.CutPrefix(link, prefix)
	if !ok || len(encoded) > b64.EncodedLen(4096) {
		return nil, invalidInvite
	}
	raw, err := from64(encoded)
	if err != nil || !utf8.Valid(raw) {
		return nil, invalidInvite
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return nil, invalidInvite
	}
	i := new(Invitation)
	seen := map[string]bool{}
	var port, version int
	var token string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, invalidInvite
		}
		key, ok := k.(string)
		if !ok || seen[key] {
			return nil, invalidInvite
		}
		seen[key] = true
		switch key {
		case "v":
			err = dec.Decode(&version)
		case "host":
			err = dec.Decode(&i.host)
		case "port":
			err = dec.Decode(&port)
		case "invite":
			err = dec.Decode(&token)
		case "name":
			err = dec.Decode(&i.name)
		case "pin":
			return nil, errors.New("此版本尚不支持带证书固定值的邀请")
		default:
			return nil, invalidInvite
		}
		if err != nil {
			return nil, invalidInvite
		}
	}
	if end, err := dec.Token(); err != nil || end != json.Delim('}') {
		return nil, invalidInvite
	}
	if _, err = dec.Token(); err != io.EOF || version != 2 || port < 1 || port > 65535 || i.host != strings.ToLower(i.host) || net.ParseIP(i.host) != nil || strings.ContainsAny(i.host, "[]:%") {
		return nil, invalidInvite
	}
	if len(i.name) > 128 || strings.IndexFunc(i.name, func(r rune) bool { return unicode.IsControl(r) || unicode.In(r, unicode.Cf) }) >= 0 {
		return nil, invalidInvite
	}
	i.authority, err = proof.Authority(net.JoinHostPort(i.host, strconv.Itoa(port)))
	if err != nil {
		return nil, invalidInvite
	}
	i.token, err = from64(token)
	if err != nil || len(i.token) != 32 {
		return nil, invalidInvite
	}
	return i, nil
}

type Signer interface {
	Sign(message []byte) ([]byte, error)
}
type Receipt struct {
	DeviceID    string
	ExpiresAt   int64
	ExpiryKnown bool
}

func Redeem(i *Invitation, endpoint string, ca, request, public []byte, signer Signer) (*Receipt, error) {
	configErr := errors.New("设备注册参数无效")
	if i == nil || len(i.token) != 32 || len(request) != 16 || len(public) != 33 || signer == nil {
		return nil, configErr
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: i.host, NextProtos: []string{"h2"}}
	if len(ca) > 0 {
		cfg.RootCAs = x509.NewCertPool()
		if !cfg.RootCAs.AppendCertsFromPEM(ca) {
			return nil, configErr
		}
	}
	if endpoint == "" {
		endpoint = i.authority
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	d := tls.Dialer{Config: cfg}
	conn, err := d.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, errors.New("无法建立受信的节点连接，请检查网络和证书")
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return nil, errors.New("无法连接节点")
	}
	state := conn.(*tls.Conn).ConnectionState()
	if state.NegotiatedProtocol != "h2" {
		return nil, errors.New("节点不支持注册所需的 HTTP/2")
	}
	secret, err := proof.Secret(i.authority, state.ExportKeyingMaterial)
	if err != nil {
		return nil, errors.New("建立设备注册证明失败")
	}
	msg, err := proof.RedeemMessage(secret, proof.ID(request), sha256.Sum256(i.token), public)
	if err != nil {
		return nil, configErr
	}
	sigErr := errors.New("设备密钥签名失败")
	der, err := signer.Sign(msg)
	if err != nil {
		return nil, sigErr
	}
	var p struct{ R, S *big.Int }
	rest, err := asn1.Unmarshal(der, &p)
	if err != nil || len(rest) != 0 || p.R == nil || p.S == nil || p.R.Sign() <= 0 || p.S.Sign() <= 0 || p.R.Cmp(elliptic.P256().Params().N) >= 0 || p.S.Cmp(elliptic.P256().Params().N) >= 0 {
		return nil, sigErr
	}
	data, _ := json.Marshal(map[string]string{"invite": b64.EncodeToString(i.token), "request_id": b64.EncodeToString(request), "public_key": b64.EncodeToString(public), "proof": b64.EncodeToString(append(p.R.FillBytes(make([]byte, 32)), p.S.FillBytes(make([]byte, 32))...))})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://"+i.authority+"/.well-known/nexus/v2/redeem", bytes.NewReader(data))
	if err != nil {
		return nil, configErr
	}
	req.Header.Set("Content-Type", "application/json")
	cc, err := new(http2.Transport).NewClientConn(conn)
	if err != nil {
		return nil, errors.New("无法建立节点注册连接")
	}
	resp, err := cc.RoundTrip(req)
	if err != nil {
		return nil, errors.New("兑换响应中断，请重试同一邀请")
	}
	defer resp.Body.Close()
	denied := errors.New("节点未接受邀请，请确认邀请有效且未过期")
	if resp.StatusCode != http.StatusOK {
		return nil, denied
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1025))
	if err != nil {
		return nil, errors.New("兑换响应中断，请重试同一邀请")
	}
	var parsed struct {
		V      int
		ID     string          `json:"device_id"`
		Expiry json.RawMessage `json:"expires_at"`
	}
	if len(body) > 1024 || json.Unmarshal(body, &parsed) != nil || parsed.V != 2 {
		return nil, denied
	}
	id, err := from64(parsed.ID)
	if err != nil || len(id) != 16 {
		return nil, denied
	}
	receipt := &Receipt{DeviceID: hex.EncodeToString(id), ExpiryKnown: parsed.Expiry != nil}
	if parsed.Expiry != nil && string(parsed.Expiry) != "null" && (json.Unmarshal(parsed.Expiry, &receipt.ExpiresAt) != nil || receipt.ExpiresAt <= 0) {
		return nil, denied
	}
	return receipt, nil
}
