package v2vpn

import (
	"errors"
	"testing"

	"github.com/xtls/xray-core/common"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	routesession "github.com/xtls/xray-core/features/routing/session"
	_ "github.com/xtls/xray-core/main/distro/all"
)

// Start replaces the selected outbound. Direct CIDRs/domains, DNS priority and the
// default route must still resolve to the same tags as a subscription's Xray core.
func TestSplitRoutingWithV2Outbound(t *testing.T) {
	const config = `{
		"outbounds":[{"protocol":"blackhole","tag":"selected"},{"protocol":"freedom","tag":"direct"}],
		"routing":{"domainStrategy":"AsIs","rules":[
			{"inboundTag":["dns-tcp"],"outboundTag":"selected"},
			{"port":"53","outboundTag":"selected"},
			{"domain":["domain:domestic.test"],"outboundTag":"direct"},
			{"ip":["192.0.2.0/24"],"outboundTag":"direct"}
		]}}`
	regular, err := core.StartInstance("json", []byte(config))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { regular.Close() })
	v2, err := Start(config, nil) // Route selection does not dial the transport.
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v2.Stop)
	for name, instance := range map[string]*core.Instance{"subscription": regular, "v2": v2.instance} {
		t.Run(name, func(t *testing.T) {
			manager := instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
			router := instance.GetFeature(routing.RouterType()).(routing.Router)
			for _, tc := range []struct {
				target, inbound, want string
			}{
				{"tcp:node.domestic.test:443", "", "direct"},
				{"udp:192.0.2.1:443", "", "direct"},
				{"udp:192.0.2.1:53", "", "selected"},
				{"tcp:192.0.2.1:443", "dns-tcp", "selected"},
				{"tcp:198.51.100.1:443", "", "selected"},
			} {
				dst, err := xnet.ParseDestination(tc.target)
				if err != nil {
					t.Fatal(err)
				}
				r, err := router.PickRoute(&routesession.Context{
					Inbound: &session.Inbound{Tag: tc.inbound}, Outbound: &session.Outbound{Target: dst},
				})
				h := manager.GetDefaultHandler()
				if err == nil {
					h = manager.GetHandler(r.GetOutboundTag())
				} else if !errors.Is(err, common.ErrNoClue) {
					t.Fatal(err)
				}
				if h == nil || h.Tag() != tc.want {
					t.Errorf("%s via %q: missing expected outbound %q", tc.target, tc.inbound, tc.want)
				} else if name == "v2" && (h == v2.handler) != (tc.want == "selected") {
					t.Errorf("%s via %q: unexpected V2 transport selection", tc.target, tc.inbound)
				}
			}
		})
	}
}
