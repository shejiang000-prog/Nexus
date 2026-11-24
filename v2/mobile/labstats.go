//go:build nexuslab

package mobile

import (
	"runtime"
	"strings"
)

// LabGoroutines counts goroutines across the shared libXray/V2 process.
func LabGoroutines() int32 { return int32(runtime.NumGoroutine()) }

// LabUDPWorkers counts live Android V2 UDP dispatchers and their workers.
func LabUDPWorkers() int32 {
	size := runtime.NumGoroutine() + 32
	for {
		records := make([]runtime.StackRecord, size)
		n, ok := runtime.GoroutineProfile(records)
		if !ok {
			size = n + 32
			continue
		}
		var count int32
		for _, r := range records[:n] {
			frames := runtime.CallersFrames(r.Stack())
			for {
				frame, more := frames.Next()
				if strings.HasPrefix(frame.Function, "github.com/xtls/libxray/v2vpn.(*handler).udp") {
					count++
					break
				}
				if !more {
					break
				}
			}
		}
		return count
	}
}
