package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/server"
	"github.com/shejiang000-prog/Nexus/v2/transport"
)

type nodeLab struct {
	bin, dir, config, dbPath, authority, endpoint, tcp, udp string
	roots                                                   *x509.CertPool
	user                                                    proof.ID
	ledger                                                  *server.Ledger
	cmd                                                     *exec.Cmd
	log                                                     bytes.Buffer
}

func (l *nodeLab) start(t *testing.T) {
	t.Helper()
	l.log.Reset()
	l.cmd = exec.Command(l.bin, "run", "--config", l.config)
	l.cmd.Dir = l.dir
	l.cmd.Env = []string{"HOME=" + l.dir, "XDG_DATA_HOME=" + l.dir, "XDG_CONFIG_HOME=" + l.dir, "GORACE=atexit_sleep_ms=0"}
	l.cmd.Stderr = &l.log
	if err := l.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", l.endpoint, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	l.stop(t, os.Kill)
	t.Fatalf("node readiness timeout: %s", l.log.String())
}

func (l *nodeLab) stop(t *testing.T, signal os.Signal) {
	t.Helper()
	if l.cmd == nil {
		return
	}
	err := l.cmd.Process.Signal(signal)
	done := make(chan error, 1)
	go func() { done <- l.cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(8 * time.Second):
		l.cmd.Process.Kill()
		<-done
		t.Error("node shutdown timeout")
	}
	l.cmd = nil
	if err != nil || (signal != os.Kill && waitErr != nil) || bytes.Contains(l.log.Bytes(), []byte("WARNING: DATA RACE")) {
		t.Fatalf("node shutdown: signal=%v wait=%v\n%s", err, waitErr, l.log.String())
	}
}

func (l *nodeLab) enroll(t *testing.T, mode string) ([]byte, proof.ID, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token, err := l.ledger.Invite(t.Context(), l.user, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return token, redeemOverTLS(t, mode, l.endpoint, l.authority, l.roots, token, key), key
}

func (l *nodeLab) connect(t *testing.T, mode string, id proof.ID, key *ecdsa.PrivateKey) *transport.Client {
	t.Helper()
	c, err := transport.Start(t.Context(), transport.Config{Authority: l.authority, Endpoint: l.endpoint, Mode: mode, Device: id, Roots: l.roots, Sign: func(msg []byte) ([]byte, error) {
		hash := sha256.Sum256(msg)
		r, s, err := ecdsa.Sign(rand.Reader, key, hash[:])
		if err != nil {
			return nil, err
		}
		return append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Stop)
	return c
}

func (l *nodeLab) client(t *testing.T, mode string) (proof.ID, *transport.Client) {
	_, id, key := l.enroll(t, mode)
	return id, l.connect(t, mode, id, key)
}

func (l *nodeLab) admin(t *testing.T, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, l.bin, append([]string{"ledger", l.dbPath}, args...)...)
	cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("ledger %s: %s", args[0], exit.Stderr)
		}
		t.Fatal(err)
	}
	return out
}
