package integration

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/mobile"
)

type deviceSigner struct{ key *ecdsa.PrivateKey }

func (s deviceSigner) Sign(msg []byte) ([]byte, error) {
	digest := sha256.Sum256(msg)
	return ecdsa.SignASN1(rand.Reader, s.key, digest[:])
}

func TestMobileTCPHalfClose(t *testing.T) {
	request := []byte("request completed by FIN")
	reply := bytes.Repeat([]byte("response after FIN"), 8192)
	serve := func(c net.Conn) {
		data, err := io.ReadAll(c)
		if err == nil && bytes.Equal(data, request) {
			c.Write(reply)
		}
	}
	testNodeTCP(t, true, false, serve, func(t *testing.T, l *nodeLab) {
		ca, err := os.ReadFile(filepath.Join(l.dir, "server.pem"))
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"h2", "h3"} {
			t.Run(mode, func(t *testing.T) {
				_, id, key := l.enroll(t, mode)
				client, err := mobile.Start(l.authority, l.endpoint, mode, ca, id[:], nil, deviceSigner{key}, nil, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Stop()
				deadline := time.AfterFunc(5*time.Second, client.Stop)
				defer deadline.Stop()
				stream, err := client.DialTCP(l.tcp, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				if n, err := stream.Write(request); err != nil || n != len(request) {
					t.Fatalf("request write %d: %v", n, err)
				}
				for range 2 {
					if err := stream.CloseWrite(); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := stream.Write([]byte("late")); err == nil {
					t.Fatal("write after FIN succeeded")
				}
				var got bytes.Buffer
				for {
					p, err := stream.Read(32768)
					got.Write(p)
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				if !bytes.Equal(got.Bytes(), reply) {
					t.Fatalf("received %d of %d bytes", got.Len(), len(reply))
				}
			})
		}
	})
}
