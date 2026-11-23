// End-to-end acceptance uses a freshly built nexus-node and disposable identities.
package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	quic "github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/server"
	"golang.org/x/net/http2"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/transport"
)

func TestNode(t *testing.T) {
	t.Run("static", func(t *testing.T) { testNode(t, false, false) })
	t.Run("ledger", func(t *testing.T) { testNode(t, true, false) })
}
func TestLedgerLoad(t *testing.T) { testNode(t, true, true) }
func testNode(t *testing.T, useLedger, load bool, scenarios ...func(*testing.T, *nodeLab)) {
	bin := os.Getenv("NEXUS_NODE_TEST_BINARY")
	if bin == "" {
		t.Skip("set NEXUS_NODE_TEST_BINARY to a built nexus-node")
	}
	dir := t.TempDir()
	site := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Nexus test only"}, DNSNames: []string{"nexus.test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	write := func(file string, data []byte) string {
		p := filepath.Join(dir, file)
		if e := os.WriteFile(p, data, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	crt := write("server.pem", ca)
	priv := write("server.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}))
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	endpoint := l.Addr().String()
	l.Close()
	tcp, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer tcp.Close()
	go func() {
		for {
			conn, e := tcp.Accept()
			if e != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); io.Copy(c, c) }(conn)
		}
	}()
	u, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer u.Close()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, a, e := u.ReadFrom(buf)
			if e != nil {
				return
			}
			u.WriteTo(buf[:n], a)
		}
	}()
	authority := "nexus.test:" + strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	device, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	type M = map[string]any
	handler := M{"handler": "nexus", "authority": authority, "device_keys": M{"00000000000000000000000000000000": hex.EncodeToString(elliptic.MarshalCompressed(device.Curve, device.X, device.Y))}, "allow_exact": []string{tcp.Addr().String(), u.LocalAddr().String()}}
	var ledger *server.Ledger
	user := proof.ID{1}
	if useLedger {
		dbPath := filepath.Join(dir, "ledger.db")
		ledger, e = server.OpenLedger(dbPath)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if ledger != nil {
				ledger.Close()
			}
		})
		if e = ledger.CreateUser(t.Context(), user); e != nil {
			t.Fatal(e)
		}
		delete(handler, "device_keys")
		handler["ledger_path"] = dbPath
	}
	cfg := M{"admin": M{"disabled": true}, "apps": M{"tls": M{"certificates": M{"load_files": []M{{"certificate": crt, "key": priv}}}}, "http": M{"servers": M{"node": M{"listen": []string{endpoint}, "automatic_https": M{"disable": true}, "enable_datagrams": true, "tls_connection_policies": []M{{"protocol_min": "tls1.3"}}, "routes": []M{{"handle": []M{handler, {"handler": "file_server", "root": site}}}}}}}}}
	data, e := json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	lab := &nodeLab{bin: bin, dir: dir, dbPath: filepath.Join(dir, "ledger.db"), config: write("node.json", data),
		authority: authority, endpoint: endpoint, tcp: tcp.Addr().String(), udp: u.LocalAddr().String(),
		roots: x509.NewCertPool(), user: user, ledger: ledger}
	lab.roots.AppendCertsFromPEM(ca)
	lab.start(t)
	defer lab.stop(t, os.Kill)
	roots := lab.roots
	if len(scenarios) != 0 {
		defer func() { ledger = lab.ledger }()
		for _, scenario := range scenarios {
			scenario(t, lab)
		}
		return
	}
	if load {
		runLoad(t, ledger, user, authority, endpoint, tcp.Addr().String(), roots)
		return
	}
	for _, mode := range []string{"h2", "h3"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
			defer cancel()
			var id proof.ID
			if useLedger {
				device, e = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if e != nil {
					t.Fatal(e)
				}
				token, e := ledger.Invite(ctx, user, time.Now().Add(time.Minute))
				if e != nil {
					t.Fatal(e)
				}
				id = redeemOverTLS(t, mode, endpoint, authority, roots, token, device)
			}
			var signs atomic.Int32
			c, e := transport.Start(ctx, transport.Config{Authority: authority, Endpoint: endpoint, Mode: mode, Device: id, Roots: roots, Sign: func(m []byte) ([]byte, error) {
				signs.Add(1)
				d := sha256.Sum256(m)
				r, s, e := ecdsa.Sign(rand.Reader, device, d[:])
				if e != nil {
					return nil, e
				}
				return append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...), nil
			}})
			if e != nil {
				t.Fatal(e)
			}
			defer c.Stop()
			stream, e := c.DialTCP(ctx, tcp.Addr().String(), []byte("initial"))
			if e != nil {
				t.Fatal(e)
			}
			defer stream.Close()
			buf := make([]byte, 7)
			if _, e = io.ReadFull(stream, buf); e != nil || string(buf) != "initial" {
				t.Fatalf("TCP echo %v", e)
			}
			a, e := c.OpenUDP(ctx, u.LocalAddr().String())
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			for _, size := range []int{4, 0, 65507} {
				p := bytes.Repeat([]byte{42}, size)
				if e = a.WritePacket(p); e != nil {
					t.Fatal(e)
				}
				data, e := a.ReadPacket()
				if e != nil || !bytes.Equal(data, p) {
					t.Fatalf("UDP %d: %v", size, e)
				}
			}
			if signs.Load() != 1 {
				t.Fatal("re-signed a reused connection")
			}
			if s, e := c.DialTCP(ctx, fmt.Sprintf("127.0.0.1:%d", 1), nil); e == nil {
				s.Close()
				t.Fatal("reserved destination accepted")
			}
			if useLedger {
				done := make(chan error, 2)
				go func() { _, e := stream.Read(make([]byte, 1)); done <- e }()
				go func() { _, e := a.ReadPacket(); done <- e }()
				started := time.Now()
				revoker := exec.CommandContext(ctx, bin, "ledger", filepath.Join(dir, "ledger.db"), "revoke", hex.EncodeToString(id[:]))
				revoker.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0")
				if output, e := revoker.CombinedOutput(); e != nil {
					t.Fatalf("revoke command: %v %s", e, output)
				}
				limit := time.After(time.Second - time.Since(started))
				for range 2 {
					select {
					case e := <-done:
						if e == nil {
							t.Fatal("revoked stream still readable")
						}
					case <-limit:
						t.Fatal("revoked stream survived one second")
					}
				}
				t.Logf("active TCP and UDP ended after %v", time.Since(started))
			}
		})
	}
}

