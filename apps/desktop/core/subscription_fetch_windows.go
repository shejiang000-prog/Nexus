package main

import (
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xtls/libxray/subscriptions"
)

var (
	errSubFormat   = errors.New("unsupported subscription")
	errSubDownload = errors.New("subscription download failed")
	errSubChanged  = errors.New("subscription changed")
)

// Cleartext is limited to literal loopback addresses for local providers.
func validSourceURL(u *url.URL) bool {
	if u.User != nil || u.Opaque != "" || u.Hostname() == "" {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return u.Scheme == "https" || u.Scheme == "http" && err == nil && ip.Unmap().IsLoopback()
}

func fetchSubscription(u *url.URL) ([]byte, string, error) {
	if !validSourceURL(u) {
		return nil, "", errSubFormat
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 64 << 10
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		next.Header.Del("Referer") // A subscription URL can contain a bearer token.
		if len(via) > 3 || !validSourceURL(next.URL) || via[len(via)-1].URL.Scheme == "https" && next.URL.Scheme != "https" {
			return errSubDownload
		}
		return nil
	}}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", errSubFormat
	}
	req.Header.Set("User-Agent", "Nexus/0.1.0 (compatible; v2rayN)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", errSubDownload
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", errSubDownload
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, subscriptions.MaxBytes+1))
	if err != nil {
		return nil, "", errSubDownload
	}
	if len(body) > subscriptions.MaxBytes {
		return nil, "", errSubFormat
	}
	return body, resp.Header.Get("Subscription-Userinfo"), nil
}

type allowance struct {
	Remaining *int64 `json:"remaining,omitempty"`
	Expire    *int64 `json:"expire,omitempty"`
}

var (
	infoBalance = regexp.MustCompile(`^剩余流量\s*[:：]\s*([0-9]{1,12}(?:\.[0-9]{1,3})?)\s*(KB|MB|GB|TB|B)$`)
	infoExpiry  = regexp.MustCompile(`^套餐到期\s*[:：]\s*([0-9]{4}-[0-9]{2}-[0-9]{2})$`)
	infoReset   = regexp.MustCompile(`^距离下次重置剩余\s*[:：]\s*[0-9]{1,4}\s*天$`)
)

// Some feeds use synthetic node names for account information (Android Usage.kt).
func (a *allowance) infoName(name string) bool {
	if m := infoBalance.FindStringSubmatch(name); m != nil {
		size, _ := new(big.Rat).SetString(m[1])
		shift := map[string]uint{"B": 0, "KB": 10, "MB": 20, "GB": 30, "TB": 40}[m[2]]
		n := new(big.Int).Lsh(size.Num(), shift)
		if n.Quo(n, size.Denom()).IsInt64() {
			if a.Remaining == nil {
				v := n.Int64()
				a.Remaining = &v
			}
			return true
		}
	}
	if m := infoExpiry.FindStringSubmatch(name); m != nil {
		if date, err := time.ParseInLocation("2006-01-02", m[1], time.Local); err == nil {
			if a.Expire == nil {
				v := date.Unix()
				a.Expire = &v
			}
			return true
		}
	}
	return infoReset.MatchString(name)
}

func (a *allowance) applyHeader(header string) {
	if len(header) > 256 {
		return
	}
	fields := map[string]int64{}
	for _, part := range strings.Split(header, ";") {
		k, s, ok := strings.Cut(strings.TrimSpace(part), "=")
		if n, err := strconv.ParseUint(s, 10, 63); ok && err == nil && len(s) <= 19 {
			fields[k] = int64(n)
		}
	}
	if total := fields["total"]; total > 0 {
		a.Remaining = nil
		up, hasUp := fields["upload"]
		down, hasDown := fields["download"]
		if hasUp || hasDown {
			left := max(0, max(0, total-up)-down)
			a.Remaining = &left
		}
	}
	if expire := fields["expire"]; expire > 0 && expire <= 253402300799 {
		a.Expire = &expire
	}
}
