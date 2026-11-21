// Source: github.com/apernet/hysteria core/internal/congestion/bbr/bandwidth.go @ c2b181e0b0e72b077e4b0b7297735af14d1f9929 (MIT; see ../LICENSE.md).
package bbr

import (
	"math"
	"time"

	"github.com/apernet/quic-go/congestion"
)

const (
	infBandwidth = Bandwidth(math.MaxUint64)
)

// Bandwidth of a connection
type Bandwidth uint64

const (
	// BitsPerSecond is 1 bit per second
	BitsPerSecond Bandwidth = 1
	// BytesPerSecond is 1 byte per second
	BytesPerSecond = 8 * BitsPerSecond
)

// BandwidthFromDelta calculates the bandwidth from a number of bytes and a time delta
func BandwidthFromDelta(bytes congestion.ByteCount, delta time.Duration) Bandwidth {
	return Bandwidth(bytes) * Bandwidth(time.Second) / Bandwidth(delta) * BytesPerSecond
}
