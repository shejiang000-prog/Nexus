package installer

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestACMEProblem(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct{ msg, want string }{
		{`{"identifier":"node.test","msg":"challenge failed","problem":{"type":"urn:ietf:params:acme:error:dns","detail":"private"}}`, "dns"},
		{`{"identifier":"node.test","msg":"challenge failed","problem":{"type":"urn:ietf:params:acme:error:connection"}}`, "connection"},
		{`{"identifier":"node.test","msg":"could not get certificate from issuer","error":"HTTP 429 urn:ietf:params:acme:error:rateLimited - private"}`, "rateLimited"},
		{`{"identifier":"node.test","msg":"could not get certificate from issuer","error":"HTTP 403 urn:ietf:params:acme:error:unauthorized - private"}`, "unauthorized"},
		{`{"identifier":"other.test","msg":"could not get certificate from issuer","error":"HTTP 429 urn:ietf:params:acme:error:rateLimited - private"}`, ""},
		{`{"identifier":"node.test","msg":"certificate obtained successfully"}
{"identifier":"node.test","msg":"could not get certificate from issuer","error":"HTTP 429 urn:ietf:params:acme:error:rateLimited - old"}`, ""},
	} {
		h := host{command: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "systemctl" {
				return []byte(id), nil
			}
			flags := strings.Join(args, " ")
			if name != "journalctl" || !strings.Contains(flags, "-r") || !strings.Contains(flags, "_SYSTEMD_INVOCATION_ID="+id) || !strings.Contains(flags, "_SYSTEMD_UNIT=nexus-v2.service") {
				t.Fatal("unscoped journal query", args)
			}
			return []byte(tc.msg), nil
		}}
		if got := h.acmeProblem(t.Context(), "node.test"); got != tc.want {
			t.Fatalf("category %q, want %q", got, tc.want)
		}
	}
}

func TestACMEFailureRollsBack(t *testing.T) {
	l := newLab(t)
	l.dnsCheck = func(context.Context, string) error { return nil }
	addr, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer addr.Close()
	port := addr.Addr().(*net.TCPAddr).Port
	runner := l.command
	l.command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "systemctl" && args[0] == "show" && args[1] == "--property=InvocationID" {
			return []byte("0123456789abcdef0123456789abcdef"), nil
		}
		if name == "journalctl" {
			return []byte(`{"identifier":"refused.test","msg":"could not get certificate from issuer","error":"HTTP 403 urn:ietf:params:acme:error:rejectedIdentifier - private material"}`), nil
		}
		return runner(ctx, name, args...)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = l.run(ctx, "install", Options{Domain: "refused.test", NodePath: "/bin/true", Ports: map[string]int{"v2": port}}, sampleRender)
	if err == nil || !strings.Contains(err.Error(), "CA rejected issuance") || strings.Contains(err.Error(), "private material") {
		t.Fatal(err)
	}
	for _, p := range []string{base, cfg, state, unitDir + "/nexus-v2.service", unitDir + "/nexus-v2-firewall.service"} {
		if e := absent(l.path(p)); e != nil {
			t.Fatal(e)
		}
	}
	if l.user || l.nft != "" || len(l.active) > 0 {
		t.Fatal("installation resources survived failed issuance")
	}
}

func TestNoACMEProblem(t *testing.T) {
	// Unavailable journals and a failed TLS handshake cannot establish a cloud firewall cause.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	defer listener.Close()
	h := host{command: func(context.Context, string, ...string) ([]byte, error) { return nil, fmt.Errorf("no journal") }}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err = h.waitForCertificate(ctx, deployment{Domain: "node.test", Ports: map[string]int{"v2": port}, CertMode: "acme"})
	if err == nil || !strings.Contains(err.Error(), "no specific CA failure confirmed") {
		t.Fatal(err)
	}
}
