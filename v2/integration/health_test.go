package integration

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/transport"
)

func TestAuthenticatedHealthAndRenewal(t *testing.T) {
	testNode(t, true, false, func(t *testing.T, l *nodeLab) {
		db, err := sql.Open("sqlite", l.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, mode := range []string{"h2", "h3"} {
			t.Run(mode, func(t *testing.T) {
				setExpiry := func(n any) {
					t.Helper()
					if _, err := db.Exec("UPDATE users SET expires_at=? WHERE id=?", n, l.user[:]); err != nil {
						t.Fatal(err)
					}
				}
				setExpiry(nil)
				_, id, key := l.enroll(t, mode)
				c := l.connect(t, mode, id, key)
				if c.AccountExpiry() != -1 {
					t.Fatal("deadline known before authentication")
				}
				conn, exporter := testCarrier(t, mode, l.endpoint, l.roots)
				secret, err := proof.Secret(l.authority, exporter)
				if err != nil {
					t.Fatal(err)
				}
				auth, err := proof.Create(secret, id, func(message []byte) ([]byte, error) {
					d := sha256.Sum256(message)
					r, s, err := ecdsa.Sign(rand.Reader, key, d[:])
					if err != nil {
						return nil, err
					}
					return append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...), nil
				})
				if err != nil {
					t.Fatal(err)
				}
				type reply struct {
					code    int
					headers http.Header
					length  int64
					body    []byte
				}
				request := func(method, path, header string) reply {
					t.Helper()
					req, err := http.NewRequestWithContext(t.Context(), method, "https://"+l.authority+path, nil)
					if err != nil {
						t.Fatal(err)
					}
					if header != "" {
						req.Header.Set("Proxy-Authorization", header)
					}
					resp, err := conn.RoundTrip(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					body, err := io.ReadAll(resp.Body)
					if err != nil {
						t.Fatal(err)
					}
					if resp.Header.Get("Date") != "" {
						resp.Header.Set("Date", "present")
					}
					return reply{resp.StatusCode, resp.Header, resp.ContentLength, body}
				}
				for _, method := range []string{"GET", "HEAD"} {
					site := request(method, "/does-not-exist", "")
					if site.code != 404 || site.headers.Get("Proxy-Authentication-Info") != "" || site.headers.Get("Nexus-Account-Expires") != "" || site.headers.Get("Nexus-QUIC-Lost") != "" || site.headers.Get("Nexus-QUIC-Sent") != "" {
						t.Fatalf("%s fallback %v", method, site)
					}
					for _, invalid := range []string{"", "invalid"} {
						if got := request(method, "/health.bin", invalid); !reflect.DeepEqual(got, site) {
							t.Fatalf("%s fallback differs: %+v %+v", method, site, got)
						}
					}
					ok := request(method, "/health.bin", auth)
					if ok.code != 200 || ok.headers.Get("Nexus-Account-Expires") != "0" || ok.length != 32768 || (method == "GET" && !bytes.Equal(ok.body, make([]byte, 32768))) || method == "HEAD" && len(ok.body) != 0 {
						t.Fatalf("invalid authenticated %s: %d len=%d", method, ok.code, ok.length)
					}
					if mode == "h3" {
						sent, e := strconv.ParseUint(ok.headers.Get("Nexus-QUIC-Sent"), 10, 64)
						lost, err := strconv.ParseUint(ok.headers.Get("Nexus-QUIC-Lost"), 10, 64)
						if e != nil || err != nil || sent == 0 || lost > sent {
							t.Fatal("invalid sender statistics")
						}
					} else if ok.headers.Get("Nexus-QUIC-Lost") != "" || ok.headers.Get("Nexus-QUIC-Sent") != "" {
						t.Fatal("sender statistics outside authenticated H3 health response")
					}
					if got := request(method, "/health.bin", ""); !reflect.DeepEqual(site, got) {
						t.Fatalf("%s no proof on previously bound connection", method)
					}
				}
				if err := c.Confirm(); err != nil || c.AccountExpiry() != 0 {
					t.Fatalf("permanent account: %v %d", err, c.AccountExpiry())
				}
				setExpiry(1)
				waitFor := func(name string, check func() bool) {
					t.Helper()
					until := time.Now().Add(3 * time.Second)
					for time.Now().Before(until) {
						if check() {
							return
						}
						time.Sleep(30 * time.Millisecond)
					}
					t.Fatal(name)
				}
				waitFor("node did not deny expired account", func() bool {
					var e *transport.Error
					return errors.As(c.Confirm(), &e) && e.Code == transport.Authentication
				})
				until := time.Now().Add(time.Hour).Unix()
				setExpiry(until)
				waitFor("business connection did not receive renewed expiry", func() bool {
					stream, err := c.DialTCP(t.Context(), l.tcp, nil)
					if err != nil {
						return false
					}
					stream.Close()
					return c.AccountExpiry() == until
				})
				if got := request("HEAD", "/health.bin", auth).headers.Get("Nexus-Account-Expires"); got != strconv.FormatInt(until, 10) {
					t.Fatalf("renewal metadata %q", got)
				}
			})
		}
	})
}
