package mobile

import "context"

// OpenTunnelDNS uses the Go dispatch context so a closed TUN flow cancels its CONNECT.
// gomobile does not bind context.Context. The Java Client surface stays unchanged.
func OpenTunnelDNS(ctx context.Context, c *Client, target string, first []byte) (*Association, error) {
	a, err := c.client.OpenDNS(ctx, target, first)
	if err != nil {
		return nil, err
	}
	return &Association{a}, nil
}
