//go:build unix

package xconnect

import (
	"os"
	"syscall"
)

func restart(exe string) error { return syscall.Exec(exe, os.Args, os.Environ()) }
