// Package compat applies per-user credentials to locally supervised upstream servers.
package compat

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

type service struct {
	ctx                 context.Context
	root, protocol, dir string
	site                deployment
	applyMu             sync.Mutex
	mu                  sync.RWMutex
	live                Snapshot
	users               map[string]Credential
	xray                *worker
	xrayKey             RealityKey
	nextExpiry          int64
	hy                  *worker
	stats, statsKey     string
	fail                chan error
}

func (c Credential) active() bool { return c.ExpiresAt == 0 || time.Now().Unix() < c.ExpiresAt }

func validHex(s string, n int) bool {
	if len(s) != n || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func (s Snapshot) usersByID() (map[string]Credential, error) {
	if s.Protocol == "reality" {
		k := s.RealityKey
		if k == nil || k.Generation < 1 || !validHex(k.ShortID, 16) {
			return nil, errors.New("invalid Reality node identity")
		}
		key, err := base64.RawURLEncoding.DecodeString(k.PrivateKey)
		if err != nil {
			return nil, errors.New("invalid Reality node key")
		}
		priv, err := ecdh.X25519().NewPrivateKey(key)
		if err != nil || base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()) != k.PublicKey {
			return nil, errors.New("invalid Reality key pair")
		}
	} else if s.RealityKey != nil {
		return nil, errors.New("unexpected Reality identity")
	}
	users := make(map[string]Credential, len(s.Credentials))
	for _, c := range s.Credentials {
		if !validHex(c.UserID, 32) || c.Generation < 1 || c.ExpiresAt < 0 {
			return nil, errors.New("invalid credential metadata")
		}
		if _, ok := users[c.UserID]; ok {
			return nil, errors.New("duplicate user")
		}
		if s.Protocol == "reality" {
			if len(c.UUID) != 36 || !validHex(strings.ReplaceAll(c.UUID, "-", ""), 32) || c.Password != "" {
				return nil, errors.New("invalid Reality identity")
			}
		} else if len(c.Password) < 20 || len(c.Password) > 256 || c.UUID != "" {
			return nil, errors.New("invalid Hysteria identity")
		}
		users[c.UserID] = c
	}
	return users, nil
}

func (s *service) apply(req Snapshot) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	return s.applyLocked(req)
}
func (s *service) applyLocked(req Snapshot) error {
	if req.Protocol != s.protocol || req.Revision < 0 || req.Revision < s.live.Revision {
		return errors.New("outdated protocol snapshot")
	}
	if req.ResetRevision < s.live.ResetRevision || req.ResetRevision > req.Revision || req.Protocol != "reality" && req.ResetRevision != 0 {
		return errors.New("invalid session reset revision")
	}
	users, err := req.usersByID()
	if err != nil {
		return err
	}
	slices.SortFunc(req.Credentials, func(a, b Credential) int { return strings.Compare(a.UserID, b.UserID) })
	if req.Revision == s.live.Revision && s.live.Protocol != "" && (req.ResetRevision != s.live.ResetRevision || !slices.Equal(req.Credentials, s.live.Credentials) || !reflect.DeepEqual(req.RealityKey, s.live.RealityKey)) {
		return errors.New("revision content mismatch")
	}
	// Replaying a snapshot after a lost reply must not disconnect users twice.
	if s.xray != nil && req.ResetRevision > s.live.ResetRevision {
		s.xray.stop()
		s.xray = nil
	}
	s.mu.Lock()
	s.users = users
	s.live = req
	s.mu.Unlock()
	if s.protocol == "reality" {
		return s.reconcileXray()
	}
	if s.protocol == "hysteria2" {
		return s.kickInvalid(users)
	}
	return nil
}

func (s *service) maintain() {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	if s.protocol == "hysteria2" {
		// An in-flight successful auth can be registered after the rotation's first kick.
		_ = s.kickInvalid(s.users)
		return
	}
	if s.xray == nil || !s.xray.running() || (s.nextExpiry > 0 && time.Now().Unix() >= s.nextExpiry) {
		_ = s.reconcileXray()
	}

}

func (s *service) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/ready" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != "POST" || r.URL.Path != "/apply" {
		http.NotFound(w, r)
		return
	}
	var next Snapshot
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&next); err != nil || dec.Decode(new(any)) != io.EOF {
		w.WriteHeader(400)
		return
	}
	if err := s.apply(next); err != nil {
		w.WriteHeader(409)
		return
	}
	w.WriteHeader(204)
}

func Serve(ctx context.Context, root, protocol string, load func(context.Context) (Snapshot, error)) error {
	if !enabled(protocol) {
		return errors.New("unsupported compatibility service")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &service{root: root, ctx: ctx, protocol: protocol, dir: stateDir(root, protocol), fail: make(chan error, 1)}
	raw, err := os.ReadFile(filepath.Join(root, "etc/nexus-v2/deployment.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &s.site); err != nil {
		return err
	}
	port := s.site.Ports[protocol]
	if port < 1 || port > 65535 || protocol == "hysteria2" && s.site.Domain == "" || protocol == "reality" && (len(s.site.ServerNames) == 0 || s.site.Target == "") {
		return errors.New("protocol not configured")
	}
	if err = os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	info, err := os.Stat(s.dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("compatibility state directory must be 0700")
	}
	if protocol == "hysteria2" {
		local, err := s.startHy()
		if err != nil {
			return err
		}
		defer local.Close()
	}
	defer func() {
		cancel()
		s.applyMu.Lock()
		defer s.applyMu.Unlock()
		if s.xray != nil {
			s.xray.stop()
		}
		if s.hy != nil {
			s.hy.stop()
		}
	}()
	path := socket(root, protocol)
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return errors.New("invalid control socket path")
		}
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return errors.New("control socket already active")
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	control, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer control.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	// Bind the control endpoint before reading the DB. Concurrent administrative
	// changes can queue their apply, while public authentication stays closed.
	snapshot, err := load(ctx)
	if err != nil {
		return err
	}
	if err = s.apply(snapshot); err != nil {
		return err
	}
	// Old laboratory builds cached raw credentials here. The ledger now owns all persistent state.
	if err = os.Remove(filepath.Join(s.dir, "active.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	h := &http.Server{Handler: http.HandlerFunc(s.handle), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 15 * time.Second}
	go func() { s.report(h.Serve(control)) }()
	defer h.Close()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-s.fail:
			return err
		case <-ticker.C:
			s.maintain()
		}
	}
}
func (s *service) report(err error) {
	select {
	case s.fail <- err:
	default:
	}
}
