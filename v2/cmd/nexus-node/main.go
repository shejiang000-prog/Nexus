package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/fileserver"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls"
	"github.com/shejiang000-prog/Nexus/v2/proof"
	"github.com/shejiang000-prog/Nexus/v2/server"
	"io"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "ledger" {
		if err := admin(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	caddycmd.Main()
}

func admin(args []string, out io.Writer) error {
	usage := errors.New("usage: nexus-node ledger ABSOLUTE_DB {create-user [RFC3339] | invite USER_HEX RFC3339 | users | devices | revoke DEVICE_HEX | audit}")
	if len(args) < 2 {
		return usage
	}
	var id proof.ID
	var until time.Time
	var err error
	switch args[1] {
	case "create-user":
		if len(args) > 3 {
			return usage
		}
		rand.Read(id[:])
		if len(args) == 3 {
			until, err = time.Parse(time.RFC3339, args[2])
		}
	case "invite", "revoke":
		if args[1] == "invite" && len(args) != 4 || args[1] == "revoke" && len(args) != 3 {
			return usage
		}
		if len(args[2]) != 32 {
			return usage
		}
		if _, err := hex.Decode(id[:], []byte(args[2])); err != nil {
			return usage
		}
		if len(args) == 4 {
			until, err = time.Parse(time.RFC3339, args[3])
		}
	case "users", "devices", "audit":
		if len(args) != 2 {
			return usage
		}
	default:
		return usage
	}
	if err != nil {
		return usage
	}
	l, err := server.OpenLedger(args[0])
	if err != nil {
		return err
	}
	defer l.Close()
	ctx := context.Background()
	switch args[1] {
	case "create-user":
		if err := l.CreateUserUntil(ctx, id, until); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, hex.EncodeToString(id[:]))
	case "invite":
		token, e := l.Invite(ctx, id, until)
		if e != nil {
			return e
		}
		_, err = fmt.Fprintln(out, base64.RawURLEncoding.EncodeToString(token))
	case "revoke":
		return l.Revoke(ctx, id)
	default:
		return l.List(ctx, args[1], out)
	}
	return err
}
