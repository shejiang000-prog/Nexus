package main

import (
	"io"
	"os"
	"path/filepath"

	"github.com/xtls/libxray/tuntrust"
	"golang.org/x/sys/windows"
)

const wintunSHA256 = "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce"

type pinnedDriver struct {
	io.Closer
	module windows.Handle
}

func (d pinnedDriver) Close() error {
	windows.FreeLibrary(d.module)
	return d.Closer.Close()
}

func pinWintun() (io.Closer, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(filepath.Dir(exe), "wintun.dll")
	lock, err := tuntrust.Pin(path, wintunSHA256)
	if err != nil {
		return nil, err
	}
	mod, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		lock.Close()
		return nil, err
	}
	return pinnedDriver{lock, mod}, nil
}
