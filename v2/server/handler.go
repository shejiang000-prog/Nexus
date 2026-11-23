package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	quic "github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/nexus/congestion/bbr"
	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/udp"
)

type MemoryDevices map[proof.ID]*ecdsa.PublicKey

func (m MemoryDevices) Lookup(id proof.ID) *ecdsa.PublicKey { return m[id] }

type sessionBinding struct {
	proof.Binding
	declaredLost func() uint64 // Bound to the congestion controller of this H3 connection.
}
type bindingKey struct{}
type socketKey struct{}
type Handler struct {
	Authority     string            `json:"authority"`
	DeviceKeys    map[string]string `json:"device_keys"`
	AllowExact    []string          `json:"allow_exact,omitempty"`
	HealthPath    string            `json:"health_path,omitempty"`
	LedgerPath    string            `json:"ledger_path,omitempty"`
	ledger        *Ledger
	releaseLedger func() error
	devices       proof.Devices
	exact         map[netip.AddrPort]bool
	sessions      *sync.Map
	slots         chan struct{}
}

func init() { caddy.RegisterModule(Handler{}) }
func (Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "http.handlers.nexus", New: func() caddy.Module { return new(Handler) }}
}
func (h *Handler) Provision(ctx caddy.Context) error {
	var e error
	h.Authority, e = proof.Authority(h.Authority)
	if e != nil {
		return e
	}
	if h.HealthPath == "" {
		h.HealthPath = "/health.bin"
	}
	devices := MemoryDevices{}
	for id, point := range h.DeviceKeys {
		b, e := hex.DecodeString(id)
		if e != nil || len(b) != 16 {
			return errors.New("invalid device ID")
		}
		p, e := hex.DecodeString(point)
		if e != nil {
			return errors.New("invalid device key")
		}
		x, y := elliptic.UnmarshalCompressed(elliptic.P256(), p)
		if x == nil {
			return errors.New("invalid P-256 point")
		}
		k := proof.ID(b)
		if devices[k] != nil {
			return errors.New("duplicate device ID")
		}
		devices[k] = &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	}
	h.devices = devices
	h.exact = make(map[netip.AddrPort]bool)
	for _, v := range h.AllowExact {
		a, e := netip.ParseAddrPort(v)
		if e != nil || a.Addr().Zone() != "" || a.Port() == 0 {
			return errors.New("invalid exact destination")
		}
		h.exact[a] = true
	}
	h.slots = make(chan struct{}, 4096)
	h.sessions = new(sync.Map)
	app, e := ctx.App("http")
	if e != nil {
		return e
	}
	for _, s := range app.(*caddyhttp.App).Servers {
		s.RegisterConnContext(func(ctx context.Context, conn net.Conn) context.Context {
			if ctx.Value(bindingKey{}) != nil {
				return ctx
			}
			return context.WithValue(context.WithValue(ctx, socketKey{}, conn), bindingKey{}, new(sync.Map))
		})
	}
	if h.LedgerPath != "" {
		if len(devices) > 0 {
			return errors.New("choose a ledger or static device keys")
		}
		h.ledger, h.releaseLedger, e = acquireNodeLedger(h.LedgerPath)
		if e != nil {
			return e
		}
		h.devices = h.ledger
		// During a reload Caddy still serves active requests on the old
		// configuration. On exit it waits for them before calling Cleanup.
		for _, s := range app.(*caddyhttp.App).Servers {
			s.RegisterOnShutdown(func() {
				if caddy.Exiting() {
					h.ledger.disconnectAll()
				}
			})
		}
	}
	return nil
}
func (h *Handler) Cleanup() error {
	if h.releaseLedger != nil {
		return h.releaseLedger()
	}
	return nil
}
func (h *Handler) authenticate(r *http.Request) *sessionBinding {
	vals := r.Header.Values("Proxy-Authorization")
	if len(vals) > 1 {
		return nil
	}
	header := r.Header.Get("Proxy-Authorization")
	secret := func() ([]byte, error) { return proof.Secret(h.Authority, r.TLS.ExportKeyingMaterial) }
	q := http3.NexusConn(r.Context())
	if q == nil {
		m, _ := r.Context().Value(bindingKey{}).(*sync.Map)
		if m == nil {
			return nil
		}
		b, _ := m.LoadOrStore(h, new(sessionBinding))
		bound := b.(*sessionBinding)
		if bound.Accept(header, h.devices, secret) {
			return bound
		}
		return nil
	}
	if q.ConnectionState().Used0RTT {
		return nil
	}
	if v, ok := h.sessions.Load(q); ok {
		bound := v.(*sessionBinding)
		if bound.Accept(header, h.devices, secret) {
			return bound
		}
		return nil
	}
	fresh := new(sessionBinding)
	if !fresh.Accept(header, h.devices, secret) {
		return nil
	}
	size := bbr.GetInitialPacketSize(q.RemoteAddr())
	if x := q.InitialPacketSize(); x > 0 {
		size = min(size, x)
	}
	cc := bbr.NewBbrSender(bbr.DefaultClock{}, size, bbr.ProfileStandard)
	fresh.declaredLost = cc.DeclaredLostPackets
	v, loaded := h.sessions.LoadOrStore(q, fresh)
	if loaded {
		bound := v.(*sessionBinding)
		if bound.Accept(header, h.devices, secret) {
			return bound
		}
		return nil
	}
	context.AfterFunc(q.Context(), func() { h.sessions.Delete(q) })
	q.SetCongestionControl(cc)
	return fresh
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if h.ledger != nil && r.Method == "POST" && r.URL.Path == "/.well-known/nexus/v2/redeem" {
		if h.redeem(w, r) {
			return nil
		}
		return next.ServeHTTP(w, r)
	}
	health := (r.Method == "GET" || r.Method == "HEAD") && r.URL.RequestURI() == h.HealthPath &&
		r.Host == h.Authority && r.Header.Get("Proxy-Authorization") != "" && (r.ProtoMajor == 2 || r.ProtoMajor == 3)
	if (!health && r.Method != "CONNECT") || r.TLS == nil || !r.TLS.HandshakeComplete || r.TLS.Version != tls.VersionTLS13 {
		return next.ServeHTTP(w, r)
	}
	proto := r.Header.Get(":protocol")
	if r.ProtoMajor == 3 && r.Proto != "HTTP/3.0" {
		proto = r.Proto
	}
	bound := h.authenticate(r)
	if bound == nil {
		return next.ServeHTTP(w, r)
	}
	var expires int64 // Static devices have no account deadline.
	if h.ledger != nil {
		// Wake blocked upstream reads and downstream writes on either carrier.
		q := http3.NexusConn(r.Context())
		tcp, _ := r.Context().Value(socketKey{}).(net.Conn)
		if q == nil && tcp == nil {
			return next.ServeHTTP(w, r)
		}
		unregister, until, ok := h.ledger.register(bound.Device(), func() {
			if q != nil {
				q.CloseWithError(0, "")
			} else {
				tcp.Close()
			}
		})
		if !ok {
			return next.ServeHTTP(w, r)
		}
		defer unregister()
		expires = until
	}
	accept := func() {
		w.Header().Set("Proxy-Authentication-Info", "Nexus2")
		w.Header().Set("Nexus-Account-Expires", strconv.FormatInt(expires, 10))
	}
	if health {
		accept()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "32768")
		w.Header().Set("Cache-Control", "private, no-store")
		if q := http3.NexusConn(r.Context()); q != nil {
			w.Header().Set("Nexus-QUIC-Sent", strconv.FormatUint(q.ConnectionStats().PacketsSent, 10))
			w.Header().Set("Nexus-QUIC-Lost", strconv.FormatUint(bound.declaredLost(), 10))
		}
		w.WriteHeader(http.StatusOK)
		if r.Method == "GET" {
			_, err := w.Write(healthBody[:])
			return err
		}
		return nil
	}
	dest := r.Host
	network := "tcp"
	if r.ProtoMajor == 2 && len(r.Header.Values(udp.H2FlowHeader)) != 0 {
		if proto != "" || len(r.Header.Values(udp.H2FlowHeader)) != 1 || r.Header.Get(udp.H2FlowHeader) != "udp" || r.Header.Get("Capsule-Protocol") != "?1" {
			return caddyhttp.Error(400, denied)
		}
		network = "udp"
	} else if proto != "" && (r.ProtoMajor != 3 || proto != "connect-udp") {
		return caddyhttp.Error(400, denied)
	} else if proto == "connect-udp" {
		if r.Host != h.Authority || r.Header.Get("Capsule-Protocol") != "?1" {
			return caddyhttp.Error(400, denied)
		}
		path, ok := strings.CutPrefix(r.URL.Path, udp.Path)
		parts := strings.Split(path, "/")
		if !ok || len(parts) != 3 || parts[2] != "" || r.URL.RawQuery != "" {
			return caddyhttp.Error(400, denied)
		}
		dest = net.JoinHostPort(parts[0], parts[1])
		network = "udp"
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		return caddyhttp.Error(429, errors.New("flow limit"))
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	addrs, e := target(ctx, dest, h.exact, net.DefaultResolver.LookupNetIP)
	if errors.Is(e, denied) {
		return caddyhttp.Error(403, denied)
	}
	if e != nil {
		return caddyhttp.Error(502, errors.New("resolve failed"))
	}
	conn, e := dialTargets(ctx, network, addrs, (&net.Dialer{}).DialContext)
	if e != nil {
		return caddyhttp.Error(502, errors.New("dial failed"))
	}
	defer conn.Close()
	stop := context.AfterFunc(r.Context(), func() { conn.Close() })
	defer stop()
	accept()
	if network == "udp" {
		w.Header().Set("Capsule-Protocol", "?1")
	}
	w.WriteHeader(200)
	flush := http.NewResponseController(w)
	if e = flush.Flush(); e != nil {
		return e
	}
	if network == "udp" {
		return relayUDP(w, r, conn, conn.RemoteAddr().String())
	}
	upDone := make(chan struct{})
	go func() {
		defer close(upDone)
		io.Copy(conn, r.Body)
		conn.(*net.TCPConn).CloseWrite()
	}()
	_, e = io.Copy(flushingWriter{w, flush}, conn)
	conn.Close()
	r.Body.Close()
	<-upDone
	return e
}

