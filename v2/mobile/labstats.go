//go:build nexuslab

package mobile

import "runtime"

// LabGoroutines counts goroutines across the shared libXray/V2 process.
func LabGoroutines() int32 { return int32(runtime.NumGoroutine()) }
