package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xtls/libxray/enrollment"
	"github.com/xtls/libxray/internal/nexusv2/transport"
	"golang.org/x/sys/windows"
)

type device struct {
	Authority, Endpoint string
	CA, ID, Hello, Pin  []byte
	Signer              *deviceKey
}

// Each invite gets a stable CNG alias. Persist the request before redeeming so a
// retry after a lost receipt uses the same public key and request ID.
type registration struct {
	Authority, Provider string
	Pin, Request, ID    []byte
}

func save(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func (r *registration) save(path string) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return save(path, raw)
}

var (
	errIdentityBusy    = errors.New("identity is in use")
	errIdentityChanged = errors.New("the current identity changed")
	errInviteInvalid   = errors.New("invalid invitation")
	errNoIdentity      = errors.New("no local identity")
	errKeyMissing      = errors.New("local key missing")
)

func errorCode(err error) string {
	var carrier *transport.Error
	switch {
	case errors.Is(err, errIdentityBusy):
		return "identity_busy"
	case errors.Is(err, errIdentityChanged):
		return "identity_changed"
	case errors.Is(err, errInviteInvalid):
		return "invite_invalid"
	case errors.Is(err, enrollment.ErrRejected):
		return "invite_rejected"
	case errors.Is(err, enrollment.ErrUnreachable), errors.As(err, &carrier) && carrier.Code == transport.Interrupted:
		return "node_unreachable"
	case errors.Is(err, errNoIdentity):
		return "no_identity"
	case errors.Is(err, errKeyMissing):
		return "key_missing"
	default:
		return "generic"
	}
}

func lockProfile() (string, windows.Handle, error) {
	base := os.Getenv("LOCALAPPDATA")
	if !filepath.IsAbs(base) {
		return "", 0, errors.New("missing profile directory")
	}
	dir := filepath.Join(base, "Nexus", "desktop")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", 0, err
	}
	name, err := windows.UTF16PtrFromString(filepath.Join(dir, "enrollment.lock"))
	if err != nil {
		return "", 0, err
	}
	lock, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	return dir, lock, err
}

func activeAlias(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "active"))
	if errors.Is(err, os.ErrNotExist) {
		return "", errNoIdentity
	}
	if err != nil {
		return "", err
	}
	if decoded, err := hex.DecodeString(string(raw)); err != nil || len(decoded) != 32 {
		return "", errors.New("invalid device reference")
	}
	return string(raw), nil
}

func readRegistration(path string) (registration, error) {
	var r registration
	raw, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	if r.Provider != platformKSP && r.Provider != softwareKSP {
		return r, errors.New("unknown key provider")
	}
	return r, nil
}

func maskedEndpoint(authority string) (string, error) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		return "", err
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 || len(host) == 0 {
		return "", errors.New("invalid node address")
	}
	var mask string
	if ip := net.ParseIP(host); ip != nil {
		if v := ip.To4(); v != nil {
			mask = fmt.Sprintf("%d.%d.*.*", v[0], v[1])
		} else {
			mask = fmt.Sprintf("[%x:*]", uint16(ip[0])<<8|uint16(ip[1]))
		}
	} else {
		c := host[0]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return "", errors.New("invalid node name")
		}
		mask = string(c) + "***"
	}
	return mask + ":" + strconv.FormatUint(n, 10), nil
}

func currentIdentity() (string, string, error) {
	dir, lock, err := lockProfile()
	if err != nil {
		return "", "", err
	}
	defer windows.CloseHandle(lock)
	alias, err := activeAlias(dir)
	if errors.Is(err, errNoIdentity) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	r, err := readRegistration(filepath.Join(dir, alias+".json"))
	if errors.Is(err, os.ErrNotExist) {
		// Finish a removal interrupted after the key and record were deleted.
		return "", "", os.Remove(filepath.Join(dir, "active"))
	}
	if err != nil {
		return "", "", err
	}
	label, err := maskedEndpoint(r.Authority)
	if err != nil {
		return "", "", err
	}
	return label, alias, nil
}

