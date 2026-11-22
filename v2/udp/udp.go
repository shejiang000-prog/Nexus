// Package udp carries one CONNECT-UDP association over capsules and optional HTTP Datagrams.
package udp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/apernet/quic-go/quicvarint"
)

const H2FlowHeader = "Nexus-Flow"

const Path = "/.well-known/masque/udp/"
const maxUDP = 65507

type Datagrams interface {
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
}

func unframe(data []byte) ([]byte, bool, error) {
	id, n, e := quicvarint.Parse(data)
	if e != nil {
		return nil, false, e
	}
	if id != 0 {
		return nil, false, nil
	}
	if len(data)-n > maxUDP {
		return nil, false, fmt.Errorf("oversized UDP")
	}
	return data[n:], true, nil
}
func readUDP(r *bufio.Reader) ([]byte, error) {
	for {
		kind, e := quicvarint.Read(r)
		if e != nil {
			return nil, e
		}
		n, e := quicvarint.Read(r)
		if e != nil {
			return nil, e
		}
		if n > maxUDP+1 {
			return nil, fmt.Errorf("capsule length exceeds limit")
		}
		if kind != 0 {
			if _, e = io.CopyN(io.Discard, r, int64(n)); e != nil {
				return nil, e
			}
			continue
		}
		buf := make([]byte, n)
		if _, e = io.ReadFull(r, buf); e != nil {
			return nil, e
		}
		data, ok, e := unframe(buf)
		if e != nil || ok {
			return data, e
		}
	}
}
func sendUDP(w io.Writer, d Datagrams, p []byte, tooLarge func(error) bool) (string, error) {
	if len(p) > maxUDP {
		return "", fmt.Errorf("oversized UDP")
	}
	data := append([]byte{0}, p...)
	if d != nil {
		e := d.SendDatagram(data)
		if e == nil {
			return "DATAGRAM", nil
		}
		if tooLarge == nil || !tooLarge(e) {
			return "", e
		}
	}
	hdr := quicvarint.Append([]byte{0}, uint64(len(data)))
	_, e := w.Write(append(hdr, data...))
	return "capsule", e
}

type packet struct {
	data []byte
	err  error
}
type Association struct {
	ctx         context.Context
	cancel      context.CancelFunc
	write       io.Writer
	native      Datagrams
	tooLarge    func(error) bool
	closeStream func()
	once        sync.Once
	mu          sync.Mutex
	incoming    chan packet
}

func New(ctx context.Context, r io.Reader, w io.Writer, d Datagrams, tooLarge func(error) bool, closeStream func()) *Association {
	ctx, cancel := context.WithCancel(ctx)
	a := &Association{ctx: ctx, cancel: cancel, write: w, native: d, tooLarge: tooLarge, closeStream: closeStream, incoming: make(chan packet)}
	context.AfterFunc(ctx, a.shutdown)
	reader := bufio.NewReader(r)
	go a.pump(func() ([]byte, error) { return readUDP(reader) })
	if d != nil {
		go a.pump(func() ([]byte, error) {
			for {
				raw, err := d.ReceiveDatagram(ctx)
				if err != nil {
					return nil, err
				}
				p, ok, err := unframe(raw)
				if ok || err != nil {
					return p, err
				}
			}
		})
	}
	return a
}
func (a *Association) pump(read func() ([]byte, error)) {
	for {
		p, e := read()
		select {
		case a.incoming <- packet{p, e}:
		case <-a.ctx.Done():
			return
		}
		if e != nil {
			return
		}
	}
}
func (a *Association) ReadPacket() ([]byte, error) {
	select {
	case p := <-a.incoming:
		if p.err != nil {
			a.Close()
		}
		return p.data, p.err
	case <-a.ctx.Done():
		return nil, a.ctx.Err()
	}
}
func (a *Association) WritePacket(p []byte) error {
	if len(p) > maxUDP {
		return errors.New("oversized UDP")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.ctx.Err(); e != nil {
		return e
	}
	_, err := sendUDP(a.write, a.native, p, a.tooLarge)
	return err
}
func (a *Association) shutdown()    { a.once.Do(a.closeStream) }
func (a *Association) Close() error { a.cancel(); a.shutdown(); return nil }
