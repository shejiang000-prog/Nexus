package server

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"github.com/quic-go/quic-go/http3"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/shejiang000-prog/Nexus/v2/proof"
)

func (h *Handler) redeem(w http.ResponseWriter, r *http.Request) bool {
	// This reserved path can contain bearer credentials. Strip its body before fallback.
	length := r.ContentLength
	body := r.Body
	r.Body = http.NoBody
	r.ContentLength = 0
	r.Header.Del("Content-Length")
	defer body.Close()
	if r.URL.RawQuery != "" || r.TLS == nil || !r.TLS.HandshakeComplete || r.TLS.Version != tls.VersionTLS13 || !strings.EqualFold(r.Host, h.Authority) {
		return false
	}
	if q := http3.NexusConn(r.Context()); q != nil && q.ConnectionState().Used0RTT {
		return false
	}
	if !nodeRedeemSources.allow(r.RemoteAddr, time.Now()) || length > 4096 {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(body, 4097))
	if err != nil || len(raw) > 4096 || !utf8.Valid(raw) {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	opening, err := dec.Token()
	if err != nil || opening != json.Delim('{') {
		return false
	}
	values := make(map[string]string, 5)
	for dec.More() {
		token, err := dec.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return false
		}
		switch key {
		case "invite", "request_id", "public_key", "proof", "name":
		default:
			return false
		}
		if _, ok := values[key]; ok {
			return false
		}
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		value, ok := tok.(string)
		if !ok {
			return false
		}
		values[key] = value
	}
	if end, err := dec.Token(); err != nil || end != json.Delim('}') {
		return false
	}
	if _, err = dec.Token(); err != io.EOF {
		return false
	}
	decode := func(key string, size int) []byte {
		v := values[key]
		if len(v) != base64.RawURLEncoding.EncodedLen(size) {
			return nil
		}
		b, err := base64.RawURLEncoding.Strict().DecodeString(v)
		if err != nil || len(b) != size {
			return nil
		}
		return b
	}
	request, token, public, sig := decode("request_id", 16), decode("invite", 32), decode("public_key", 33), decode("proof", 64)
	if request == nil || token == nil || public == nil || sig == nil {
		return false
	}
	exporter, err := proof.Secret(h.Authority, r.TLS.ExportKeyingMaterial)
	if err != nil {
		return false
	}
	id, expires, err := h.ledger.redeem(r.Context(), exporter, token, public, sig, proof.ID(request), values["name"])
	if err != nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	var until *int64
	if expires.Valid {
		until = &expires.Int64
	}
	json.NewEncoder(w).Encode(struct {
		Version int    `json:"v"`
		ID      string `json:"device_id"`
		Expires *int64 `json:"expires_at"`
	}{2, base64.RawURLEncoding.EncodeToString(id[:]), until})
	return true
}

// All handlers in the node share the admission budget.
var nodeRedeemSources redeemSources

type redeemSources struct {
	mu    sync.Mutex
	until time.Time
	count map[netip.Addr]int
	total int
}

func (s *redeemSources) allow(remote string, now time.Time) bool {
	peer, err := netip.ParseAddrPort(remote)
	if err != nil {
		return false
	}
	ip := peer.Addr().Unmap()
	if ip.Is6() {
		ip = netip.PrefixFrom(ip, 64).Masked().Addr()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !now.Before(s.until) {
		s.until = now.Add(time.Second)
		s.count = make(map[netip.Addr]int)
		s.total = 0
	}
	if s.count[ip] >= 16 || s.total >= 256 {
		return false
	}
	s.count[ip]++
	s.total++
	return true
}
