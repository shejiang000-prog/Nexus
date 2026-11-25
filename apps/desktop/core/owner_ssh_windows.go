package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/windows"
)

func parseOwnerEndpoint(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	host, port := s, "22"
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		host = s[1 : len(s)-1]
	} else if _, err := netip.ParseAddr(s); err != nil {
		host, port, err = net.SplitHostPort(s)
		if err != nil {
			return "", ownerError("invalid_endpoint")
		}
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || addr.Zone() != "" || strings.HasPrefix(s, "[") && !addr.Is6() ||
		len(port) < 1 || len(port) > 5 || strings.Trim(port, "0123456789") != "" {
		return "", ownerError("invalid_endpoint")
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return "", ownerError("invalid_endpoint")
	}
	return netip.AddrPortFrom(addr, uint16(n)).String(), nil
}

func ownerSSHError(ctx context.Context, err error) error {
	var timeout net.Error
	switch {
	case ctx.Err() == context.Canceled:
		return ownerError("cancelled")
	case ctx.Err() == context.DeadlineExceeded || errors.As(err, &timeout) && timeout.Timeout():
		return ownerError("ssh_timeout")
	case errors.Is(err, windows.WSAECONNREFUSED):
		return ownerError("ssh_refused")
	case errors.Is(err, ownerError("host_key_changed")):
		return ownerError("host_key_changed")
	case strings.Contains(err.Error(), "ssh: unable to authenticate, attempted methods"):
		// x/crypto/ssh exposes no typed client authentication rejection.
		return ownerError("ssh_auth")
	default:
		return ownerError("ssh_failed")
	}
}

func ownerHandshake(parent context.Context, endpoint string, config *ssh.ClientConfig) (*ssh.Client, error) {
	addr, err := parseOwnerEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	conn, err := new(net.Dialer).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, ownerSSHError(ctx, err)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if !stop() || err != nil {
		conn.Close()
		if err == nil {
			err = ctx.Err()
		}
		return nil, ownerSSHError(ctx, err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func ownerDiscover(ctx context.Context, endpoint string) (string, error) {
	var pin string
	_, err := ownerHandshake(ctx, endpoint, &ssh.ClientConfig{User: "root", HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		pin = ssh.FingerprintSHA256(key)
		return ownerError("host_key_changed") // stop before user authentication
	}})
	if pin != "" && err == ownerError("host_key_changed") {
		return pin, nil
	}
	return "", err
}

func ownerConnect(ctx context.Context, endpoint, pin string, auth ssh.AuthMethod) (*ssh.Client, error) {
	return ownerHandshake(ctx, endpoint, &ssh.ClientConfig{User: "root", Auth: []ssh.AuthMethod{auth}, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if ssh.FingerprintSHA256(key) != pin {
			return ownerError("host_key_changed")
		}
		return nil
	}})
}

// Drain both SSH streams without retaining unbounded remote output.
type ownerOutput struct {
	buf      bytes.Buffer
	overflow bool
}

func (o *ownerOutput) Write(p []byte) (int, error) {
	keep := min(len(p), 128*1024-o.buf.Len())
	o.buf.Write(p[:keep])
	o.overflow = o.overflow || keep < len(p)
	return len(p), nil
}

var occupiedMarker = regexp.MustCompile("(?m)^port occupied: ([1-9][0-9]{0,4})/(?:tcp|udp)\\r?$")

func ownerRun(parent context.Context, client *ssh.Client, cmd string, stdin []byte, duration time.Duration, failure ownerError) (string, error) {
	ctx, cancel := context.WithTimeout(parent, duration)
	defer cancel()
	var active atomic.Pointer[ssh.Session]
	stop := context.AfterFunc(ctx, func() {
		if session := active.Load(); session != nil {
			// Ask sshd to stop the remote command before closing its channel.
			// A dead connection must not block local cancellation.
			fallback := time.AfterFunc(200*time.Millisecond, func() { client.Close() })
			_ = session.Signal(ssh.SIGTERM)
			fallback.Stop()
		}
		client.Close()
	})
	defer stop()
	s, err := client.NewSession()
	if err != nil {
		return "", ownerSSHError(ctx, err)
	}
	defer s.Close()
	active.Store(s)
	var out, diagnostics ownerOutput
	s.Stdout, s.Stderr, s.Stdin = &out, io.Discard, bytes.NewReader(stdin)
	if failure == "install_failed" {
		s.Stderr = &diagnostics
	}
	if err = s.Run(cmd); err != nil {
		var exit *ssh.ExitError
		if ctx.Err() != nil || !errors.As(err, &exit) {
			return "", ownerSSHError(ctx, err)
		}
		if !diagnostics.overflow {
			if marker := occupiedMarker.FindSubmatch(diagnostics.buf.Bytes()); marker != nil {
				if _, e := strconv.ParseUint(string(marker[1]), 10, 16); e == nil {
					return "", ownerError("install_port_occupied")
				}
			}
		}
		return "", failure
	}
	if out.overflow {
		return "", failure
	}
	return strings.TrimSpace(out.buf.String()), nil
}

func ownerExec(ctx context.Context, client *ssh.Client, cmd string, stdin []byte) (string, error) {
	return ownerRun(ctx, client, cmd, stdin, 30*time.Second, ownerError("ssh_failed"))
}

const ownerInstalled = "if test -x /opt/nexus-v2/bin/nexus-node; then echo yes; fi"

func ownerAuthorize(ctx context.Context, client *ssh.Client, public ssh.PublicKey, deploy bool) error {
	status, err := ownerExec(ctx, client, ownerInstalled, nil)
	if err != nil {
		return err
	}
	switch {
	case status == "yes" && deploy:
		return ownerError("already_installed")
	case status == "" && !deploy:
		return ownerError("not_installed")
	case status != "yes" && status != "":
		return ownerError("ssh_failed")
	}
	if public.Type() != ssh.KeyAlgoED25519 {
		return ownerError("ssh_failed")
	}
	key := "restrict " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public))) + " Nexus"
	_, err = ownerExec(ctx, client, "umask 077 && mkdir -p ~/.ssh && touch ~/.ssh/authorized_keys && "+
		"(grep -qxF '"+key+"' ~/.ssh/authorized_keys || printf '\\n%s\\n' '"+key+"' >> ~/.ssh/authorized_keys)", nil)
	return err
}

