package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shejiang000-prog/Nexus/v2/proof"
)

type shareInvite struct {
	Version int    `json:"v"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Invite  string `json:"invite"`
	Pin     string `json:"pin,omitempty"`
}

func installedInvite(root, db string) (*shareInvite, error) {
	if !filepath.IsAbs(db) || !filepath.IsAbs(root) {
		return nil, errors.New("absolute ledger and installation paths required")
	}
	installedPath := filepath.Join(root, "var/lib/nexus-v2/ledger.db")
	a, e1 := os.Stat(installedPath)
	b, e2 := os.Stat(db)
	if e1 != nil || e2 != nil || !os.SameFile(a, b) {
		return nil, errors.New("invite links require the installed ledger")
	}
	data, err := os.ReadFile(filepath.Join(root, "etc/nexus-v2/deployment.json"))
	if err != nil {
		return nil, err
	}
	var c compatDeployment
	if err = json.Unmarshal(data, &c); err != nil {
		return nil, errors.New("invalid deployment configuration")
	}
	i := &shareInvite{Version: 2, Host: c.Domain, Port: c.Ports["v2"]}
	if i.Port < 1 || i.Port > 65535 {
		return nil, errors.New("V2 is not installed")
	}
	if i.Host == "" {
		ip := net.ParseIP(c.PublicHost)
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			return nil, errors.New("invalid installed node IP")
		}
		i.Host = ip.String()
		data, err = os.ReadFile(filepath.Join(root, "var/lib/nexus-v2/ip-cert.pem"))
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, errors.New("invalid V2 IP identity certificate")
		}
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err != nil || leaf.VerifyHostname(i.Host) != nil {
			return nil, errors.New("IP identity does not match installation")
		}
		pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
		i.Pin = base64.RawURLEncoding.EncodeToString(pin[:])
	} else if net.ParseIP(i.Host) != nil || c.PublicHost != "" {
		return nil, errors.New("invalid installed node domain")
	}
	if _, err := proof.Authority(net.JoinHostPort(i.Host, strconv.Itoa(i.Port))); err != nil {
		return nil, err
	}
	return i, nil
}

func (i *shareInvite) write(out io.Writer, token []byte, qr bool) error {
	i.Invite = base64.RawURLEncoding.EncodeToString(token)
	raw, err := json.Marshal(i)
	if err != nil {
		return err
	}
	link := "nexus://invite/v2#" + base64.RawURLEncoding.EncodeToString(raw)
	if _, err = fmt.Fprintln(out, link); err != nil || !qr {
		return err
	}
	// qrencode is optional. Keep the bearer out of process arguments and stderr.
	cmd := exec.Command("qrencode", "-t", "UTF8")
	cmd.Stdin = strings.NewReader(link)
	var terminal bytes.Buffer
	cmd.Stdout = &terminal
	if cmd.Run() == nil {
		_, err = terminal.WriteTo(out)
	}
	return err
}
