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
	"unsafe"

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
	if len(expected) != 64 {
		return nil, errors.New("missing binary checksum")
	}
	lock, actual, err := pinDigest(path, false)
	if err != nil {
		return nil, err
	}
	if actual != expected {
		lock.Close()
		return nil, errors.New("binary integrity check failed")
	}
	return lock, nil
}

// InstalledDigest lets the privileged installer record the digest while the complete
// destination path is locked against writes, renames and reparse points.
func InstalledDigest(path string) (io.Closer, string, error) { return pinDigest(path, true) }

func protectedACL(h windows.Handle) bool {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	trusted := func(s *windows.SID) bool {
		return s != nil && (s.IsWellKnown(windows.WinBuiltinAdministratorsSid) || s.IsWellKnown(windows.WinLocalSystemSid) || s.String() == "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464")
	}
	owner, _, err := sd.Owner()
	if err != nil || !trusted(owner) {
		return false
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return false
	}
	for i := uint16(0); i < acl.AceCount; i++ {
		var a *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, uint32(i), &a) != nil {
			return false
		}
		if a.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || a.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if a.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return false
		}
		const readExecute = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE | windows.GENERIC_READ | windows.GENERIC_EXECUTE
		if uint32(a.Mask)&^uint32(readExecute) != 0 && !trusted((*windows.SID)(unsafe.Pointer(&a.SidStart))) {
			return false
		}
	}
	return true
}

func pinDigest(path string, protected bool) (io.Closer, string, error) {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || !filepath.IsAbs(path) {
		return nil, "", errors.New("untrusted binary path")
	}
	// Deny replacement of both the binary and intervening path components
	// between hashing and ShellExecuteEx/LoadLibraryEx resolving the name.
	current := volume + `\`
	root, err := windows.UTF16PtrFromString(current)
	if err != nil || windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return nil, "", errors.New("binary must reside on a local fixed drive")
	}
	parts := strings.Split(path[len(current):], `\`)
	var held files
	var digest string
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
			return nil, "", err
		}
		// Metadata-only opens do not participate in NT share-access checks.
		access := uint32(windows.FILE_LIST_DIRECTORY | windows.FILE_READ_ATTRIBUTES)
		if protected {
			access |= windows.READ_CONTROL
		}
		flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT | windows.FILE_FLAG_BACKUP_SEMANTICS)
		if i == len(parts)-1 {
			access = windows.GENERIC_READ
		}
		h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, flags, 0)
		if err != nil {
			return nil, "", err
		}
		f := os.NewFile(uintptr(h), current)
		held = append(held, f)
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return nil, "", errors.New("untrusted binary location")
		}
		if protected && !protectedACL(h) {
			return nil, "", errors.New("installation must be administrator-owned and read-only to users")
		}
		if i == len(parts)-1 {
			hash := sha256.New()
			if _, err := io.Copy(hash, f); err != nil {
				return nil, "", err
			}
			digest = hex.EncodeToString(hash.Sum(nil))
		}
	}
	ok = true
	return held, digest, nil
}
