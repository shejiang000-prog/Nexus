// Package tuntrust pins the exact local binary during elevation or driver loading.
package tuntrust

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

type files []*os.File

func (fs files) Close() error {
	for i := len(fs) - 1; i >= 0; i-- {
		fs[i].Close()
	}
	return nil
}

func Pin(path, expected string) (io.Closer, error) {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || !filepath.IsAbs(path) || len(expected) != 64 {
		return nil, errors.New("untrusted binary path")
	}
	// Deny replacement of both the binary and intervening path components
	// between hashing and ShellExecuteEx/LoadLibraryEx resolving the name.
	current := volume + `\`
	root, err := windows.UTF16PtrFromString(current)
	if err != nil || windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return nil, errors.New("binary must reside on a local fixed drive")
	}
	parts := strings.Split(path[len(current):], `\`)
	var held files
	ok := false
	defer func() {
		if !ok {
			held.Close()
		}
	}()
	for i, part := range parts {
		current = filepath.Join(current, part)
		p, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return nil, err
		}
		// Metadata-only opens do not participate in NT share-access checks.
		access := uint32(windows.FILE_LIST_DIRECTORY | windows.FILE_READ_ATTRIBUTES)
		flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT | windows.FILE_FLAG_BACKUP_SEMANTICS)
		if i == len(parts)-1 {
			access = windows.GENERIC_READ
		}
		h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, flags, 0)
		if err != nil {
			return nil, err
		}
		f := os.NewFile(uintptr(h), current)
		held = append(held, f)
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return nil, errors.New("untrusted binary location")
		}
		if i == len(parts)-1 {
			hash := sha256.New()
			if _, err := io.Copy(hash, f); err != nil || hex.EncodeToString(hash.Sum(nil)) != expected {
				return nil, errors.New("binary integrity check failed")
			}
		}
	}
	ok = true
	return held, nil
}
