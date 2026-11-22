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
	users, devices, audit := run("users"), run("devices"), run("audit")
	if !strings.Contains(users, user) || devices != "" || !strings.Contains(audit, "create_user") || !strings.Contains(audit, "invite") || strings.Contains(users+devices+audit, token) {
		t.Fatal("wrong management listing")
	}
}
