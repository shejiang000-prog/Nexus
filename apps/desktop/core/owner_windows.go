package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"

	"github.com/xtls/libxray/enrollment"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/windows"
)

type ownerError string

func (e ownerError) Error() string { return string(e) }

type ownerRequest struct {
	Op        string `json:"op"`
	Address   string `json:"address"`
	Pin       string `json:"pin"`
	Password  string `json:"password"`
	ServerID  string `json:"serverId"`
	AccountID string `json:"accountId"`
	DeviceID  string `json:"deviceId"`
	Days      *int   `json:"days"`
}

type ownerRecord struct {
	Endpoint, Pin   string
	Seed            []byte
	Ready           bool
	Installing      bool // our key was authorized for an initially empty server
	FirstAccount    string
	FirstInvite     string // encrypted with the rest of the record until redeemed
	FirstInviteEnds int64
}

type ownerStore map[string]*ownerRecord

func (s ownerStore) save(path string) error {
	plain, err := json.Marshal(s)
	if err != nil {
		return ownerError("owner_storage")
	}
	defer clear(plain)
	encrypted, err := sourceDPAPI(plain, true)
	if err != nil || len(encrypted) > 2<<20 || save(path, encrypted) != nil {
		return ownerError("owner_storage")
	}
	return nil
}

func readOwners(path string) (ownerStore, error) {
	s := make(ownerStore)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, ownerError("owner_storage")
	}
	defer f.Close()
	encrypted, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil || len(encrypted) > 2<<20 {
		return nil, ownerError("owner_storage")
	}
	plain, err := sourceDPAPI(encrypted, false)
	if err != nil {
		return nil, ownerError("owner_storage")
	}
	defer clear(plain)
	if json.Unmarshal(plain, &s) != nil || s == nil {
		return nil, ownerError("owner_storage")
	}
	for id, r := range s {
		if !validSourceID(id) || r == nil || len(r.Seed) != ed25519.SeedSize {
			return nil, ownerError("owner_storage")
		}
		endpoint, err := parseOwnerEndpoint(r.Endpoint)
		if err != nil || endpoint != r.Endpoint {
			return nil, ownerError("owner_storage")
		}
	}
	return s, nil
}

func runOwner(ctx context.Context, req ownerRequest, progress func(string)) (any, error) {
	base := os.Getenv("LOCALAPPDATA")
	if !filepath.IsAbs(base) {
		return nil, ownerError("owner_storage")
	}
	dir := filepath.Join(base, "Nexus", "desktop")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, ownerError("owner_storage")
	}
	// One immutable snapshot per operation. Fail fast on competing mutations,
	// including cases where two shells manage the same Windows profile.
	if req.Op != "list" && req.Op != "discover" {
		f, err := os.OpenFile(filepath.Join(dir, "owners.lock"), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, ownerError("owner_storage")
		}
		defer f.Close()
		err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ownerError("owner_busy")
		}
		if err != nil {
			return nil, ownerError("owner_storage")
		}
	}
	path := filepath.Join(dir, "owners.dpapi")
	s, err := readOwners(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, r := range s {
			clear(r.Seed)
		}
	}()
	if req.Op == "list" {
		type row struct {
			ID    string `json:"serverId"`
			Label string `json:"label"`
		}
		rows := []row{}
		for id, r := range s {
			if !r.Ready {
				continue
			}
			label, err := maskedEndpoint(r.Endpoint)
			if err != nil {
				return nil, ownerError("owner_storage")
			}
			rows = append(rows, row{id, label})
		}
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].Label < rows[j].Label || rows[i].Label == rows[j].Label && rows[i].ID < rows[j].ID
		})
		return rows, nil
	}
	if req.Op == "discover" || req.Op == "deploy" || req.Op == "bind" {
		endpoint, err := parseOwnerEndpoint(req.Address)
		if err != nil {
			return nil, err
		}
		var id string
		for key, r := range s {
			if r.Endpoint == endpoint {
				id = key
				break
			}
		}
		progress("connecting")
		if req.Op == "discover" {
			pin, err := ownerDiscover(ctx, endpoint)
			if err != nil {
				return nil, err
			}
			if id != "" && pin != s[id].Pin {
				return nil, ownerError("host_key_changed")
			}
			return map[string]string{"pin": pin}, nil
		}
		if req.Pin == "" || req.Password == "" {
			return nil, ownerError("invalid_request")
		}
		if id != "" && req.Pin != s[id].Pin {
			return nil, ownerError("host_key_changed")
		}
		client, err := ownerConnect(ctx, endpoint, req.Pin, ssh.Password(req.Password))
		req.Password = ""
		if err != nil {
			return nil, err
		}
		defer client.Close()
		if id == "" {
			seed := make([]byte, ed25519.SeedSize)
			if _, err := rand.Read(seed); err != nil {
				return nil, ownerError("owner_storage")
			}
			id = newSourceID()
			s[id] = &ownerRecord{Endpoint: endpoint, Pin: req.Pin, Seed: seed}
			if err := s.save(path); err != nil {
				return nil, err
			}
		}
		if err := s.bootstrap(ctx, path, id, client, req.Op == "deploy", progress); err != nil {
			return nil, err
		}
		return map[string]string{"serverId": id}, nil
	}
	if !validSourceID(req.ServerID) {
		return nil, ownerError("invalid_request")
	}
	r := s[req.ServerID]
	if r == nil || !r.Ready {
		return nil, ownerError("server_not_found")
	}
	if req.Days != nil && (*req.Days < 1 || *req.Days > 3650) {
		return nil, ownerError("invalid_request")
	}
	if req.Op != "invite" && req.Op != "devices" && req.Op != "revoke" && req.Op != "renew" {
		return nil, ownerError("invalid_request")
	}
	progress("connecting")
	client, err := r.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return manageLedger(ctx, client, req)
}

