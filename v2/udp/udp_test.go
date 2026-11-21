package udp

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/apernet/quic-go/quicvarint"
)

func TestUDPCapsuleBounds(t *testing.T) {
	for _, size := range []int{0, 4, maxUDP} {
		want := bytes.Repeat([]byte{42}, size)
		buf := bytes.NewBuffer([]byte{7, 1, 99, 0, 2, 1, 99})
		if _, err := sendUDP(buf, nil, want, nil); err != nil {
			t.Fatal(err)
		}
		got, err := readUDP(bufio.NewReader(buf))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("round trip size %d: %v", size, err)
		}
	}
	for _, bad := range [][]byte{
		{0x40}, {0, 0x40}, {0, 0}, {0, 1, 0x40}, {0, 2, 0},
		quicvarint.Append([]byte{0}, maxUDP+2),
	} {
		if _, err := readUDP(bufio.NewReader(bytes.NewReader(bad))); err == nil {
			t.Errorf("accepted invalid capsule %x", bad)
		}
	}
	oversize := make([]byte, maxUDP+2)
	if _, _, err := unframe(oversize); err == nil {
		t.Error("accepted oversized HTTP datagram")
	}
	if _, err := sendUDP(new(bytes.Buffer), nil, oversize, nil); err == nil {
		t.Error("sent oversized UDP payload")
	}
}
