package bbr

import (
	"net"
	"testing"
)

func TestInitialProfile(t *testing.T) {
	size := GetInitialPacketSize(&net.UDPAddr{IP: net.IPv4(192, 0, 2, 1)})
	if size < 1200 || NewBbrSender(DefaultClock{}, size, ProfileStandard) == nil {
		t.Fatal("initial sender")
	}
}