func (r *ownerRecord) signer() (ssh.Signer, error) {
	return ssh.NewSignerFromKey(ed25519.NewKeyFromSeed(r.Seed))
}

func (r *ownerRecord) connect(ctx context.Context) (*ssh.Client, error) {
	key, err := r.signer()
	if err != nil {
		return nil, ownerError("owner_storage")
	}
	return ownerConnect(ctx, r.Endpoint, r.Pin, ssh.PublicKeys(key))
}

func (s ownerStore) bootstrap(ctx context.Context, path, id string, passwordSession *ssh.Client, deploy bool, progress func(string)) error {
	r := s[id]
	key, err := r.signer()
	if err != nil {
		return ownerError("owner_storage")
	}
	progress("binding")
	err = ownerAuthorize(ctx, passwordSession, key.PublicKey(), deploy)
	install := deploy
	if deploy && r.Installing && errors.Is(err, ownerError("already_installed")) {
		// A previous installation reached the server before the local reply.
		err = ownerAuthorize(ctx, passwordSession, key.PublicKey(), false)
		install = false
	}
	passwordSession.Close()
	if err != nil {
		return err
	}
	client, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	if install {
		r.Installing = true
		if err := s.save(path); err != nil {
			return err
		}
		progress("installing")
		if err := ownerInstall(ctx, client, r.Endpoint); err != nil {
			return err
		}
	}
	progress("binding")
	if _, err := ownerDevices(ctx, client); err != nil {
		return err
	}
	r.Ready = true
	if err := s.save(path); err != nil {
		return err
	}
	if !deploy {
		return nil
	}
	progress("enrolling")
	if err := s.enrollFirst(ctx, path, id, client); err != nil {
		return err
	}
	r.Installing = false
	r.FirstInvite = ""
	r.FirstAccount = ""
	r.FirstInviteEnds = 0
	return s.save(path)
}

func (s ownerStore) enrollFirst(ctx context.Context, path, id string, client *ssh.Client) error {
	r := s[id]
	now, err := ownerNow(ctx, client)
	if err != nil {
		return err
	}
	// The ledger allocates a random account. Save its (non-secret) ID on the
	// server before replying, so a dropped SSH reply cannot create two accounts.
	receipt := "\"$HOME/.ssh/.nexus-account-" + id + "\""
	if r.FirstAccount == "" {
		cmd := "umask 077 && if test ! -e " + receipt + "; then (set -C; " + ownerLedger + " create-user > " + receipt + ") || exit 1; fi && cat " + receipt
		account, err := ledgerExec(ctx, client, cmd)
		if err != nil {
			return err
		}
		if !validSourceID(account) {
			return ownerError("ledger_failed")
		}
		r.FirstAccount = account
		if err := s.save(path); err != nil {
			return err
		}
	}
	// From here the DPAPI record is sufficient even if removal is interrupted.
	if _, err := ledgerExec(ctx, client, "rm -f -- "+receipt); err != nil {
		return err
	}
	newInvite := r.FirstInvite == ""
	if newInvite {
		link, end, err := ownerIssueLink(ctx, client, r.FirstAccount, now, nil)
		if err != nil {
			return err
		}
		r.FirstInvite, r.FirstInviteEnds = link, end
	}
	inv, err := enrollment.Parse(r.FirstInvite)
	host, _, _ := net.SplitHostPort(r.Endpoint)
	if err != nil || !net.ParseIP(inv.Host()).Equal(net.ParseIP(host)) {
		return ownerError("enrollment_failed")
	}
	if newInvite {
		if err := s.save(path); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ownerError("cancelled")
	}
	device, err := loadDevice(r.FirstInvite)
	if err != nil {
		// Try the saved invitation first even past expiry: a completed local
		// receipt can survive a crash before owners.dpapi was finalized.
		if errors.Is(err, enrollment.ErrRejected) && now.Unix() >= r.FirstInviteEnds {
			r.FirstInvite = ""
			if err := s.save(path); err != nil {
				return err
			}
		}
		switch code := errorCode(err); code {
		case "invite_rejected", "identity_busy", "key_missing", "node_unreachable":
			return ownerError(code)
		default:
			return ownerError("enrollment_failed")
		}
	}
	device.Signer.Close()
	return nil
}
