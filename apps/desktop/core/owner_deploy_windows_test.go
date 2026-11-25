package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/libxray/enrollment"
)

// The remote shell is simulated. The final claim uses the real pinned H2
// enrollment client and a fresh Windows key in an isolated test profile.
func TestOwnerDeployFirstIdentity(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	enc := base64.RawURLEncoding
	token := make([]byte, 32)
	rand.Read(token)
	var claims atomic.Int32
	var firstRequest atomic.Pointer[string]
	node := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]string
		if r.ProtoMajor != 2 || r.Method != "POST" || r.URL.Path != "/.well-known/nexus/v2/redeem" ||
			json.NewDecoder(r.Body).Decode(&request) != nil || request["invite"] != enc.EncodeToString(token) ||
			request["public_key"] == "" || request["proof"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		claims.Add(1)
		id := request["request_id"]
		if firstRequest.CompareAndSwap(nil, &id) {
			panic(http.ErrAbortHandler) // model a lost reply after receiving the registration
		}
		if id == "" || id != *firstRequest.Load() {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"v": 2, "device_id": enc.EncodeToString(make([]byte, 16))})
	}))
	node.EnableHTTP2 = true
	node.StartTLS()
	defer node.Close()
	pin := sha256.Sum256(node.Certificate().RawSubjectPublicKeyInfo)
	addr := node.Listener.Addr().(*net.TCPAddr)
	payload, _ := json.Marshal(map[string]any{"v": 2, "host": addr.IP.String(), "port": addr.Port,
		"invite": enc.EncodeToString(token), "pin": enc.EncodeToString(pin[:])})
	link := "nexus://invite/v2#" + enc.EncodeToString(payload)
	invite, err := enrollment.Parse(link)
	if err != nil {
		t.Fatal("invalid test invitation")
	}
	defer func() {
		// Also remove a partial enrollment if a later assertion fails.
		for _, provider := range []string{platformKSP, softwareKSP} {
			if key, err := acquireKey(provider, invite.LocalID(), false); err == nil {
				if err := key.Delete(); err != nil {
					t.Error("could not remove test key")
				}
			}
		}
	}()

	var installed atomic.Bool
	var accounts, invites atomic.Int32
	endpoint, hostPin := testOwnerSSH(t, func(cmd string, _ []byte) (string, string, uint32) {
		switch {
		case strings.Contains(cmd, "test -x /opt/nexus-v2/bin/nexus-node"):
			if installed.Load() {
				return "yes", "", 0
			}
		case cmd == "uname -m":
			return "x86_64", "", 0
		case strings.Contains(cmd, "mktemp -d"):
			return "/tmp/nexus-upload.a123456789", "", 0
		case strings.HasPrefix(cmd, "sha256sum "):
			return ownerNodeRelease.AMD64 + "  nexus-node", "", 0
		case strings.Contains(cmd, "install --non-interactive"):
			installed.Store(true)
		case cmd == "date -u +%s":
			return strconv.FormatInt(time.Now().Unix(), 10), "", 0
		case strings.Contains(cmd, "ledger.db create-user"):
			accounts.Add(1)
			return strings.Repeat("1", 32), "", 0
		case strings.Contains(cmd, "ledger.db invite --link"):
			invites.Add(1)
			return link, "", 0
		}
		return "", "", 0
	})
	req := ownerRequest{Op: "deploy", Address: endpoint, Pin: hostPin, Password: "test-owner-password"}
	if _, err := runOwner(context.Background(), req, func(string) {}); err == nil {
		t.Fatal("deployment ignored an interrupted first registration")
	}
	_, err = runOwner(context.Background(), req, func(string) {})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	_, active, err := currentIdentity()
	if err != nil || active != invite.LocalID() || claims.Load() != 2 || accounts.Load() != 1 || invites.Load() != 1 || !installed.Load() {
		t.Fatal("deploy did not install and activate its first device")
	}
}
