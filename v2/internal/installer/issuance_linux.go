package installer

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Caddy acquires its first certificate asynchronously after systemd starts it.
// A trusted handshake proves that the requested name is actually being served.
func (h host) waitForCertificate(ctx context.Context, d deployment) error {
	var nextJournal time.Time
	var reason string
	for {
		err := h.siteTLS(ctx, d)
		if err == nil {
			return nil
		}
		var fileErr *os.PathError
		var certErr *tls.CertificateVerificationError
		if errors.As(err, &fileErr) || errors.Is(err, errTLSBundle) {
			return errors.New("cannot load the installed ACME trust bundle")
		}
		if errors.As(err, &certErr) {
			return errors.New("Caddy presented a certificate that failed trust, name or validity verification")
		}
		if ctx.Err() == nil && time.Now().After(nextJournal) {
			kind := h.acmeProblem(ctx, d.Domain)
			if kind != "" {
				reason = problemReason(kind)
			}
			if kind == "rateLimited" || kind == "rejectedIdentifier" || kind == "unsupportedIdentifier" {
				return fmt.Errorf("ACME certificate unavailable: %s", reason)
			}
			nextJournal = time.Now().Add(4 * time.Second)
		}
		select {
		case <-ctx.Done():
			if reason == "" {
				reason = "no specific CA failure confirmed; check Caddy's service journal, public DNS and inbound TCP 80/443 (NAT and cloud firewall)"
			}
			return fmt.Errorf("waiting for Caddy's ACME certificate: %w; %s", ctx.Err(), reason)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (h host) acmeProblem(ctx context.Context, domain string) string {
	unit := unitName("v2")
	id, err := h.command(ctx, "systemctl", "show", "--property=InvocationID", "--value", unit)
	invocation := strings.TrimSpace(string(id))
	if err != nil || len(invocation) != 32 {
		return ""
	}
	if _, err := hex.DecodeString(invocation); err != nil {
		return ""
	}
	// Read only this Caddy invocation's issuance messages. ACME detail text
	// may contain challenge material; return only recognized problem types.
	b, err := h.command(ctx, "journalctl", "--quiet", "--no-pager", "-o", "cat", "-r", "-n", "32",
		"--grep=challenge failed|could not get certificate from issuer|certificate (obtained|renewed) successfully",
		"_SYSTEMD_UNIT="+unit, "_SYSTEMD_INVOCATION_ID="+invocation)
	if err != nil {
		return ""
	}
	for line := range bytes.Lines(b) {
		var record struct {
			Identifier, Msg, Error string
			Problem                struct{ Type string }
		}
		if json.Unmarshal(line, &record) != nil || record.Identifier != domain {
			continue
		}
		switch record.Msg {
		case "certificate obtained successfully", "certificate renewed successfully":
			return ""
		case "challenge failed", "could not get certificate from issuer":
			id := record.Problem.Type
			if fields := strings.Fields(record.Error); id == "" && len(fields) >= 3 && fields[0] == "HTTP" {
				// acmez renders an issuer's Problem as "HTTP <status> <URN> - <detail>".
				id = fields[2]
			}
			if token, ok := strings.CutPrefix(id, "urn:ietf:params:acme:error:"); ok && problemReason(token) != "" {
				return token
			}
		}
	}
	return ""
}

func problemReason(kind string) string {
	switch kind {
	case "dns":
		return "the CA reported a DNS lookup problem; check public A/AAAA records"
	case "connection":
		return "the CA could not connect during validation; check A/AAAA, inbound TCP 80/443, NAT forwarding and cloud firewall rules"
	case "unauthorized", "incorrectResponse", "tls":
		return "the CA could not validate control of the name; check the destination of A/AAAA and the service answering the challenge"
	case "rejectedIdentifier", "unsupportedIdentifier", "caa":
		return "the CA rejected issuance for this name; check its identifier and DNS CAA policies"
	case "rateLimited":
		return "the CA rate-limited issuance; retry after the CA's indicated interval"
	case "serverInternal":
		return "the CA reported an internal error"
	default:
		return ""
	}
}
