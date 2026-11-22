package main

import (
	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/fileserver"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls"
	_ "github.com/shejiang000-prog/Nexus/v2/server"
)

func main() {
	caddycmd.Main()
}
