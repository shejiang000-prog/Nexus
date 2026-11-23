package main

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
)

func TestRegistered(t *testing.T) {
	for _, id := range []string{"http.handlers.nexus", "http.handlers.file_server"} {
		if _, e := caddy.GetModule(id); e != nil {
			t.Fatal(e)
		}
	}
}

func TestAdminListings(t *testing.T) {
	db := filepath.Join(t.TempDir(), "ledger.db")
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := admin(append([]string{db}, args...), &out); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out.String())
	}
	until := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	user := run("create-user", until)
	token := run("invite", user, until)
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != 32 {
		t.Fatal("bad invitation")
	}
	if err := admin([]string{db, "create-user", "0001-01-01T00:00:00Z"}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted past expiry")
	}
	if err := admin([]string{db, "revoke", strings.Repeat("0", 32)}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted missing device")
	}
	run("set-quota", user, "0")
	if !strings.Contains(run("users"), `"quota_bytes":0`) {
		t.Fatal("zero quota missing")
	}
	run("set-quota", user, "unlimited")
	if !strings.Contains(run("users"), `"quota_bytes":null`) {
		t.Fatal("unlimited quota missing")
	}
	run("reconcile")
	run("backup", filepath.Join(t.TempDir(), "copy.db"))
	users, devices, audit := run("users"), run("devices"), run("audit")
	if !strings.Contains(users, user) || devices != "" || !strings.Contains(audit, "create_user") || !strings.Contains(audit, "invite") || strings.Contains(users+devices+audit, token) {
		t.Fatal("wrong management listing")
	}
}
