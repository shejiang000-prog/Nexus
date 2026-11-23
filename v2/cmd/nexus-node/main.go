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
	"strconv"
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
	usage := errors.New("usage: nexus-node ledger ABSOLUTE_DB {create-user [RFC3339] | invite USER_HEX RFC3339 | users | devices | revoke DEVICE_HEX | revoke-user USER_HEX | set-quota USER_HEX BYTES|unlimited | reconcile | backup ABSOLUTE_PATH | audit}")
	if len(args) < 2 {
		return usage
	}
	var id proof.ID
	var quota *int64
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
			if err == nil && until.Unix() <= time.Now().Unix() {
				return errors.New("expiry must be in the future")
			}
		}
	case "invite", "set-quota", "revoke", "revoke-user":
		want := 3
		if args[1] == "invite" || args[1] == "set-quota" {
			want = 4
		}
		if len(args) != want {
			return usage
		}
		if len(args[2]) != 32 {
			return usage
		}
		if _, err := hex.Decode(id[:], []byte(args[2])); err != nil {
			return usage
		}
		if args[1] == "invite" {
			until, err = time.Parse(time.RFC3339, args[3])
		} else if args[1] == "set-quota" && args[3] != "unlimited" {
			n, e := strconv.ParseInt(args[3], 10, 64)
			if e != nil || n < 0 {
				return usage
			}
			quota = &n
		}
	case "backup":
		if len(args) != 3 {
			return usage
		}
		if _, err := os.Stat(args[0]); err != nil {
			return err
		}
	case "users", "devices", "audit", "reconcile":
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
		err = l.Revoke(ctx, id)
		if errors.Is(err, server.ErrAlreadyRevoked) {
			_, err = fmt.Fprintln(out, server.ErrAlreadyRevoked)
		}
	case "revoke-user":
		return l.RevokeUser(ctx, id)
	case "set-quota":
		return l.SetQuota(ctx, id, quota)
	case "reconcile":
		return l.Reconcile(ctx)
	case "backup":
		return l.Backup(ctx, args[2])
	default:
		return l.List(ctx, args[1], out)
	}
	return err
}
