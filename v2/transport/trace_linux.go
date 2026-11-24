//go:build nexuslab && linux

package transport

import (
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const labTrace = true

var labEvents struct {
	sync.Mutex
	armed   bool
	rows    []string
	dropped int
}

func trace(event, kind, port string) {
	labEvents.Lock()
	defer labEvents.Unlock()
	if !labEvents.armed {
		return
	}
	if len(labEvents.rows) >= 4096 {
		labEvents.dropped++
		return
	}
	var ts unix.Timespec
	if unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts) == nil {
		labEvents.rows = append(labEvents.rows, fmt.Sprintf("%d\t%s\t%s\t%s", ts.Nano(), event, kind, port))
	}
}

// DrainTransportTrace arms the lab recorder and consumes events on Android's elapsedRealtime clock.
func DrainTransportTrace() string {
	labEvents.Lock()
	defer labEvents.Unlock()
	labEvents.armed = true
	if labEvents.dropped > 0 {
		labEvents.rows = append(labEvents.rows, fmt.Sprintf("0\ttrace_dropped\t\t%d", labEvents.dropped))
		labEvents.dropped = 0
	}
	rows := labEvents.rows
	labEvents.rows = nil
	return strings.Join(rows, "\n")
}
