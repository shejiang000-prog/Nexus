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
	workers             map[string]*worker
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
	users := make(map[string]Credential, len(s.Credentials))
	for _, c := range s.Credentials {
		if !validHex(c.UserID, 32) || c.Generation < 1 || c.ExpiresAt < 0 {
			return nil, errors.New("invalid credential metadata")
		}
		if _, ok := users[c.UserID]; ok {
			return nil, errors.New("duplicate user")
		}
		if s.Protocol == "reality" {
			key, err := base64.RawURLEncoding.DecodeString(c.PrivateKey)
			if err != nil {
				return nil, errors.New("invalid key")
			}
			priv, err := ecdh.X25519().NewPrivateKey(key)
			if err != nil || base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()) != c.PublicKey || !validHex(c.ShortID, 16) || len(c.UUID) != 36 || !validHex(strings.ReplaceAll(c.UUID, "-", ""), 32) || c.Password != "" {
				return nil, errors.New("invalid Reality identity")
			}
		} else if len(c.Password) < 20 || len(c.Password) > 256 || c.PrivateKey != "" || c.UUID != "" {
			return nil, errors.New("invalid Hysteria identity")
		}
		users[c.UserID] = c
	}
	return users, nil
}

func saveFile(dir, name string, data []byte) error {
	f, err := os.CreateTemp(dir, ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (s *service) apply(req Snapshot, persist bool) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	return s.applyLocked(req, persist)
}
func (s *service) applyLocked(req Snapshot, persist bool) error {
	if req.Protocol != s.protocol || req.Revision < 0 || req.Revision < s.live.Revision {
		return errors.New("outdated protocol snapshot")
	}
	users, err := req.usersByID()
	if err != nil {
		return err
	}
	slices.SortFunc(req.Credentials, func(a, b Credential) int { return strings.Compare(a.UserID, b.UserID) })
	if req.Revision == s.live.Revision && s.live.Protocol != "" && !slices.Equal(req.Credentials, s.live.Credentials) {
		return errors.New("revision content mismatch")
	}
	workers := map[string]*worker{}
	var started []*worker
	defer func() {
		for _, w := range started {
			w.stop()
		}
	}()
	for id, c := range users {
		if s.protocol != "reality" || !c.active() {
			continue
		}
		if w := s.workers[id]; w != nil && s.users[id] == c && w.running() {
			workers[id] = w
			continue
		}
		w, err := s.startXray(c)
		if err != nil {
			return err
		}
		started = append(started, w)
		workers[id] = w
	}
	if persist {
		raw, err := json.Marshal(req)
		if err != nil {
			return err
		}
		if err = saveFile(s.dir, "active.json", raw); err != nil {
			return err
		}
	}
	s.mu.Lock()
	old := s.workers
	s.users = users
	s.workers = workers
	s.live = req
	s.mu.Unlock()
	started = nil
	for id, w := range old {
		if workers[id] != w {
			w.stop()
		}
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
	for id, c := range s.users {
		w := s.workers[id]
		if c.active() && (w == nil || !w.running()) || !c.active() && w != nil {
			// Retry just the missing worker. The apply path reuses every healthy generation.
			_ = s.applyLocked(s.live, false)
			return
		}
	}
}

func (s *service) handle(w http.ResponseWriter, r *http.Request) {
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
	if err := s.apply(next, true); err != nil {
		w.WriteHeader(409)
		return
	}
	w.WriteHeader(204)
}

func Serve(ctx context.Context, root, protocol string) error {
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
	if port < 1 || port > 65535 || s.site.Domain == "" {
		return errors.New("protocol not configured")
	}
	if err = os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	info, err := os.Stat(s.dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("compatibility state directory must be 0700")
	}
	var snapshot = Snapshot{Protocol: protocol}
	raw, err = os.ReadFile(filepath.Join(s.dir, "active.json"))
	if err == nil {
		if err = json.Unmarshal(raw, &snapshot); err != nil {
			return errors.New("invalid compatibility snapshot")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	var local net.Listener
	if protocol == "hysteria2" {
		local, err = s.startHy()
	} else {
		local, err = s.listenReality(port)
	}
	if err != nil {
		return err
	}
	defer local.Close()
	defer func() {
		cancel()
		s.applyMu.Lock()
		defer s.applyMu.Unlock()
		for _, w := range s.workers {
			w.stop()
		}
		if s.hy != nil {
			s.hy.stop()
		}
	}()
	if err = s.apply(snapshot, false); err != nil {
		return err
	}
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
