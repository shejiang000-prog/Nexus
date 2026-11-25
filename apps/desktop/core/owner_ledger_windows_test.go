package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestOwnerManagement(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("LOCALAPPDATA", profile)
	const user = "11111111111111111111111111111111"
	const device = "22222222222222222222222222222222"
	const pinValue = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	link := "nexus://invite/v2#" + base64.RawURLEncoding.EncodeToString([]byte(`{"v":2,"host":"127.0.0.1","port":443,"invite":"`+pinValue+`","pin":"`+pinValue+`"}`))
	var calls []string
	var mu sync.Mutex
	endpoint, pin := testOwnerSSH(t, func(cmd string, _ []byte) (string, string, uint32) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, cmd)
		switch {
		case cmd == ownerInstalled:
			return "yes", "", 0
		case strings.HasPrefix(cmd, "umask 077 && mkdir -p ~/.ssh") && strings.Contains(cmd, "restrict ssh-ed25519 "):
			return "", "", 0
		case cmd == ownerLedger+" devices":
			return `{"id":"` + device + `","user_id":"` + user + `","name":"127.0.0.1","revoked_at":null,"expires_at":null}`, "", 0
		case cmd == ownerLedger+" users":
			return `{"id":"` + user + `","expires_at":1700172800}`, "", 0
		case cmd == "date -u +%s":
			return "1700000000", "", 0
		case cmd == ownerLedger+" create-user", cmd == ownerLedger+" create-user 2023-11-16T22:13:20Z":
			return user, "", 0
		case cmd == ownerLedger+" invite --link "+user+" 2023-11-21T22:13:20Z", cmd == ownerLedger+" invite --link "+user+" 2023-11-16T22:13:20Z":
			return link, "", 0
		case cmd == ownerLedger+" revoke "+device, cmd == ownerLedger+" renew "+user+" never", cmd == ownerLedger+" renew "+user+" 2023-11-18T22:13:20Z":
			return "", "", 0
		}
		t.Error("unexpected SSH command")
		return "", "", 1
	})
	call := func(r ownerRequest) any {
		t.Helper()
		result, err := runOwner(context.Background(), r, func(string) {})
		if err != nil {
			t.Fatalf("%s: %v", r.Op, err)
		}
		return result
	}
	if call(ownerRequest{Op: "discover", Address: endpoint}).(map[string]string)["pin"] != pin {
		t.Fatal("discovery")
	}
	reply := call(ownerRequest{Op: "bind", Address: endpoint, Pin: pin, Password: "test-owner-password"}).(map[string]string)
	id := reply["serverId"]
	if !validSourceID(id) {
		t.Fatal("server id")
	}
	list, err := json.Marshal(call(ownerRequest{Op: "list"}))
	if err != nil || !bytes.Contains(list, []byte(id)) || bytes.Contains(list, []byte(endpoint)) {
		t.Fatal("masked server list")
	}
	path := filepath.Join(profile, "Nexus", "desktop", "owners.dpapi")
	raw, err := os.ReadFile(path)
	if err != nil || bytes.Contains(raw, []byte(endpoint)) {
		t.Fatal("unprotected owner record")
	}
	body, err := sourceDPAPI(raw, false)
	if err != nil || !bytes.Contains(body, []byte(endpoint)) || bytes.Contains(body, []byte("test-owner-password")) {
		t.Fatal("invalid DPAPI payload")
	}
	clear(body)
	days := 2
	for _, d := range []*int{nil, &days} {
		if call(ownerRequest{Op: "invite", ServerID: id, Days: d}).(map[string]string)["text"] != inviteMessage+link {
			t.Fatal("share text")
		}
		call(ownerRequest{Op: "renew", ServerID: id, AccountID: user, Days: d})
	}
	devices := call(ownerRequest{Op: "devices", ServerID: id}).([]ownerDeviceView)
	if len(devices) != 1 || devices[0] != (ownerDeviceView{DeviceID: device, AccountID: user, Name: device[:8], ExpiryKnown: true}) {
		t.Fatal("device view")
	}
	call(ownerRequest{Op: "revoke", ServerID: id, DeviceID: device})
	mu.Lock()
	defer mu.Unlock()
	seen := map[string]bool{}
	for _, cmd := range calls {
		seen[cmd] = true
	}
	for _, suffix := range []string{"create-user", "create-user 2023-11-16T22:13:20Z", "invite --link " + user + " 2023-11-21T22:13:20Z", "invite --link " + user + " 2023-11-16T22:13:20Z", "renew " + user + " never", "renew " + user + " 2023-11-18T22:13:20Z", "revoke " + device} {
		if !seen[ownerLedger+" "+suffix] {
			t.Fatalf("missing ledger round trip: %s", suffix)
		}
	}
}
