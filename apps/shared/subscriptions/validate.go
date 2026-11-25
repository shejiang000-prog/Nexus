package subscriptions

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
)

var protocolFields = map[string]string{
	"vless": "address port id flow encryption", "vmess": "address port id security",
	"shadowsocks": "address port method password", "trojan": "address port password",
	"hysteria": "version address port",
}

// These are the data fields emitted by the pinned share projection for supported
// transports. Runtime configuration never accepts an arbitrary Xray JSON object.
var containers = map[string]string{
	"streamSettings": "network security rawSettings wsSettings grpcSettings httpupgradeSettings xhttpSettings hysteriaSettings tlsSettings realitySettings finalmask",
	"rawSettings":    "header", "header": "type request", "request": "path headers", "headers": "Host",
	"wsSettings": "host path", "httpupgradeSettings": "host path", "xhttpSettings": "host path mode",
	"grpcSettings": "authority serviceName multiMode", "hysteriaSettings": "version auth",
	"tlsSettings":     "serverName alpn fingerprint pinnedPeerCertSha256",
	"realitySettings": "serverName fingerprint shortId password spiderX",
	"finalmask":       "udp",
}

// ValidateOutbound also applies to a previously saved node immediately before
// Build. Unknown nested objects cannot introduce dialers, files or control APIs.
func ValidateOutbound(raw json.RawMessage) error {
	if len(raw) > MaxBytes || !json.Valid(raw) {
		return errFormat
	}
	v, e := checkedTree(raw)
	if e != nil {
		return e
	}
	o, ok := v.(object)
	if !ok {
		return errFormat
	}
	p := o.text("protocol")
	if protocolFields[p] == "" || o.text("tag") != "selected" {
		return errFormat
	}
	settings := o.child("settings")
	if !hostPattern.MatchString(settings.text("address")) {
		return errFormat
	}
	port, e := strconv.Atoi(settings.text("port"))
	if e != nil || port < 1 || port > 65535 {
		return errFormat
	}
	stream := o.child("streamSettings")
	if n := stream.text("network"); n != "" && !listed(n, "tcp raw ws grpc httpupgrade xhttp splithttp hysteria") {
		return errFormat
	}
	if s := stream.text("security"); s != "" && !listed(s, "tls reality none") {
		return errUnsafe
	}
	if p == "hysteria" && (stream.text("security") != "tls" || stream.text("network") != "hysteria") {
		return errUnsafe
	}
	var fields func(object, string) error
	fields = func(value object, keys string) error {
		for k, x := range value {
			if !listed(k, keys) {
				return errUnsafe
			}
			nested := containers[k]
			if k == "settings" {
				nested = protocolFields[p]
			}
			if nested != "" {
				sub, ok := x.(object)
				if !ok {
					return errFormat
				}
				if e := fields(sub, nested); e != nil {
					return e
				}
			} else if k == "udp" {
				a, ok := x.([]any)
				if !ok || p != "hysteria" || len(a) > 2 {
					return errUnsafe
				}
				for _, v := range a {
					mask, ok := v.(object)
					if !ok || len(mask) != 2 {
						return errUnsafe
					}
					allowed := ""
					switch mask.text("type") {
					case "salamander":
						if s := mask.child("settings"); s.text("packetSize") == "0" {
							delete(s, "packetSize") // pinned native converter's default
						}
						allowed = "password"
					case "udphop":
						if s := mask.child("settings"); s["remoteIPs"] == nil {
							delete(s, "remoteIPs")
						}
						allowed = "mode remotePorts interval"
					default:
						return errUnsafe
					}
					s := mask.child("settings")
					if s == nil {
						return errFormat
					}
					if e := fields(s, allowed); e != nil {
						return e
					}
				}
			} else {
				items, arr := x.([]any)
				if arr && !listed(k, "alpn Host path") {
					return errFormat
				}
				if !arr {
					items = []any{x}
				}
				for _, item := range items {
					s, ok := item.(string)
					if !ok || strings.IndexFunc(s, unicode.IsControl) >= 0 {
						return errUnsafe
					}
				}
			}
		}
		return nil
	}
	return fields(o, "protocol tag settings streamSettings")
}
