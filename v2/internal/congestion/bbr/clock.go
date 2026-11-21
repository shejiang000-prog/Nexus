// Source: github.com/apernet/hysteria core/internal/congestion/bbr/clock.go @ c2b181e0b0e72b077e4b0b7297735af14d1f9929 (MIT; see ../LICENSE.md).
package bbr

import "github.com/apernet/quic-go/monotime"

// A Clock returns the current time
type Clock interface {
	Now() monotime.Time
}

// DefaultClock implements the Clock interface using the Go stdlib clock.
type DefaultClock struct{}

var _ Clock = DefaultClock{}

// Now gets the current time
func (DefaultClock) Now() monotime.Time {
	return monotime.Now()
}
