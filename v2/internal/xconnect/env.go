// Package xconnect prepares the Go HTTP/2 environment before package initialization.
package xconnect

import (
	"os"
	"strings"
)

func setting(env string) string {
	var keep []string
	for _, s := range strings.Split(env, ",") {
		if s != "" && !strings.HasPrefix(s, "http2xconnect=") {
			keep = append(keep, s)
		}
	}
	return strings.Join(append(keep, "http2xconnect=1"), ",")
}
func Ensure() error {
	old := os.Getenv("GODEBUG")
	next := setting(old)
	if next == old {
		return nil
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	if e = os.Setenv("GODEBUG", next); e != nil {
		return e
	}
	return restart(exe)
}
