//go:build nexuslab && linux

package transport

import (
	"strconv"
	"strings"
	"testing"
)

func TestLabTransportTrace(t *testing.T) {
	DrainTransportTrace()
	trace("selected", "h2", "")
	trace("tcp_open", "h2", "443")
	rows := strings.Split(DrainTransportTrace(), "\n")
	if len(rows) != 2 || DrainTransportTrace() != "" {
		t.Fatal("events must be consumed exactly once")
	}
	var previous int64
	for i, row := range rows {
		fields := strings.Split(row, "\t")
		if len(fields) != 4 || fields[1] != []string{"selected", "tcp_open"}[i] || fields[2] != "h2" || fields[3] != []string{"", "443"}[i] {
			t.Fatalf("unexpected trace %q", row)
		}
		nanos, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || nanos <= 0 || nanos < previous {
			t.Fatalf("invalid boot clock %q", fields[0])
		}
		previous = nanos
	}
}
