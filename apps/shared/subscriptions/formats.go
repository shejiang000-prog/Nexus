package subscriptions

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	shortID   = regexp.MustCompile(`^[0-9a-fA-F]{0,16}$`)
	publicKey = regexp.MustCompile(`^[A-Za-z0-9_-]{43}=?$`)
	certPin   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	alpnValue = regexp.MustCompile(`^[A-Za-z0-9./_-]{1,32}$`)
)

const fingerprints = "chrome firefox safari ios android edge 360 qq random randomized randomizednoalpn"
const ssCiphers = "aes-128-gcm aes-256-gcm chacha20-poly1305 chacha20-ietf-poly1305 xchacha20-poly1305 xchacha20-ietf-poly1305 none plain 2022-blake3-aes-128-gcm 2022-blake3-aes-256-gcm 2022-blake3-chacha20-poly1305"
const vmessCiphers = "auto aes-128-gcm chacha20-poly1305 none zero"

func legacyVMess(link, name string) (string, string, error) {
	base, fragment, _ := strings.Cut(strings.TrimPrefix(link, "vmess://"), "#")
	p, e := decode64(base)
	if e != nil || !json.Valid(p) {
		return "", "", errFormat
	}
	root, e := checkedTree(p)
	if e != nil {
		return "", "", e
	}
	o, ok := root.(object)
	if !ok {
		return "", "", errFormat
	}
	if e := checkRestrictions(o); e != nil {
		return "", "", e
	}
	if name == "" {
		name, e = url.PathUnescape(fragment)
		if e != nil {
			return "", "", errFormat
		}
		if name == "" {
			name = o.text("ps")
		}
	}
	s := object{"server": o.text("add"), "port": o.text("port"), "uuid": o.text("id"), "cipher": o.text("scy"), "alterId": o.text("aid"), "network": o.text("net"), "client-fingerprint": o.text("fp"), "alpn": o["alpn"], "servername": o.text("sni")}
	host, path, header := o.text("host"), o.text("path"), strings.ToLower(o.text("type"))
	switch strings.ToLower(o.text("net")) {
	case "", "tcp", "raw":
		if header == "http" {
			s["network"] = "http"
			s["http-opts"] = object{"path": path, "headers": object{"Host": host}}
		} else if header != "" && header != "none" {
			return "", "", errFormat
		}
	case "ws", "httpupgrade":
		s["ws-opts"] = object{"path": path, "headers": object{"Host": host}}
	case "grpc":
		s["grpc-opts"] = object{"grpc-service-name": path}
	case "xhttp", "splithttp":
		opt := object{"path": path, "host": host}
		if header != "none" {
			opt["mode"] = header
		}
		s["xhttp-opts"] = opt
	default:
		return "", "", errFormat
	}
	switch strings.ToLower(o.text("tls")) {
	case "", "none":
	case "tls":
		s["tls"] = "true"
		if s.text("servername") == "" {
			first, _, _ := strings.Cut(host, ",")
			s["servername"] = first
		}
	default:
		return "", "", errFormat
	}
	out, e := clashLink(s, "vmess")
	if e == nil && o.text("net") == "grpc" {
		if host != "" {
			out += "&authority=" + url.QueryEscape(host)
		}
		if listed(header, "gun multi") {
			out += "&mode=" + header
		}
	}
	return out, name, e
}

