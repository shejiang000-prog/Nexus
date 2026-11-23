//go:build !linux

package main

import (
	"errors"
	"io"
)

func platformCommand(args []string, _ io.Reader, _ io.Writer) (bool, error) {
	if len(args) > 0 {
		switch args[0] {
		case "install", "upgrade", "uninstall", "status", "compat-serve", "firewall-apply":
			return true, errors.New("installation requires Linux with systemd")
		}
	}
	return false, nil
}
