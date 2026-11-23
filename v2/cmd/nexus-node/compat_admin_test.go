package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shejiang000-prog/Nexus/v2/compat"
	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/server"
	"go.yaml.in/yaml/v3"
)

func TestLocalCompatDelivery(t *testing.T) {
	root, err := os.MkdirTemp("", "nx-compat-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	etc := filepath.Join(root, "etc/nexus-v2")
	state := filepath.Join(root, "var/lib/nexus-v2")
	for _, dir := range []string{etc, state} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	db := filepath.Join(state, "ledger.db")
	l, err := server.OpenLedger(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.CreateUser(t.Context(), proof.ID{}); err != nil {
		t.Fatal(err)
	}
	l.Close()
	if err := os.WriteFile(filepath.Join(etc, "deployment.json"), []byte(`{"domain":"test.invalid","reality_server_names":["primary.invalid","other.invalid"],"ports":{"reality":2443,"hysteria2":3443}}`), 0600); err != nil {
		t.Fatal(err)
	}
	buf := new(bytes.Buffer)
	user := strings.Repeat("0", 32)
	run := func(verb string, params ...string) error {
		t.Helper()
		buf.Reset()
		args := append([]string{db, "compat", verb}, params...)
		handled, err := compatAdmin(append(args, "--root", root), buf)
		if !handled {
			t.Fatal("missing compat dispatcher")
		}
		return err
	}
	reality := make(chan compat.Snapshot, 2)
	hy := make(chan compat.Snapshot, 2)
	for p, requests := range map[string]chan compat.Snapshot{"reality": reality, "hysteria2": hy} {
		dir := filepath.Join(state, "compat-"+p)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
		if err != nil {
			t.Fatal(err)
		}
		api := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var s compat.Snapshot
			if err := json.NewDecoder(r.Body).Decode(&s); err != nil || s.Protocol != p {
				http.Error(w, "invalid", 400)
				return
			}
			requests <- s
			w.WriteHeader(http.StatusNoContent)
		})}
		go api.Serve(listener)
		defer api.Close()
	}
	for _, p := range []string{"reality", "hysteria2"} {
		if err := run("enable", user, p); err != nil {
			t.Fatal(err)
		}
	}
	rs := <-reality
	rc := rs.Credentials[0]
	key := rs.RealityKey
	hc := (<-hy).Credentials[0]
	if err := run("export", user, "links"); err != nil {
		t.Fatal(err)
	}
	raw := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(raw) != 2 || strings.Contains(buf.String(), key.PrivateKey) {
		t.Fatal("invalid link projection")
	}
	v, e1 := url.Parse(raw[0])
	h, e2 := url.Parse(raw[1])
	if e1 != nil || e2 != nil {
		t.Fatal("invalid link syntax")
	}
	pw, _ := h.User.Password()
	if v.Scheme != "vless" || v.User.Username() != rc.UUID || v.Query().Get("pbk") != key.PublicKey || v.Query().Get("sid") != key.ShortID || v.Query().Get("sni") != "primary.invalid" || v.Port() != "2443" || h.Scheme != "hysteria2" || h.Port() != "3443" || h.User.Username() != user || pw != hc.Password {
		t.Fatal("wrong link fields")
	}
	if err := run("export", user, "mihomo"); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Proxies []struct {
			Type, Server, Password, UUID string
			Port                         int
			TLS                          bool
			VerifyOff                    bool              `yaml:"skip-cert-verify"`
			Reality                      map[string]string `yaml:"reality-opts"`
		}
	}
	if err := yaml.Unmarshal(buf.Bytes(), &doc); err != nil || len(doc.Proxies) != 2 {
		t.Fatal("invalid mihomo YAML")
	}
	vp, hp := doc.Proxies[0], doc.Proxies[1]
	if vp.Type != "vless" || vp.UUID != rc.UUID || !vp.TLS || vp.Reality["public-key"] != key.PublicKey || hp.Type != "hysteria2" || hp.Password != user+":"+hc.Password || hp.VerifyOff {
		t.Fatal("wrong mihomo fields")
	}
	if err := run("rotate-node-key", "reality"); err != nil {
		t.Fatal(err)
	}
	rotated := <-reality
	if rotated.RealityKey.Generation != key.Generation+1 || rotated.RealityKey.PublicKey == key.PublicKey || rotated.Credentials[0] != rc {
		t.Fatal("wrong node key rotation scope")
	}
	buf.Reset()
	_, err = compatAdmin([]string{db, "revoke-user", user, "--root", root}, buf)
	if err != nil {
		t.Fatal(err)
	}
	if len((<-reality).Credentials) != 0 || len((<-hy).Credentials) != 0 {
		t.Fatal("revoke-user retained compatibility credentials")
	}
	if err := run("export", user, "links"); err == nil {
		t.Fatal("export after revoke")
	}
	// A was gracefully disabled, then B requested a reset that was never
	// delivered. A's strict revoke must send the covered reset to the service.
	if err := run("enable", user, "reality"); err != nil {
		t.Fatal(err)
	}
	<-reality
	if err := run("disable", user, "reality", "--keep-connections"); err != nil {
		t.Fatal(err)
	}
	soft := <-reality
	l, err = server.OpenLedger(db)
	if err != nil {
		t.Fatal(err)
	}
	b := proof.ID{1}
	if err := l.CreateUser(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if err := l.ChangeCompat(t.Context(), b, "reality", "enable"); err != nil {
		t.Fatal(err)
	}
	if err := l.ChangeCompat(t.Context(), b, "reality", "rotate"); err != nil {
		t.Fatal(err)
	}
	l.Close()
	if _, err = compatAdmin([]string{db, "revoke-user", user, "--root", root}, buf); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-reality:
		if received.ResetRevision <= soft.Revision || len(received.Credentials) != 1 {
			t.Fatal("failed to send outstanding reset")
		}
	default:
		t.Fatal("acknowledged user row hid undelivered reset")
	}

}

func TestRealityDeliveryWithoutDomain(t *testing.T) {
	cfg := compatDeployment{PublicHost: "2001:db8::2", RealityServerNames: []string{"target.invalid"}, Ports: map[string]int{"reality": 443}}
	creds := map[string]server.CompatShare{"reality": {UUID: "test-uuid", PublicKey: "test-key", ShortID: "1234"}}
	var out bytes.Buffer
	if err := exportCompat(&out, cfg, creds, "links"); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(strings.TrimSpace(out.String()))
	if err != nil || u.Hostname() != cfg.PublicHost || u.Port() != "443" || u.Query().Get("sni") != "target.invalid" {
		t.Fatal("invalid IP delivery")
	}
	out.Reset()
	if err := exportCompat(&out, cfg, creds, "mihomo"); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Proxies []struct{ Server, Servername string }
	}
	if err := yaml.Unmarshal(out.Bytes(), &doc); err != nil || len(doc.Proxies) != 1 || doc.Proxies[0].Server != cfg.PublicHost || doc.Proxies[0].Servername != "target.invalid" {
		t.Fatal("invalid mihomo IP delivery")
	}
}
