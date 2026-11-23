package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/server"
	"github.com/shejiang000-prog/Nexus/v2/transport"
)

func TestLedgerReload(t *testing.T) {
	testNode(t, true, false, func(t *testing.T, l *nodeLab) {
		ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
		defer cancel()
		config, err := os.ReadFile(l.config)
		if err != nil {
			t.Fatal(err)
		}
		// Enable Caddy's admin API only on this test's private Unix socket.
		addr := "unix/" + filepath.Join(l.dir, "admin.sock")
		listen, _ := json.Marshal(map[string]string{"listen": addr})
		base := bytes.Replace(config, []byte(`{"disabled":true}`), listen, 1)
		store := func(data []byte) {
			t.Helper()
			if err := os.WriteFile(l.config, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		l.stop(t, os.Interrupt)
		store(base)
		l.start(t)
		reload := func(path string, wantOK bool) {
			t.Helper()
			old, _ := json.Marshal(l.dbPath)
			replacement, _ := json.Marshal(path)
			store(bytes.ReplaceAll(base, old, replacement))
			cmd := exec.CommandContext(ctx, l.bin, "reload", "--config", l.config, "--address", addr)
			cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0")
			out, err := cmd.CombinedOutput()
			if (err == nil) != wantOK {
				t.Fatalf("reload success=%t want=%t: %s", err == nil, wantOK, out)
			}
		}
		type flow struct {
			mode string
			id   proof.ID
			key  *ecdsa.PrivateKey
			tcp  io.ReadWriteCloser
			udp  *transport.Association
		}
		var active []flow
		for _, mode := range []string{"h2", "h3"} {
			_, id, key := l.enroll(t, mode)
			c := l.connect(t, mode, id, key)
			tcp, err := c.DialTCP(ctx, l.tcp, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tcp.Close()
			u, err := c.OpenUDP(ctx, l.udp)
			if err != nil {
				t.Fatal(err)
			}
			defer u.Close()
			active = append(active, flow{mode, id, key, tcp, u})
		}
		echo := func(tcp io.ReadWriteCloser) {
			t.Helper()
			if _, err := tcp.Write([]byte("r")); err != nil {
				t.Fatal(err)
			}
			var b [1]byte
			if _, err := io.ReadFull(tcp, b[:]); err != nil || b[0] != 'r' {
				t.Fatalf("TCP echo: %v", err)
			}
		}
		verifyActive := func() {
			t.Helper()
			for _, f := range active {
				echo(f.tcp)
				if err := f.udp.WritePacket([]byte("u")); err != nil {
					t.Fatal(err)
				}
				if b, err := f.udp.ReadPacket(); err != nil || string(b) != "u" {
					t.Fatalf("%s UDP echo: %v", f.mode, err)
				}
			}
		}
		verifyNewDevice := func() {
			t.Helper()
			for _, mode := range []string{"h2", "h3"} {
				_, cl := l.client(t, mode)
				tcp, err := cl.DialTCP(ctx, l.tcp, nil)
				if err != nil {
					t.Fatal(err)
				}
				echo(tcp)
				tcp.Close()
			}
		}
		verifyActive()
		reload(filepath.Join(l.dbPath, "bad.db"), false)
		verifyActive()
		alias := filepath.Join(l.dir, "alias.db")
		if err := os.Symlink(l.dbPath, alias); err != nil {
			t.Fatal(err)
		}
		reload(alias, true)
		verifyActive()
		verifyNewDevice()
		otherPath := filepath.Join(l.dir, "second.db")
		other, err := server.OpenLedger(otherPath)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		if err := other.CreateUser(ctx, l.user); err != nil {
			t.Fatal(err)
		}
		reload(otherPath, true)
		ended := make(chan error, 2*len(active))
		for _, f := range active {
			go func() { var b [1]byte; _, err := f.tcp.Read(b[:]); ended <- err }()
			go func() { _, err := f.udp.ReadPacket(); ended <- err }()
		}
		timeout := time.NewTimer(time.Second)
		defer timeout.Stop()
		for range 2 * len(active) {
			select {
			case err := <-ended:
				if err == nil {
					t.Fatal("old ledger stream still readable")
				}
			case <-timeout.C:
				t.Fatal("old ledger TCP/UDP stream survived switch")
			}
		}
		for _, f := range active {
			cl := l.connect(t, f.mode, f.id, f.key)
			if s, err := cl.DialTCP(ctx, l.tcp, nil); err == nil {
				s.Close()
				t.Fatal("old identity authorized by new ledger")
			}
		}
		previous := l.ledger
		l.ledger = other
		defer func() { l.ledger = previous }()
		verifyNewDevice()
		l.stop(t, os.Interrupt)
		if bytes.Contains(l.log.Bytes(), []byte("connection is already closed")) {
			t.Fatal("duplicate ledger cleanup")
		}
	})
}