func redeemOverTLS(t *testing.T, mode, endpoint, authority string, roots *x509.CertPool, token []byte, key *ecdsa.PrivateKey) proof.ID {
	t.Helper()
	return testRedemption(t, mode, endpoint, authority, roots, token, key, true)
}
func testRedemption(t *testing.T, mode, endpoint, authority string, roots *x509.CertPool, token []byte, key *ecdsa.PrivateKey, wantValid bool) proof.ID {
	t.Helper()
	config := &tls.Config{RootCAs: roots, ServerName: "nexus.test", NextProtos: []string{mode}, MinVersion: tls.VersionTLS13}
	var conn http.RoundTripper
	var exporter proof.Exporter
	if mode == "h3" {
		q, err := quic.DialAddr(t.Context(), endpoint, config, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer q.CloseWithError(0, "")
		conn = (&http3.Transport{}).NewClientConn(q)
		exporter = q.ExportKeyingMaterial
	} else {
		tcp, err := tls.Dial("tcp", endpoint, config)
		if err != nil {
			t.Fatal(err)
		}
		defer tcp.Close()
		state := tcp.ConnectionState()
		exporter = state.ExportKeyingMaterial
		conn, err = new(http2.Transport).NewClientConn(tcp)
		if err != nil {
			t.Fatal(err)
		}
	}
	secret, err := proof.Secret(authority, exporter)
	if err != nil {
		t.Fatal(err)
	}
	var request proof.ID
	rand.Read(request[:])
	pub := elliptic.MarshalCompressed(key.Curve, key.X, key.Y)
	msg, err := proof.RedeemMessage(secret, request, sha256.Sum256(token), pub)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(msg)
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	fields := map[string]string{"invite": enc(token), "request_id": enc(request[:]), "public_key": enc(pub), "proof": enc(append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...))}
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), "POST", "https://"+authority+"/.well-known/nexus/v2/redeem", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := conn.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if !wantValid {
		if res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("unexpected site status %d", res.StatusCode)
		}
		return proof.ID{}
	}
	var result struct {
		Version int    `json:"v"`
		ID      string `json:"device_id"`
	}
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&result) != nil || result.Version != 2 {
		t.Fatalf("redeem HTTP %d", res.StatusCode)
	}
	id, err := base64.RawURLEncoding.DecodeString(result.ID)
	if err != nil || len(id) != 16 {
		t.Fatal("invalid device ID")
	}
	return proof.ID(id)
}

