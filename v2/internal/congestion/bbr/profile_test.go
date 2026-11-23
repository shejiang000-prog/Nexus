package bbr

import (
	"github.com/apernet/quic-go/congestion"
	"github.com/apernet/quic-go/monotime"
	"net"
	"testing"
	"time"
)

func TestInitialProfile(t *testing.T) {
	size := GetInitialPacketSize(&net.UDPAddr{IP: net.IPv4(192, 0, 2, 1)})
	if size < 1200 || NewBbrSender(DefaultClock{}, size, ProfileStandard) == nil {
		t.Fatal("initial sender")
	}
}

type testRTT struct{ congestion.RTTStatsProvider }

func (testRTT) MinRTT() time.Duration { return time.Millisecond }
func TestDeclaredLostPackets(t *testing.T) {
	b := NewBbrSender(DefaultClock{}, 1200, ProfileStandard)
	b.SetRTTStatsProvider(testRTT{})
	now := monotime.Now()
	b.OnPacketSent(now, 0, 1, 1200, true)
	b.OnPacketSent(now, 1200, 2, 1200, true)
	b.OnCongestionEventEx(2400, now.Add(time.Millisecond), nil, []congestion.LostPacketInfo{{PacketNumber: 1, BytesLost: 1200}, {PacketNumber: 2, BytesLost: 1200}})
	if got := b.DeclaredLostPackets(); got != 2 {
		t.Fatalf("lost packets = %d", got)
	}
}