var healthBody [32768]byte

type flushingWriter struct {
	io.Writer
	f *http.ResponseController
}

func (w flushingWriter) Write(p []byte) (int, error) {
	n, e := w.Writer.Write(p)
	if e == nil {
		e = w.f.Flush()
	}
	return n, e
}

func relayUDP(w http.ResponseWriter, r *http.Request, c net.Conn, addr string) error {
	var reader io.Reader = r.Body
	var writer io.Writer = flushingWriter{w, http.NewResponseController(w)}
	closeStream := func() { r.Body.Close() }
	var d udp.Datagrams
	if r.ProtoMajor == 3 {
		for {
			v, ok := w.(interface{ Unwrap() http.ResponseWriter })
			if !ok {
				break
			}
			w = v.Unwrap()
		}
		streamer, ok := w.(http3.HTTPStreamer)
		if !ok {
			return errors.New("missing H3 stream")
		}
		stream, e := streamer.HTTPStream()
		if e != nil {
			return e
		}
		reader = stream
		writer = stream
		closeStream = func() { stream.CancelRead(0); stream.Close() }
		if caps, ok := w.(interface {
			ReceivedSettings() <-chan struct{}
			Settings() *http3.Settings
		}); ok {
			select {
			case <-caps.ReceivedSettings():
				if caps.Settings().EnableDatagrams && http3.NexusConn(r.Context()).ConnectionState().SupportsDatagrams.Local {
					d = stream
				}
			case <-r.Context().Done():
				closeStream()
				return r.Context().Err()
			}
		}
	}
	a := udp.New(r.Context(), reader, writer, d, func(err error) bool { var x *quic.DatagramTooLargeError; return errors.As(err, &x) }, closeStream)
	upDone := make(chan struct{})
	defer func() { a.Close(); c.Close(); <-upDone }()
	idle := 60 * time.Second
	if strings.HasSuffix(addr, ":53") {
		idle = 10 * time.Second
	}
	c.SetReadDeadline(time.Now().Add(idle))
	go func() {
		defer close(upDone)
		defer c.Close()
		for {
			packet, e := a.ReadPacket()
			if e != nil {
				return
			}
			if _, e = c.Write(packet); e != nil {
				return
			}
			c.SetReadDeadline(time.Now().Add(idle))
		}
	}()
	buf := make([]byte, 65507)
	for {
		n, e := c.Read(buf)
		if e != nil {
			return nil
		}
		c.SetReadDeadline(time.Now().Add(idle))
		if e = a.WritePacket(buf[:n]); e != nil {
			return e
		}
	}
}
