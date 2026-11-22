// Laboratory adapter. Run only in an isolated netns with disposable lab_identity fixtures.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/shejiang000-prog/Nexus/v2/transport"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

func must[T any](v T, e error) T {
	if e != nil {
		panic(e)
	}
	return v
}
func main() {
	mode := os.Args[1]
	authority := "nexus.test:31443"
	endpoint := "192.0.2.2:31443"
	if v := os.Getenv("LAB_ENDPOINT"); v != "" {
		endpoint = v
	}
	if mode == "sample" { // Capture stops after ClientHello, before server authentication or signing.
		carrier, port, profile := os.Args[2], os.Args[3], os.Args[4]
		cfg := transport.Config{Authority: authority, Endpoint: net.JoinHostPort("127.0.0.1", port), Mode: carrier, Sign: func([]byte) ([]byte, error) { panic("unexpected signing") }}
		if profile != "-" {
			cfg.ClientHello = must(os.ReadFile(profile))
		}
		transport.Start(context.Background(), cfg)
		return
	}
	key := must(x509.ParsePKCS8PrivateKey(must(os.ReadFile("device.key.der")))).(*ecdsa.PrivateKey)
	if mode == "server" {
		type M = map[string]any
		home := must(os.Getwd())
		os.Mkdir("site", 0700)
		if e := os.WriteFile("site/health.bin", make([]byte, 32768), 0600); e != nil {
			panic(e)
		}
		handler := M{"handler": "nexus", "authority": authority, "device_keys": M{"00000000000000000000000000000000": hex.EncodeToString(elliptic.MarshalCompressed(key.Curve, key.X, key.Y))}, "allow_exact": []string{"127.0.0.1:32443", "127.0.0.1:32554"}}
		node := M{"listen": []string{endpoint}, "automatic_https": M{"disable": true}, "enable_datagrams": os.Getenv("LAB_CAPSULES") != "1", "tls_connection_policies": []M{{"protocol_min": "tls1.3"}}, "routes": []M{{"handle": []M{handler, {"handler": "file_server", "root": filepath.Join(home, "site")}}}}}
		config := M{"admin": M{"disabled": true}, "apps": M{"tls": M{"certificates": M{"load_files": []M{{"certificate": "server.pem", "key": "server.key"}}}}, "http": M{"servers": M{"lab": node}, "grace_period": "100ms"}}}
		if e := os.WriteFile("node.json", must(json.Marshal(config)), 0600); e != nil {
			panic(e)
		}
		bin := os.Getenv("LAB_NODE")
		if e := syscall.Exec(bin, []string{bin, "run", "--config", "node.json"}, append(os.Environ(), "XDG_DATA_HOME="+home, "XDG_CONFIG_HOME="+home)); e != nil {
			panic(e)
		}
	}
	carrier := "h3"
	if len(os.Args) > 2 {
		carrier = os.Args[2]
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(must(os.ReadFile("ca.pem"))) {
		panic("CA")
	}
	cfg := transport.Config{Authority: authority, Endpoint: endpoint, Mode: carrier, Roots: roots, OnState: func(s string) { fmt.Fprintln(os.Stderr, "selected", s) }, Sign: func(m []byte) ([]byte, error) {
		h := sha256.Sum256(m)
		r, s, e := ecdsa.Sign(rand.Reader, key, h[:])
		if e != nil {
			return nil, e
		}
		return append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...), nil
	}}
	if len(os.Args) > 3 && os.Args[3] != "-" {
		cfg.ClientHello = must(os.ReadFile(os.Args[3]))
	}
	start := func() *transport.Client { return must(transport.Start(context.Background(), cfg)) }
	if mode == "hello" {
		start().Stop()
		return
	}
	if mode == "udp" {
		cl := start()
		defer cl.Stop()
		a := must(cl.OpenUDP(context.Background(), "127.0.0.1:32554"))
		defer a.Close()
		var wg sync.WaitGroup
		wg.Add(1)
		sent := make([]time.Time, 1500)
		var mu sync.Mutex
		go func() {
			defer wg.Done()
			for {
				p, e := a.ReadPacket()
				if e != nil {
					return
				}
				if len(p) != 1024 {
					continue
				}
				i := int(binary.BigEndian.Uint32(p))
				if i >= len(sent) {
					continue
				}
				mu.Lock()
				stamp := sent[i]
				mu.Unlock()
				fmt.Printf("%d,%.3f\n", i, float64(time.Since(stamp).Microseconds())/1000)
			}
		}()
		begin := time.Now()
		for i := range sent {
			time.Sleep(time.Until(begin.Add(time.Duration(i) * 20 * time.Millisecond)))
			data := make([]byte, 1024)
			binary.BigEndian.PutUint32(data, uint32(i))
			mu.Lock()
			sent[i] = time.Now()
			mu.Unlock()
			if err := a.WritePacket(data); err != nil {
				panic(err)
			}
		}
		time.Sleep(3 * time.Second)
		a.Close()
		wg.Wait()
		return
	}
	if mode != "client" {
		panic("invalid mode")
	}
	ln := must(net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(32400))))
	cl := sync.OnceValue(start)
	for {
		sock := must(ln.Accept())
		go func() {
			defer sock.Close()
			buf := make([]byte, 16384)
			n, e := sock.Read(buf)
			if e != nil {
				return
			}
			stream, e := cl().DialTCP(context.Background(), "127.0.0.1:32443", buf[:n])
			if e != nil {
				return
			}
			defer stream.Close()
			go io.Copy(stream, sock)
			io.Copy(sock, stream)
		}()
	}
}