func clashLink(s object, typ string) (string, error) {
	host := strings.TrimSuffix(strings.TrimPrefix(s.text("server"), "["), "]")
	port := s.text("port")
	n, e := strconv.Atoi(port)
	if !hostPattern.MatchString(host) || e != nil || n < 1 || n > 65535 {
		return "", errFormat
	}
	if _, ok := s["tls"].(object); ok || s.child("transport") != nil || s.child("ech-opts").flag("enable") {
		return "", errFormat
	}
	hy := typ == "hy2" || typ == "hysteria2"
	q := url.Values{}
	put := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	hostHeader := ""
	if !hy {
		network := strings.ToLower(s.text("network"))
		switch network {
		case "", "tcp", "raw":
			q.Set("type", "tcp")
		case "ws", "websocket", "httpupgrade":
			opts := s.child("ws-opts")
			headers := opts.child("headers")
			if headers == nil {
				headers = s.child("ws-headers")
			}
			hostHeader = headers.text("Host", "host")
			if hostHeader == "" {
				hostHeader = opts.text("host")
			}
			put("host", hostHeader)
			path := opts.text("path")
			if path == "" {
				path = s.text("ws-path")
			}
			if path == "" {
				path = "/"
			}
			network = "ws"
			if strings.EqualFold(s.text("network"), "httpupgrade") || opts.flag("v2ray-http-upgrade") {
				network = "httpupgrade"
			}
			if ed, e := strconv.Atoi(opts.text("max-early-data")); network == "ws" && e == nil && ed > 0 && opts.text("early-data-header-name") == "Sec-WebSocket-Protocol" && !strings.Contains(path, "ed=") {
				join := "?"
				if strings.Contains(path, "?") {
					join = "&"
				}
				path += join + "ed=" + strconv.Itoa(ed)
			}
			q.Set("path", path)
			q.Set("type", network)
		case "grpc":
			q.Set("type", "grpc")
			q.Set("serviceName", s.child("grpc-opts").text("grpc-service-name"))
		case "http":
			opts := s.child("http-opts")
			q.Set("type", "tcp")
			q.Set("headerType", "http")
			put("path", strings.Join(opts.list("path"), ","))
			put("host", strings.Join(opts.child("headers").list("Host"), ","))
		case "xhttp", "splithttp":
			opts := s.child("xhttp-opts")
			path := opts.text("path")
			if path == "" {
				path = "/"
			}
			q.Set("type", "xhttp")
			q.Set("path", path)
			hostHeader = opts.text("host")
			put("host", hostHeader)
			mode := opts.text("mode")
			if mode != "" && !listed(mode, "auto packet-up stream-up stream-one") {
				return "", errFormat
			}
			put("mode", mode)
		default:
			return "", errFormat
		}
	}
	reality := s.child("reality-opts")
	secure := typ == "trojan" || hy || s.flag("tls") || reality != nil
	if reality != nil {
		k, sid := reality.text("public-key"), reality.text("short-id")
		if !publicKey.MatchString(k) || !shortID.MatchString(sid) {
			return "", errFormat
		}
		q.Set("security", "reality")
		q.Set("pbk", k)
		put("sid", sid)
	} else if secure {
		q.Set("security", "tls")
	}
	if secure {
		sni := s.text("servername", "sni")
		if sni == "" {
			sni = hostHeader
		}
		if sni != "" && !hostPattern.MatchString(sni) {
			return "", errFormat
		}
		put("sni", sni)
		alpn := s.list("alpn")
		if len(alpn) > 8 {
			return "", errFormat
		}
		for _, v := range alpn {
			if !alpnValue.MatchString(v) {
				return "", errFormat
			}
		}
		put("alpn", strings.Join(alpn, ","))
		if pin := strings.ToLower(strings.ReplaceAll(s.text("fingerprint"), ":", "")); pin != "" {
			if !certPin.MatchString(pin) {
				return "", errFormat
			}
			put("pcs", pin)
		}
		if fp := strings.ToLower(s.text("client-fingerprint")); !hy && listed(fp, fingerprints) {
			put("fp", fp)
		}
	}
	id := s.text("password")
	switch typ {
	case "vless":
		id = s.text("uuid")
		flow := s.text("flow")
		if flow != "" && !listed(flow, "xtls-rprx-vision xtls-rprx-vision-udp443") {
			return "", errFormat
		}
		put("flow", flow)
		if enc := s.text("encryption"); enc != "none" {
			if len(enc) > 2048 {
				return "", errFormat
			}
			put("encryption", enc)
		}
	case "vmess":
		id = s.text("uuid")
		if v := s.text("alterId", "aid"); v != "" && v != "0" {
			return "", errFormat
		}
		c := strings.ToLower(s.text("cipher"))
		if c == "" {
			c = "auto"
		}
		if !listed(c, vmessCiphers) {
			return "", errFormat
		}
		put("encryption", c)
	case "ss":
		c := strings.ToLower(s.text("cipher", "method"))
		if !listed(c, ssCiphers) || id == "" {
			return "", errFormat
		}
		id = base64.RawURLEncoding.EncodeToString([]byte(c + ":" + id))
	case "hy2", "hysteria2":
		id = s.text("password", "auth")
		obfs := s.text("obfs")
		if obfs != "" && obfs != "none" {
			password := s.text("obfs-password")
			if obfs != "salamander" || len(password) < 4 {
				return "", errFormat
			}
			q.Set("obfs", obfs)
			put("obfs-password", password)
		}
		if hops := strings.ReplaceAll(strings.ReplaceAll(s.text("ports"), "/", ","), " ", ""); hops != "" {
			put("mport", hops)
			if interval := s.text("hop-interval"); interval != "" {
				number := interval
				if strings.HasSuffix(number, "s") || strings.HasSuffix(number, "m") {
					number = number[:len(number)-1]
				}
				seconds, e := strconv.Atoi(number)
				if strings.HasSuffix(interval, "m") {
					seconds *= 60
				}
				if e != nil || seconds < 5 || seconds > 3600 {
					return "", errFormat
				}
				put("hop-interval", strconv.Itoa(seconds))
			}
		}
	}
	if id == "" {
		return "", errFormat
	}
	// Encode userinfo as a single field, including literal @, colon, plus and slash.
	return typ + "://" + strings.ReplaceAll(url.QueryEscape(id), "+", "%20") + "@" + net.JoinHostPort(host, port) + "?" + q.Encode(), nil
}
