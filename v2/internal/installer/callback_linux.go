package installer

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var errCallbackFailed = errors.New("domain HTTP callback failed")

func domainCallback(ctx context.Context, domain string) error {
	const timeout = 8 * time.Second
	ln, err := net.Listen("tcp", ":80")
	if err != nil {
		return fmt.Errorf("cannot listen on 80/tcp for the ACME preflight: %w", err)
	}
	defer ln.Close()

	path, secret := "/.well-known/nexus-check/"+rand.Text(), rand.Text()
	srv := &http.Server{
		ReadHeaderTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, MaxHeaderBytes: 4096,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || !strings.EqualFold(r.Host, domain) || r.URL.Path != path {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			io.WriteString(w, secret)
		}),
	}
	go srv.Serve(ln)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+domain+path, nil)
	if err != nil {
		return err
	}
	client := http.Client{
		Transport:     &http.Transport{DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 8192},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		// Keep the random request path out of diagnostics.
		var u *url.Error
		if errors.As(err, &u) {
			err = u.Err
		}
		return fmt.Errorf("%w for %s: %w", errCallbackFailed, domain, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(len(secret)+1)))
	if err != nil {
		return fmt.Errorf("%w for %s: %w", errCallbackFailed, domain, err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != secret {
		return fmt.Errorf("%w for %s: HTTP %d without the expected response", errCallbackFailed, domain, resp.StatusCode)
	}
	return nil
}

// Run only before a new ACME installation; a running deployment already owns port 80.
func (h host) checkCallback(ctx context.Context, o Options) error {
	check := h.dnsCheck
	if check == nil {
		check = domainCallback
	}
	err := check(ctx, o.Domain)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !errors.Is(err, errCallbackFailed) {
		return err
	}
	reason := fmt.Errorf("%w. Check DNS and inbound TCP/80; some NAT gateways do not support hairpin connections", err)
	if !o.SkipDNSCheck && o.ConfirmDNSFailure == nil {
		return fmt.Errorf("%w. To continue to ACME issuance, explicitly pass --skip-dns-check", reason)
	}
	fmt.Fprintln(h.out, reason)
	if o.ConfirmDNSFailure != nil {
		ok, readErr := o.ConfirmDNSFailure()
		if readErr != nil {
			return readErr
		}
		if !ok {
			return reason
		}
	}
	fmt.Fprintln(h.out, "Continuing to ACME issuance; installation still requires a valid certificate.")
	return nil
}
