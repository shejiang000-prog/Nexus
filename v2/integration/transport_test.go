// End-to-end acceptance uses a freshly built nexus-node and disposable identities.
package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/transport"
)

func TestNode(t *testing.T) {
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
	cfg := M{"admin": M{"disabled": true}, "apps": M{"tls": M{"certificates": M{"load_files": []M{{"certificate": crt, "key": priv}}}}, "http": M{"servers": M{"node": M{"listen": []string{endpoint}, "automatic_https": M{"disable": true}, "enable_datagrams": true, "tls_connection_policies": []M{{"protocol_min": "tls1.3"}}, "routes": []M{{"handle": []M{handler, {"handler": "file_server", "root": site}}}}}}}}}
	data, e := json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(bin, "run", "--config", write("node.json", data))
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "XDG_DATA_HOME=" + dir, "XDG_CONFIG_HOME=" + dir}
	var log bytes.Buffer
	cmd.Stderr = &log
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			t.Log(log.String())
		}
	}()
	ready := time.Now().Add(8 * time.Second)
	for {
		c, e := net.DialTimeout("tcp", endpoint, 100*time.Millisecond)
		if e == nil {
			c.Close()
			break
		}
		if time.Now().After(ready) {
			t.Fatal("node readiness timeout")
		}
		time.Sleep(30 * time.Millisecond)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	for _, mode := range []string{"h2", "h3"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
			defer cancel()
			var signs atomic.Int32
			c, e := transport.Start(ctx, transport.Config{Authority: authority, Endpoint: endpoint, Mode: mode, Roots: roots, Sign: func(m []byte) ([]byte, error) {
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
		})
	}
}
