package compat

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type object = map[string]any
type worker struct {
	cmd          *exec.Cmd
	dir, address string
	done         chan struct{}
}

func (w *worker) running() bool {
	select {
	case <-w.done:
		return false
	default:
		return true
	}
}
func (w *worker) stop() { _ = w.cmd.Process.Kill(); <-w.done; _ = os.RemoveAll(w.dir) }
func (s *service) start(name string, config func(string) object, args ...string) (*worker, error) {
	dir, err := os.MkdirTemp(s.dir, "worker-")
	if err != nil {
		return nil, err
	}
	file := filepath.Join(dir, "upstream.json")
	cfg, err := json.Marshal(config(dir))
	if err == nil {
		err = os.WriteFile(file, cfg, 0600)
	}
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	cmd := exec.CommandContext(s.ctx, filepath.Join(s.root, "opt/nexus-v2/bin", name), append(args, file)...)
	cmd.Dir = dir
	if err = cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	w := &worker{cmd: cmd, dir: dir, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(w.done) }()
	return w, nil
}
func (w *worker) ready(check func() bool) error {
	defer os.Remove(filepath.Join(w.dir, "upstream.json"))
	deadline := time.Now().Add(8 * time.Second)
	for w.running() && time.Now().Before(deadline) {
		if check() {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	w.stop()
	return errors.New("upstream did not become ready")
}

// Include the non-global IPv6 ranges as well as reserved IPv4/IPv6 space.
// Upstreams must check the resolved destination, including names resolving to private IPs.
var denied = strings.Fields("0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.168.0.0/16 192.0.0.0/24 192.0.2.0/24 192.88.99.0/24 198.18.0.0/15 198.51.100.0/24 203.0.113.0/24 224.0.0.0/4 240.0.0.0/4 168.63.129.16/32 ::/3 4000::/2 8000::/1 2002::/16 2001::/23 2001:db8::/32 3fff::/20")

func (s *service) auth(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.URL.Path != "/"+s.statsKey {
		http.NotFound(w, r)
		return
	}
	var input struct{ Auth string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input) != nil {
		w.WriteHeader(400)
		return
	}
	id, pass, ok := strings.Cut(input.Auth, ":")
	s.mu.RLock()
	cred, exists := s.users[id]
	s.mu.RUnlock()
	answer := object{"ok": false}
	if ok && exists && cred.active() && subtle.ConstantTimeCompare([]byte(pass), []byte(cred.Password)) == 1 {
		answer = object{"ok": true, "id": sessionID(cred)}
	}
	_ = json.NewEncoder(w).Encode(answer)
}
func sessionID(c Credential) string { return c.UserID + "-" + strconv.FormatInt(c.Generation, 10) }

func (s *service) startHy() (net.Listener, error) {
	auth, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	slot, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		auth.Close()
		return nil, err
	}
	statsAddr := slot.Addr().String()
	slot.Close()
	s.stats = "http://" + statsAddr
	var key [32]byte
	rand.Read(key[:])
	s.statsKey = hex.EncodeToString(key[:])
	var rules []string
	for _, cidr := range denied {
		rules = append(rules, "reject("+cidr+")")
	}
	etc := filepath.Join(s.root, "etc/nexus-v2")
	w, err := s.start("hysteria", func(_ string) object {
		return object{
			"listen":       fmt.Sprintf(":%d", s.site.Ports["hysteria2"]),
			"tls":          object{"cert": filepath.Join(etc, "cert.pem"), "key": filepath.Join(etc, "key.pem")},
			"auth":         object{"type": "http", "http": object{"url": "http://" + auth.Addr().String() + "/" + s.statsKey}},
			"trafficStats": object{"listen": statsAddr, "secret": s.statsKey},
			"acl":          object{"inline": rules},
		}
	}, "server", "-c")
	if err != nil {
		auth.Close()
		return nil, err
	}
	err = w.ready(func() bool { _, err := s.online(); return err == nil })
	if err != nil {
		auth.Close()
		return nil, err
	}
	s.hy = w
	h := &http.Server{Handler: http.HandlerFunc(s.auth), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second}
	go func() { s.report(h.Serve(auth)) }()
	go func() { <-w.done; s.report(errors.New("Hysteria process exited")) }()
	return auth, nil
}
func (s *service) statsRequest(method, path string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(s.ctx, method, s.stats+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", s.statsKey)
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, errors.New("Hysteria stats rejected request")
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}
func (s *service) online() (map[string]int, error) {
	raw, err := s.statsRequest("GET", "/online", nil)
	if err != nil {
		return nil, err
	}
	var clients map[string]int
	err = json.Unmarshal(raw, &clients)
	return clients, err
}
func (s *service) kickInvalid(users map[string]Credential) error {
	active, err := s.online()
	if err != nil {
		return err
	}
	var removed []string
	for session := range active {
		id, _, _ := strings.Cut(session, "-")
		if c, ok := users[id]; !ok || !c.active() || sessionID(c) != session {
			removed = append(removed, session)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	data, _ := json.Marshal(removed)
	_, err = s.statsRequest("POST", "/kick", bytes.NewReader(data))
	return err
}
