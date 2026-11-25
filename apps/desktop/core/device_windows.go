package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/xtls/libxray/enrollment"
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

func loadDevice(link string) (*device, error) {
	base := os.Getenv("LOCALAPPDATA")
	if !filepath.IsAbs(base) {
		return nil, errors.New("missing local profile")
	}
	dir := filepath.Join(base, "Nexus", "desktop")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lockName, err := windows.UTF16PtrFromString(filepath.Join(dir, "enrollment.lock"))
	if err != nil {
		return nil, err
	}
	lock, err := windows.CreateFile(lockName, windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
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
			return nil, err
		}
		alias = invite.LocalID()
	} else {
		raw, err := os.ReadFile(active)
		if err != nil {
			return nil, err
		}
		alias = string(raw)
	}
	if decoded, err := hex.DecodeString(alias); err != nil || len(decoded) != 32 {
		return nil, errors.New("invalid device reference")
	}
	path := filepath.Join(dir, alias+".json")
	var r registration
	var key *deviceKey
	raw, err := os.ReadFile(path)
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
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		if r.Provider != platformKSP && r.Provider != softwareKSP {
			return nil, errors.New("unknown key provider")
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
	if invite != nil {
		if err := save(active, []byte(alias)); err != nil {
			return nil, err
		}
	}
	complete = true
	return &device{Authority: r.Authority, Endpoint: r.Authority, ID: r.ID, Pin: r.Pin, Signer: key}, nil
}
