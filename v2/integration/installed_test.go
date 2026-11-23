//go:build linux

package integration

import (
	"bytes"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/server"
)

// This opt-in test exercises the actual systemd service in a disposable NexusLab
// installation. Only the two temporary echo endpoints are allowed on loopback.
func TestInstalledPrivateNode(t *testing.T) {
	if os.Getenv("NEXUS_TEST_INSTALLED") != "1" {
		t.Skip("requires disposable NexusLab systemd installation")
	}
	const config = "/etc/nexus-v2/v2.json"
	const bin = "/opt/nexus-v2/bin/nexus-node"
	const db = "/var/lib/nexus-v2/ledger.db"
	const socket = "unix//var/lib/nexus-v2/admin.sock"
	dir := t.TempDir()
	b, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	srv := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)["node"].(map[string]any)
	handler := srv["routes"].([]any)[0].(map[string]any)["handle"].([]any)[0].(map[string]any)
	authority := handler["authority"].(string)
	host, port, err := net.SplitHostPort(authority)
	if err != nil || host != "nexus.test" || handler["ledger_path"] != db {
		t.Fatal("requires installer-generated nexus.test lab configuration")
	}
	reload := func(file string) {
		t.Helper()
		if result, e := exec.CommandContext(t.Context(), bin, "reload", "--config", file, "--address", socket).CombinedOutput(); e != nil {
			t.Fatalf("reload: %s %v", result, e)
		}
	}
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	u, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	go func() {
		b := make([]byte, 65535)
		for {
			n, addr, e := u.ReadFrom(b)
			if e != nil {
				return
			}
			u.WriteTo(b[:n], addr)
		}
	}()
	handler["allow_exact"] = []string{echo.Addr().String(), u.LocalAddr().String()}
	local := filepath.Join(dir, "with-echo.json")
	b, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(local, b, 0600); err != nil {
		t.Fatal(err)
	}
	reload(local)
	defer reload(config)
	ledger, err := server.OpenLedger(db)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	var deployed struct {
		Certificate string `json:"certificate"`
	}
	data, err := os.ReadFile("/etc/nexus-v2/deployment.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &deployed); err != nil {
		t.Fatal(err)
	}
	cert, err := os.ReadFile(deployed.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	l := &nodeLab{bin: bin, dbPath: db, authority: authority, endpoint: net.JoinHostPort("127.0.0.1", port), roots: x509.NewCertPool(), ledger: ledger}
	if !l.roots.AppendCertsFromPEM(cert) {
		t.Fatal("empty test trust store")
	}
	rand.Read(l.user[:])
	if err = ledger.CreateUser(t.Context(), l.user); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"h2", "h3"} {
		t.Run(mode, func(t *testing.T) {
			_, id, key := l.enroll(t, mode)
			client := l.connect(t, mode, id, key)
			s, err := client.DialTCP(t.Context(), echo.Addr().String(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			udp, err := client.OpenUDP(t.Context(), u.LocalAddr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer udp.Close()
			verify := func() {
				t.Helper()
				var b [1]byte
				if _, err := s.Write([]byte{'t'}); err != nil {
					t.Fatal(err)
				}
				if _, err := io.ReadFull(s, b[:]); err != nil || b[0] != 't' {
					t.Fatalf("tcp echo %v", err)
				}
				if err := udp.WritePacket([]byte{'u'}); err != nil {
					t.Fatal(err)
				}
				if b, err := udp.ReadPacket(); err != nil || !bytes.Equal(b, []byte{'u'}) {
					t.Fatalf("udp echo %v", err)
				}
			}
			verify()
			// Force a real configuration change with both association types active.
			srv["routes"].([]any)[1].(map[string]any)["handle"].([]any)[0].(map[string]any)["body"] = "Lab " + mode + "\n"
			b, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(local, b, 0600); err != nil {
				t.Fatal(err)
			}
			reload(local)
			verify()
			path := filepath.Join(dir, "backup-"+mode+".db")
			l.admin(t, "backup", path)
			copy, err := server.OpenLedger(path)
			if err != nil {
				t.Fatal(err)
			}
			if copy.Lookup(id) == nil {
				copy.Close()
				t.Fatal("backup lost active device")
			}
			copy.Close()
			l.admin(t, "revoke", fmt.Sprintf("%x", id[:]))
			done := make(chan error, 1)
			go func() { var b [1]byte; _, err := s.Read(b[:]); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("revoked stream remained open")
				}
			case <-time.After(time.Second):
				t.Fatal("revoked stream did not close")
			}
		})
	}
}