// Completed registrations are immutable. Share reads among running cores and
// acquire exclusive access before removing their key. Enrollment.lock gates
// new leases while removal is in progress.
func openRecord(path string, access, share uint32) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(name, access, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
}

func removeDevice(expected string) error {
	dir, lock, err := lockProfile()
	if err != nil {
		return err
	}
	defer windows.CloseHandle(lock)
	alias, err := activeAlias(dir)
	if errors.Is(err, errNoIdentity) {
		return nil
	}
	if err != nil {
		return err
	}
	// Compare the full, previously displayed identity under the enrollment lock.
	// Different identities may have identical masked endpoint labels.
	if expected != alias || expected != strings.ToLower(expected) {
		return errIdentityChanged
	}
	path := filepath.Join(dir, alias+".json")
	r, err := readRegistration(path)
	if err == nil {
		lease, err := openRecord(path, windows.DELETE, 0)
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return errIdentityBusy
		}
		if err != nil {
			return err
		}
		// No new readers can acquire a lease until we release enrollment.lock.
		if err := windows.CloseHandle(lease); err != nil {
			return err
		}
		key, err := acquireKey(r.Provider, alias, false)
		if err != nil && !errors.Is(err, errKeyMissing) {
			return err
		}
		if key != nil {
			if err := key.Delete(); err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Remove(filepath.Join(dir, "active"))
}

func loadDevice(link string) (*device, error) {
	dir, lock, err := lockProfile()
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(lock)

	var invite *enrollment.Invitation
	var alias string
	active := filepath.Join(dir, "active")
	if link = strings.TrimSpace(link); link != "" {
		invite, err = enrollment.Parse(link)
		if err != nil {
			return nil, errInviteInvalid
		}
		alias = invite.LocalID()
	} else {
		alias, err = activeAlias(dir)
		if err != nil {
			return nil, err
		}
	}
	path := filepath.Join(dir, alias+".json")
	var key *deviceKey
	r, err := readRegistration(path)
	if errors.Is(err, os.ErrNotExist) && invite != nil {
		r = registration{Authority: invite.Authority(), Pin: invite.Pin(), Request: make([]byte, 16), Provider: platformKSP}
		if _, err := rand.Read(r.Request); err != nil {
			return nil, err
		}
		key, err = acquireKey(r.Provider, alias, true)
		if err != nil {
			r.Provider = softwareKSP
			key, err = acquireKey(r.Provider, alias, true)
		}
		if err != nil {
			return nil, err
		}
		if err := r.save(path); err != nil {
			key.Close()
			return nil, err
		}
	} else {
		if err != nil {
			return nil, err
		}
		if invite != nil && (r.Authority != invite.Authority() || !bytes.Equal(r.Pin, invite.Pin())) {
			return nil, errors.New("invitation identity changed")
		}
		key, err = acquireKey(r.Provider, alias, false)
		if err != nil {
			return nil, err
		}
	}
	complete := false
	defer func() {
		if !complete {
			key.Close()
		}
	}()
	if len(r.ID) == 0 {
		if invite == nil || len(r.Request) != 16 {
			return nil, errors.New("invitation required")
		}
		public, err := key.public()
		if err != nil {
			return nil, err
		}
		receipt, err := enrollment.Redeem(invite, r.Authority, nil, r.Request, public, key)
		if err != nil {
			return nil, err
		}
		r.ID, err = hex.DecodeString(receipt.DeviceID)
		if err != nil || len(r.ID) != 16 {
			return nil, errors.New("invalid receipt")
		}
		r.Request = nil
		if err := r.save(path); err != nil {
			return nil, err
		}
	}
	if len(r.ID) != 16 {
		return nil, errors.New("invalid device ID")
	}
	lease, err := openRecord(path, windows.GENERIC_READ, windows.FILE_SHARE_READ)
	if err != nil {
		return nil, err
	}
	key.lease = lease
	if invite != nil {
		if err := save(active, []byte(alias)); err != nil {
			return nil, err
		}
	}
	complete = true
	return &device{Authority: r.Authority, Endpoint: r.Authority, ID: r.ID, Pin: r.Pin, Signer: key}, nil
}
