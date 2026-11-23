//go:build linux

package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	quic "github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
)

const (
	acmeLabDomain = "nexus.test"
	acmeLabRoot   = "/etc/nexus-v2/acme-root.pem"
	acmeLabConfig = "/etc/nexus-v2/v2.json"
	acmeLabSocket = "/var/lib/nexus-v2/admin.sock"
	acmeLabBin    = "/opt/nexus-v2/bin/nexus-node"
)

func TestInstalledACMERenewal(t *testing.T) {
	if os.Getenv("NEXUS_TEST_INSTALLED") != "1" || os.Getenv("NEXUS_TEST_ACME_RENEWAL") != "1" {
		t.Skip("requires disposable three-protocol NexusLab private ACME installation")
	}
	if os.Getenv("WSL_DISTRO_NAME") != "NexusLab" {
		t.Fatal("ACME renewal test can only run on disposable NexusLab")
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	var site struct {
		Domain      string
		Ports       map[string]int
		Certificate string
		KeyFile     string `json:"key_file"`
	}
	b, err := os.ReadFile("/etc/nexus-v2/deployment.json")
	check(err)
	check(json.Unmarshal(b, &site))
	const storage = "/var/lib/nexus-v2/caddy/certificates/"
	if site.Domain != acmeLabDomain || !strings.HasPrefix(filepath.Clean(site.Certificate), storage) || !strings.HasPrefix(filepath.Clean(site.KeyFile), storage) {
		t.Fatal("requires the managed nexus.test lab certificate")
	}
	for _, p := range []string{"v2", "reality", "hysteria2"} {
		if site.Ports[p] < 1 || site.Ports[p] > 65535 {
			t.Fatal("requires a three-protocol installation")
		}
	}
	base, err := os.ReadFile(acmeLabConfig)
	check(err)
	var guard struct {
		Admin struct{ Listen string }
		Apps  struct {
			TLS struct {
				Certificates struct{ Automate []string }
				Automation   struct {
					Policies []struct {
						Subjects []string
						Issuers  []struct {
							Module, CA string
							Roots      []string `json:"trusted_roots_pem_files"`
						}
					}
				}
			}
		}
	}
	check(json.Unmarshal(base, &guard))
	policies := guard.Apps.TLS.Automation.Policies
	if guard.Admin.Listen != "unix/"+acmeLabSocket || !slices.Equal(guard.Apps.TLS.Certificates.Automate, []string{acmeLabDomain}) || len(policies) != 1 || !slices.Equal(policies[0].Subjects, []string{acmeLabDomain}) || len(policies[0].Issuers) != 1 {
		t.Fatal("requires the dedicated nexus.test ACME policy")
	}
	issuer := policies[0].Issuers[0]
	ca, err := url.Parse(issuer.CA)
	check(err)
	if issuer.Module != "acme" || !slices.Equal(issuer.Roots, []string{acmeLabRoot}) || ca.Scheme != "https" || ca.User != nil || ca.Hostname() == "" {
		t.Fatal("requires a private HTTPS ACME directory")
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, ca.Hostname())
	check(err)
	if len(addrs) == 0 {
		t.Fatal("empty ACME directory address")
	}
	for _, addr := range addrs {
		if !addr.IP.IsLoopback() {
			t.Fatal("ACME directory must resolve exclusively to loopback")
		}
	}
	roots := x509.NewCertPool()
	b, err = os.ReadFile(acmeLabRoot)
	check(err)
	if !roots.AppendCertsFromPEM(b) {
		t.Fatal("empty lab root pool")
	}
	// Refuse to overwrite a prior test's active runtime configuration.
	adminTransport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", acmeLabSocket)
	}}
	defer adminTransport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", "http://localhost/config/", nil)
	check(err)
	current, err := (&http.Client{Transport: adminTransport}).Do(req)
	check(err)
	var active, cfg map[string]any
	defer current.Body.Close()
	check(json.NewDecoder(current.Body).Decode(&active))
	check(json.Unmarshal(base, &cfg))
	if current.StatusCode != 200 || !reflect.DeepEqual(cfg, active) {
		t.Fatal("active Caddy configuration differs from installed lab configuration")
	}

	admin := func(args ...string) string {
		t.Helper()
		args = append([]string{"ledger", "/var/lib/nexus-v2/ledger.db"}, args...)
		out, err := exec.CommandContext(ctx, "runuser", append([]string{"-u", "nexus-v2", "--", acmeLabBin}, args...)...).Output()
		if err != nil {
			t.Fatalf("ledger operation %s failed: %v", args[2], err)
		}
		return strings.TrimSpace(string(out))
	}
	user := admin("create-user", time.Now().Add(30*time.Minute).UTC().Format(time.RFC3339))
	defer func() {
		c, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		if err := exec.CommandContext(c, "runuser", "-u", "nexus-v2", "--", acmeLabBin, "ledger", "/var/lib/nexus-v2/ledger.db", "revoke-user", user).Run(); err != nil {
			t.Errorf("revoke test user: %v", err)
		}
	}()
	admin("compat", "enable", user, "hysteria2")
	share, err := url.Parse(admin("compat", "export", user, "links"))
	if err != nil || share.Scheme != "hysteria2" || share.User == nil || share.User.Username() != user {
		t.Fatal("invalid lab user export")
	}
	password, ok := share.User.Password()
	if !ok || password == "" {
		t.Fatal("missing lab user password")
	}
	dial := func(p string) (*quic.Conn, error) {
		dctx, done := context.WithTimeout(ctx, 3*time.Second)
		defer done()
		return quic.DialAddr(dctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(site.Ports[p])),
			&tls.Config{RootCAs: roots, ServerName: acmeLabDomain, NextProtos: []string{"h3"}, MinVersion: tls.VersionTLS13},
			&quic.Config{EnableDatagrams: true, KeepAlivePeriod: 2 * time.Second})
	}
	request := func(conn *http3.ClientConn, path, auth string) (int, string, http.Header) {
		t.Helper()
		method, host := "GET", acmeLabDomain
		if auth != "" {
			method, host = "POST", "hysteria"
		}
		rctx, done := context.WithTimeout(ctx, 5*time.Second)
		defer done()
		r, err := http.NewRequestWithContext(rctx, method, "https://"+host+path, nil)
		check(err)
		if auth != "" {
			r.Header.Set("Hysteria-Auth", auth)
			r.Header.Set("Hysteria-CC-RX", "0")
		}
		resp, err := conn.RoundTrip(r)
		check(err)
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
		check(err)
		if len(data) > 8192 {
			t.Fatal("unexpected lab response size")
		}
		return resp.StatusCode, string(data), resp.Header
	}
	const renewalRatio = 0.75
	automation := cfg["apps"].(map[string]any)["tls"].(map[string]any)["automation"].(map[string]any)
	automation["renew_interval"] = "1s"
	automation["policies"].([]any)[0].(map[string]any)["renewal_window_ratio"] = renewalRatio
	accelerated, err := json.Marshal(cfg)
	check(err)
	prepareACMELabTimer(t, accelerated)
	// Wait out a stale certificate or an initial start-up renewal. Only accept
	// a valid common leaf whose renewal window is still in the future.
	var public, local *quic.Conn
	var before *x509.Certificate
	var windowStart time.Time
	ready := time.Now().Add(80 * time.Second)
	for time.Now().Before(ready) {
		public, err = dial("v2")
		if err == nil {
			local, err = dial("hysteria2")
			if err == nil {
				before = local.ConnectionState().TLS.PeerCertificates[0]
				lifetime := before.NotAfter.Sub(before.NotBefore)
				windowStart = before.NotBefore.Add(time.Duration(float64(lifetime) * (1 - renewalRatio)))
				if lifetime > 10*time.Minute {
					t.Fatal("requires short-lived lab certificates")
				}
				if bytes.Equal(public.ConnectionState().TLS.PeerCertificates[0].Raw, before.Raw) && time.Until(windowStart) > 4*time.Second {
					break
				}
				local.CloseWithError(0, "waiting for initial lab leaf")
				local = nil
			}
			public.CloseWithError(0, "waiting for initial lab leaf")
		}
		time.Sleep(300 * time.Millisecond)
	}
	if local == nil {
		t.Fatalf("no shared unexpired certificate before its renewal window: %v", err)
	}
	defer local.CloseWithError(0, "test complete")
	defer public.CloseWithError(0, "test complete")
	h3 := http3.Transport{EnableDatagrams: true, DisableCompression: true}
	hy := h3.NewClientConn(local)
	web := h3.NewClientConn(public)
	for _, tc := range []struct {
		path, auth string
		status     int
	}{{"/", "", 200}, {"/absent-acme-test", "", 404}, {"/auth", "invalid-lab-auth", 404}} {
		left, lb, lh := request(web, tc.path, tc.auth)
		right, rb, rh := request(hy, tc.path, tc.auth)
		if left != tc.status || right != left || rb != lb {
			t.Fatalf("site parity for %s: Caddy %d, Hysteria2 %d, body match %t", tc.path, left, right, rb == lb)
		}
		for _, k := range []string{"Content-Type", "Content-Encoding", "Cache-Control", "Location", "Server", "WWW-Authenticate", "Hysteria-UDP", "Hysteria-CC-RX"} {
			if !slices.Equal(lh.Values(k), rh.Values(k)) {
				t.Fatalf("site response header %s differs on %s", k, tc.path)
			}
		}
	}
	authenticate := func() {
		t.Helper()
		if code, _, _ := request(hy, "/auth", user+":"+password); code != 233 {
			t.Fatalf("Hysteria2 authentication status %d", code)
		}
	}
	authenticate()
	started := time.Now()
	if !started.Before(windowStart) {
		t.Fatal("test connection was authenticated after the leaf entered its renewal window")
	}
	// This ClientConn is pinned to a single QUIC connection. RoundTrip cannot
	// reconnect while the test waits for both new handshakes to change leaf.
	leaf := func(p string) (*x509.Certificate, error) {
		q, err := dial(p)
		if err != nil {
			return nil, err
		}
		defer q.CloseWithError(0, "leaf checked")
		return q.ConnectionState().TLS.PeerCertificates[0], nil
	}
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	pulses := 0
	lastPulse := started
	for time.Now().Before(deadline) {
		c, e1 := leaf("v2")
		h, e2 := leaf("hysteria2")
		lastErr = errors.Join(e1, e2)
		if time.Since(lastPulse) >= 2*time.Second {
			authenticate()
			pulses++
			lastPulse = time.Now()
		}
		if lastErr == nil && c.SerialNumber.Cmp(before.SerialNumber) != 0 && bytes.Equal(c.Raw, h.Raw) {
			b, err := os.ReadFile(site.Certificate)
			check(err)
			block, _ := pem.Decode(b)
			if block == nil || !bytes.Equal(c.Raw, block.Bytes) {
				continue
			}
			authenticate()
			if time.Now().Before(windowStart) {
				t.Fatal("observed new leaf before renewal window")
			}
			t.Logf("Automatic renewal changed leaf %s -> %s on both ports after %s; original QUIC connection retained status 233 (%d intermediate checks)", before.SerialNumber.Text(16), c.SerialNumber.Text(16), time.Since(started).Round(time.Millisecond), pulses)
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatalf("both fresh handshakes did not converge on a renewed ACME leaf: %v", lastErr)
}

// CertMagic creates its maintenance ticker at process start. Install a private
// lab config before measurement, and retain the original inode for restoration.
func prepareACMELabTimer(t *testing.T, accelerated []byte) {
	t.Helper()
	const caddyUnit = "nexus-v2.service"
	const hyUnit = "nexus-v2-hysteria2.service"
	systemctl := func(args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "systemctl", args...).Run()
	}
	for _, unit := range []string{caddyUnit, hyUnit} {
		if err := systemctl("is-active", "--quiet", unit); err != nil {
			t.Fatalf("requires active %s: %v", unit, err)
		}
	}
	restart := func() error {
		if err := systemctl("restart", caddyUnit); err != nil {
			return err
		}
		// Hysteria2 requires Caddy. Some systemd versions stop the dependent
		// while restarting its prerequisite.
		if err := systemctl("is-active", "--quiet", hyUnit); err != nil {
			return systemctl("start", hyUnit)
		}
		return nil
	}
	info, err := os.Lstat(acmeLabConfig)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("requires a regular private installer configuration")
	}
	st := info.Sys().(*syscall.Stat_t)
	dir, err := os.MkdirTemp(filepath.Dir(acmeLabConfig), ".acme-lab-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(dir); err != nil {
			t.Errorf("temporary lab config retained in %s: %v", dir, err)
		}
	})
	tmp := filepath.Join(dir, "accelerated.json")
	if err = os.WriteFile(tmp, accelerated, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmp) })
	if err = os.Chown(tmp, int(st.Uid), int(st.Gid)); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(tmp, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(dir, "original.json")
	if err = os.Link(acmeLabConfig, original); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(tmp, acmeLabConfig); err != nil {
		os.Remove(original)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Rename(original, acmeLabConfig); err != nil {
			t.Errorf("restore original lab configuration: %v", err)
			return
		}
		if err := restart(); err != nil {
			t.Errorf("restart restored Caddy: %v", err)
		}
	})
	if err := restart(); err != nil {
		t.Fatalf("restart lab Caddy with short maintenance interval: %v", err)
	}
}