// Time successful first-byte echoes, so a sample includes authorization and target dial.
func runLoad(t *testing.T, l *server.Ledger, user proof.ID, authority, endpoint, target string, roots *x509.CertPool) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	start := func() (proof.ID, *transport.Client) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		token, err := l.Invite(ctx, user, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		id := redeemOverTLS(t, "h2", endpoint, authority, roots, token, key)
		c, err := transport.Start(ctx, transport.Config{Authority: authority, Endpoint: endpoint, Mode: "h2", Device: id, Roots: roots, Sign: func(msg []byte) ([]byte, error) {
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
		return id, c
	}
	const n = 50
	var clients [n]*transport.Client
	for i := range clients {
		_, clients[i] = start()
		time.Sleep(70 * time.Millisecond) // devices share one loopback source
	}
	workers, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { stop(); wg.Wait() }()
	var samples [n][]time.Duration
	var failures atomic.Int64
	for i, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for workers.Err() == nil {
				began := time.Now()
				conn, err := c.DialTCP(ctx, target, []byte{1})
				if err == nil {
					var b [1]byte
					_, err = io.ReadFull(conn, b[:])
					conn.Close()
					if b[0] != 1 && err == nil {
						err = fmt.Errorf("bad echo")
					}
				}
				if err != nil {
					failures.Add(1)
					return
				}
				samples[i] = append(samples[i], time.Since(began))
			}
		}()
	}
	var maxRev time.Duration
	for range 8 {
		id, c := start()
		stream, err := c.DialTCP(ctx, target, []byte{1})
		if err != nil {
			t.Fatal(err)
		}
		var buf [1]byte
		if _, err := io.ReadFull(stream, buf[:]); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { _, err := stream.Read(buf[:]); done <- err }()
		began := time.Now()
		if err := l.Revoke(ctx, id); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("unexpected data")
			}
		case <-time.After(time.Second):
			t.Fatal("revocation timeout")
		}
		maxRev = max(maxRev, time.Since(began))
		stream.Close()
		time.Sleep(100 * time.Millisecond)
	}
	t.Run("random_invites", func(t *testing.T) {
		for batch := range 8 {
			t.Run(strconv.Itoa(batch), func(t *testing.T) {
				t.Parallel()
				key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				for range 16 {
					token := make([]byte, 32)
					rand.Read(token)
					testRedemption(t, "h2", endpoint, authority, roots, token, key, false)
				}
			})
		}
	})
	stop()
	wg.Wait()
	var all []time.Duration
	for _, group := range samples {
		if len(group) == 0 {
			t.Fatal("idle device")
		}
		all = append(all, group...)
	}
	slices.Sort(all)
	t.Logf("H2 devices=50 samples=%d p50=%v p99=%v failed_opens=%d max_revoke=%v", len(all), all[(len(all)-1)/2], all[(99*len(all)+99)/100-1], failures.Load(), maxRev)
	if failures.Load() != 0 || maxRev >= time.Second {
		t.Fail()
	}
}
