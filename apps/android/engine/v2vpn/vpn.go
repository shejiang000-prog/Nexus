// Package v2vpn is built inside the pinned libXray module by build_v2_aar.py.
// Only the selected outbound changes; Xray retains its existing Android TUN and router.
package v2vpn

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	mobile "github.com/xtls/libxray/nexusmobile"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
)

type Session struct {
	instance *core.Instance
	handler  *handler
}

type handler struct {
	client *mobile.Client
	ctx    context.Context
	cancel context.CancelFunc
	code   atomic.Int32
}

func Start(config string, client *mobile.Client) (*Session, error) {
	cfg, err := core.LoadConfig("json", strings.NewReader(config))
	if err != nil {
		return nil, err
	}
	instance, err := core.New(cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	h := &handler{client: client, ctx: ctx, cancel: cancel}
	if old := manager.GetDefaultHandler(); old == nil || old.Tag() != h.Tag() {
		err = errors.New("missing V2 default outbound")
	} else {
		err = manager.RemoveHandler(ctx, h.Tag())
		if err == nil {
			err = old.Close()
		}
		if err == nil {
			err = manager.AddHandler(ctx, h)
		}
		if err == nil {
			err = instance.Start()
		}
	}
	if err != nil {
		cancel()
		instance.Close()
		return nil, err
	}
	return &Session{instance, h}, nil
}

func (s *Session) Stop()            { s.instance.Close() }
func (s *Session) ErrorCode() int32 { return s.handler.code.Swap(0) }

func (*handler) Tag() string                          { return "selected" }
func (*handler) Start() error                         { return nil }
func (h *handler) Close() error                       { h.cancel(); return nil }
func (*handler) SenderSettings() *serial.TypedMessage { return nil }
func (*handler) ProxySettings() *serial.TypedMessage  { return nil }

func (h *handler) dialError(err error) {
	code := mobile.ErrorCode(err)
	// A per-destination refusal must not tear down unrelated flows.
	if code == 2 || code == 3 {
		h.code.Store(code)
	}
}

// The Android sniffer buffers the client hello before selecting this outbound. Only
// take bytes already on hand so a server-first peer can still send its greeting.
func initialTCP(reader buf.Reader) ([]byte, buf.MultiBuffer, error) {
	timed, ok := reader.(buf.TimeoutReader)
	if !ok {
		return nil, nil, nil
	}
	batch, err := timed.ReadMultiBufferTimeout(0)
	if errors.Is(err, buf.ErrReadTimeout) {
		err = nil
	}
	payload := make([]byte, min(batch.Len(), 16384))
	tail, _ := buf.SplitBytes(batch, payload)
	return payload, tail, err
}

func uploadTCP(reader buf.Reader, writer buf.Writer, tail buf.MultiBuffer, readErr error) error {
	if len(tail) > 0 {
		if err := writer.WriteMultiBuffer(tail); err != nil {
			return err
		}
	}
	if errors.Is(readErr, io.EOF) {
		return nil
	}
	if readErr != nil {
		return readErr
	}
	return buf.Copy(reader, writer)
}

func (h *handler) Dispatch(ctx context.Context, link *transport.Link) {
	outs := session.OutboundsFromContext(ctx)
	if len(outs) == 0 {
		common.Interrupt(link.Writer)
		return
	}
	out := outs[len(outs)-1]
	defer common.Interrupt(link.Reader)
	defer common.Close(link.Writer)
	if out.Target.Network == xnet.Network_UDP {
		h.udp(ctx, link, out)
		return
	}
	initial, tail, readErr := initialTCP(link.Reader)
	stream, err := h.client.DialTCP(out.Target.NetAddr(), initial)
	if err != nil {
		buf.ReleaseMulti(tail)
		h.dialError(err)
		return
	}
	defer stream.Close()
	done := make(chan struct{}, 2)
	go func() {
		if err := uploadTCP(link.Reader, buf.NewWriter(stream), tail, readErr); err == nil {
			// Android's shutdownOutput ends only the CONNECT request body.
			if err := stream.CloseWrite(); err == nil {
				return
			}
		}
		done <- struct{}{}
	}()
	go func() {
		for {
			data, err := stream.Read(32768)
			if len(data) > 0 {
				if err := link.Writer.WriteMultiBuffer(buf.MergeBytes(nil, data)); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (h *handler) udp(ctx context.Context, link *transport.Link, out *session.Outbound) {
	// The TUN groups UDP by source socket, while V2 associations have fixed destinations.
	// Keep independent associations, including for apps querying several servers from one socket.
	peers := map[xnet.Destination]*mobile.Association{}
	type retiredPeer struct {
		source xnet.Destination
		peer   *mobile.Association
	}
	retired := make(chan retiredPeer, 64)
	var writers sync.Mutex
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Dispatch must be able to return while an Android UDP socket is silent.
	// Xray closes the underlying TUN queue only after Dispatch returns.
	incoming := make(chan buf.MultiBuffer)
	go func() {
		defer close(incoming)
		for {
			batch, err := link.Reader.ReadMultiBuffer()
			if len(batch) != 0 {
				select {
				case incoming <- batch:
				case <-readCtx.Done():
					buf.ReleaseMulti(batch)
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		for _, peer := range peers {
			peer.Close()
		}
	}()
	for {
		var batch buf.MultiBuffer
		select {
		case <-ctx.Done():
			return
		case <-h.ctx.Done():
			return
		case p := <-retired:
			if peers[p.source] == p.peer {
				delete(peers, p.source)
			}
			if len(peers) == 0 {
				return
			}
			continue
		case packets, ok := <-incoming:
			if !ok {
				return
			}
			batch = packets
		}
		for len(batch) > 0 {
			var packet *buf.Buffer
			batch, packet = buf.SplitFirst(batch)
			original := out.OriginalTarget
			if packet.UDP != nil {
				original = *packet.UDP
			}
			if !original.IsValid() {
				original = out.Target
			}
			peer := peers[original]
			firstDNS := false
			if peer == nil && len(peers) < 64 {
				dest := original
				if original == out.OriginalTarget {
					dest = out.Target
				}
				var openErr error
				firstDNS = dest.Port == 53 && !packet.IsEmpty()
				if firstDNS {
					peer, openErr = mobile.OpenTunnelDNS(ctx, h.client, dest.NetAddr(), packet.Bytes())
				} else {
					peer, openErr = h.client.OpenUDP(dest.NetAddr())
				}
				if openErr != nil {
					h.dialError(openErr)
				} else {
					peers[original] = peer
					go func(a *mobile.Association, source xnet.Destination) {
						defer func() {
							a.Close()
							select {
							case retired <- retiredPeer{source, a}:
							case <-readCtx.Done():
							}
						}()
						for {
							p, e := a.ReadPacket()
							if e != nil {
								return
							}
							b := buf.FromBytes(p)
							b.UDP = &source
							writers.Lock()
							e = link.Writer.WriteMultiBuffer(buf.MultiBuffer{b})
							writers.Unlock()
							if e != nil {
								return
							}
						}
					}(peer, original)
				}
			}
			if peer != nil && !firstDNS {
				if e := peer.WritePacket(packet.Bytes()); e != nil {
					peer.Close()
					delete(peers, original)
				}
			}
			packet.Release()
		}
		if len(peers) == 0 {
			return
		}
	}
}

var _ io.Writer = (*mobile.Stream)(nil)