var ownerTempPath = regexp.MustCompile("^/tmp/nexus-upload\\.[A-Za-z0-9]{10}$")
var ownerVersion = regexp.MustCompile("^[0-9]+\\.[0-9]+\\.[0-9]+$")
var ownerDigest = regexp.MustCompile("^[a-f0-9]{64}$")

func ownerInstall(ctx context.Context, client *ssh.Client, endpoint string) error {
	canonical, err := parseOwnerEndpoint(endpoint)
	if err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(canonical)
	machine, err := ownerExec(ctx, client, "uname -m", nil)
	if err != nil {
		return err
	}
	var arch, digest string
	switch machine {
	case "x86_64":
		arch, digest = "amd64", ownerNodeRelease.AMD64
	case "aarch64":
		arch, digest = "arm64", ownerNodeRelease.ARM64
	default:
		return ownerError("unsupported_arch")
	}
	if !ownerVersion.MatchString(ownerNodeRelease.Version) || !ownerDigest.MatchString(digest) {
		return ownerError("node_checksum")
	}
	dir, err := ownerExec(ctx, client, "umask 077 && mktemp -d /tmp/nexus-upload.XXXXXXXXXX", nil)
	if err != nil {
		return err
	}
	if !ownerTempPath.MatchString(dir) {
		return ownerError("install_failed")
	}
	file := dir + "/nexus-node"
	defer ownerExec(ctx, client, "rm -f -- "+file+" && rmdir -- "+dir, nil)
	url := "https://github.com/shejiang000-prog/Nexus/releases/download/v" + ownerNodeRelease.Version + "/nexus-node-linux-" + arch
	if _, err = ownerRun(ctx, client, "timeout 300 sh -s -- '"+url+"' '"+file+"'", []byte(ownerDownloadScript), 305*time.Second, ownerError("node_download")); err != nil {
		return err
	}
	hash, err := ownerExec(ctx, client, "sha256sum '"+file+"'", nil)
	if err != nil {
		return err
	}
	sum, _, ok := strings.Cut(hash, " ")
	if !ok || sum != digest {
		return ownerError("node_checksum")
	}
	_, err = ownerRun(ctx, client, "chmod 0700 "+file+" && "+file+" install --non-interactive --public-host "+host+" --protocols v2,reality", nil, 10*time.Minute, ownerError("install_failed"))
	return err
}
