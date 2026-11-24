//go:build !nexuslab || !linux

package transport

const labTrace = false

func trace(_, _, _ string) {}
