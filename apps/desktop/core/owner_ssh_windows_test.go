package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Only the supplied command handler runs. No shell or external listener is used.
func testOwnerSSH(t *testing.T, exec func(cmd string, stdin []byte) (stdout, stderr string, status uint32)) (endpoint, pin string) {
	t.Helper()
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	host, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if c.User() == "root" && string(password) == "test-owner-password" {
				return nil, nil
			}
			return nil, fmt.Errorf("rejected")
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == "root" && key.Type() == ssh.KeyAlgoED25519 {
				return nil, nil
			}
			return nil, fmt.Errorf("rejected")
		},
	}
	config.AddHostKey(host)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer raw.Close()
				conn, chans, reqs, err := ssh.NewServerConn(raw, config)
				if err != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					if ch.ChannelType() != "session" {
						ch.Reject(ssh.UnknownChannelType, "")
						continue
					}
					stream, requests, err := ch.Accept()
					if err != nil {
						return
					}
					go func() {
						defer stream.Close()
						for req := range requests {
							var payload struct{ Command string }
							if req.Type != "exec" || ssh.Unmarshal(req.Payload, &payload) != nil {
								req.Reply(false, nil)
								continue
							}
							req.Reply(true, nil)
							input, err := io.ReadAll(io.LimitReader(stream, 128*1024))
							if err != nil {
								return
							}
							out, diag, status := exec(payload.Command, input)
							io.WriteString(stream, out)
							io.WriteString(stream.Stderr(), diag)
							stream.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
							return
						}
					}()
				}
			}()
		}
	}()
	return ln.Addr().String(), ssh.FingerprintSHA256(host.PublicKey())
}

func TestOwnerSSH(t *testing.T) {
	ctx := context.Background()
	const staged = "/tmp/nexus-upload.AABB112233/nexus-node"
	var ran, cleaned atomic.Bool
	var wrongDigest atomic.Bool
	endpoint, pin := testOwnerSSH(t, func(cmd string, input []byte) (string, string, uint32) {
		switch {
		case cmd == ownerInstalled:
			return "", "", 0
		case strings.HasPrefix(cmd, "umask 077 && mkdir -p ~/.ssh") && strings.Contains(cmd, "restrict ssh-ed25519 "):
			return "", "", 0
		case cmd == "uname -m":
			return "x86_64", "", 0
		case cmd == "umask 077 && mktemp -d /tmp/nexus-upload.XXXXXXXXXX":
			return "/tmp/nexus-upload.AABB112233", "", 0
		case cmd == "timeout 300 sh -s -- 'https://github.com/shejiang000-prog/Nexus/releases/download/v"+ownerNodeRelease.Version+"/nexus-node-linux-amd64' '"+staged+"'" && string(input) == ownerDownloadScript:
			return "", "", 0
		case cmd == "sha256sum '"+staged+"'":
			if wrongDigest.Load() {
				return strings.Repeat("b", 64) + "  " + staged, "", 0
			}
			return ownerNodeRelease.AMD64 + "  " + staged, "", 0
		case cmd == "chmod 0700 "+staged+" && "+staged+" install --non-interactive --public-host 127.0.0.1 --protocols v2,reality":
			ran.Store(true)
			return "", "port occupied: 443/tcp\n", 1
		case cmd == "rm -f -- "+staged+" && rmdir -- /tmp/nexus-upload.AABB112233":
			cleaned.Store(true)
			return "", "", 0
		}
		return "", "unexpected command", 2
	})
	for _, pair := range [][2]string{{endpoint, endpoint}, {"127.0.0.1", "127.0.0.1:22"}, {"::1", "[::1]:22"}, {"[::1]:2200", "[::1]:2200"}} {
		if got, err := parseOwnerEndpoint(pair[0]); err != nil || got != pair[1] {
			t.Fatal("endpoint normalization failed")
		}
	}
	if _, err := parseOwnerEndpoint("fe80::1%eth0"); err != ownerError("invalid_endpoint") {
		t.Fatal("accepted a scoped endpoint")
	}
	if got, err := ownerDiscover(ctx, endpoint); err != nil || got != pin {
		t.Fatalf("discovery error: %v", err)
	}
	if _, err := ownerConnect(ctx, endpoint, pin, ssh.Password("synthetic-wrong")); err != ownerError("ssh_auth") {
		t.Fatalf("password rejection: %v", err)
	}
	var asked atomic.Bool
	auth := ssh.PasswordCallback(func() (string, error) { asked.Store(true); return "test-owner-password", nil })
	if _, err := ownerConnect(ctx, endpoint, "SHA256:"+strings.Repeat("x", 43), auth); err != ownerError("host_key_changed") || asked.Load() {
		t.Fatalf("changed pin: %v", err)
	}
	client, err := ownerConnect(ctx, endpoint, pin, auth)
	if err != nil || !asked.Load() {
		t.Fatalf("login: %v", err)
	}
	defer client.Close()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := ownerAuthorize(ctx, client, key, true); err != nil {
		t.Fatal(err)
	}
	if err := ownerInstall(ctx, client, endpoint); err != ownerError("install_port_occupied") || !ran.Load() || !cleaned.Load() {
		t.Fatalf("preflight: %v", err)
	}
	wrongDigest.Store(true)
	ran.Store(false)
	if err := ownerInstall(ctx, client, endpoint); err != ownerError("node_checksum") || ran.Load() {
		t.Fatalf("digest: %v", err)
	}

	silent, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := silent.Addr().String()
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err = ownerDiscover(short, addr)
	silent.Close()
	if err != ownerError("ssh_timeout") {
		t.Fatalf("timeout: %v", err)
	}
	if _, err := ownerDiscover(ctx, addr); err != ownerError("ssh_refused") {
		t.Fatalf("refused: %v", err)
	}
}
