//go:build nexuslab && linux

package mobile

import "github.com/shejiang000-prog/Nexus/v2/transport"

// DrainTransportTrace returns tab-separated boot-clock timestamps, events, carriers and optional ports.
func DrainTransportTrace() string { return transport.DrainTransportTrace() }
