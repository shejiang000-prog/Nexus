package main

import (
	"log"

	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/fileserver"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls"
	"github.com/shejiang000-prog/Nexus/v2/internal/xconnect"
	_ "github.com/shejiang000-prog/Nexus/v2/server"
)

func main() {
	if err := xconnect.Ensure(); err != nil {
		log.Fatal(err)
	}
	caddycmd.Main()
}
