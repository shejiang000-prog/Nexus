package transport

import (
	"context"
	"errors"
	"net"

	"github.com/xtls/libxray/internal/nexusv2/udp"
)

// OpenDNS is built only into libXray's internal transport for the Android TUN.
func (c *Client) OpenDNS(ctx context.Context, target string, first []byte) (*Association, error) {
	_, port, err := net.SplitHostPort(target)
	if err != nil || port != "53" || len(first) == 0 {
		return nil, &Error{Configuration, errors.New("DNS destination and first packet required")}
	}
	frame, err := udp.DatagramCapsule(first)
	if err != nil {
		return nil, &Error{Configuration, err}
	}
	return c.openUDP(ctx, target, frame)
}
