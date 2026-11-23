// Package compat applies per-user credentials to locally supervised upstream servers.
package compat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

type Credential struct {
	UserID     string `json:"user_id"`
	Generation int64  `json:"generation"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
	UUID       string `json:"uuid,omitempty"`
	Password   string `json:"password,omitempty"`
}
type RealityKey struct {
	Generation int64  `json:"generation"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	ShortID    string `json:"short_id"`
}
type Snapshot struct {
	Protocol      string       `json:"protocol"`
	Revision      int64        `json:"revision"`
	ResetRevision int64        `json:"reset_revision,omitempty"`
	Credentials   []Credential `json:"credentials"`
	RealityKey    *RealityKey  `json:"reality_key,omitempty"`
}
type deployment struct {
	Certificate string         `json:"certificate"`
	KeyFile     string         `json:"key_file"`
	Domain      string         `json:"domain"`
	Ports       map[string]int `json:"ports"`
	Target      string         `json:"reality_target"`
	ServerNames []string       `json:"reality_server_names"`
}

func enabled(p string) bool { return p == "reality" || p == "hysteria2" }
func stateDir(root, protocol string) string {
	return filepath.Join(root, "var/lib/nexus-v2", "compat-"+protocol)
}
func socket(root, protocol string) string {
	return filepath.Join(stateDir(root, protocol), "control.sock")
}

func Apply(ctx context.Context, root string, s Snapshot) error {
	if !enabled(s.Protocol) {
		return errors.New("unsupported protocol")
	}
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://localhost/apply", bytes.NewReader(body))
	if err != nil {
		return err
	}
	tr := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket(root, s.Protocol))
	}}
	resp, err := (&http.Client{Transport: tr, Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("%s control unavailable: %w", s.Protocol, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return fmt.Errorf("%s service rejected apply (%d)", s.Protocol, resp.StatusCode)
	}
	return nil
}
