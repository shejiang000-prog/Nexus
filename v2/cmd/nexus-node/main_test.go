package main

import (
	"testing"

	"github.com/caddyserver/caddy/v2"
)

func TestRegistered(t *testing.T) {
	for _, id := range []string{"http.handlers.nexus", "http.handlers.file_server"} {
		if _, e := caddy.GetModule(id); e != nil {
			t.Fatal(e)
		}
	}
}
