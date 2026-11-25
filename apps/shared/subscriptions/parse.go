// Package subscriptions imports data-only nodes for the pinned Xray engine.
package subscriptions

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/xtls/libxray/share"
	"go.yaml.in/yaml/v3"
)

const MaxBytes = 512 << 10

type Node struct {
	Name     string          `json:"name"`
	Protocol string          `json:"protocol"`
	Outbound json.RawMessage `json:"outbound"`
}

// Never propagate native conversion, YAML or URL errors: they may contain credentials.
var errFormat = errors.New("订阅格式或节点参数不受支持")
var errUnsafe = errors.New("订阅含不安全或受限制的设置")

var protocols = map[string]string{"vless": "vless", "vmess": "vmess", "trojan": "trojan", "ss": "shadowsocks", "hysteria2": "hysteria", "hy2": "hysteria"}
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,253}$`)
var controlLine = regexp.MustCompile(`(?i)^[- #]*["']?(inbounds|outbounds|routing|rules|api|listeners|external-controller|dns|tun|fakedns|proxies|proxy-groups|proxy-providers|script)["']?\s*:`)

func listed(s, words string) bool {
	return strings.Contains(" "+words+" ", " "+s+" ") && s != "" && !strings.ContainsRune(s, ' ')
}
func decode64(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	s = strings.NewReplacer("-", "+", "_", "/").Replace(s)
	if strings.Contains(s, "=") {
		return base64.StdEncoding.DecodeString(s)
	}
	return base64.RawStdEncoding.DecodeString(s)
}

// Parse consumes subscription contents only. It never fetches URLs or accepts a full client config.
func Parse(data []byte) ([]Node, error) {
	if len(data) > MaxBytes || !utf8.Valid(data) {
		return nil, errFormat
	}
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "\ufeff"))
	if decoded, err := decode64(raw); err == nil && utf8.Valid(decoded) {
		raw = strings.TrimSpace(string(decoded))
	}
	var nodes []Node
	add := func(typ, name, link string) error {
		n, e := convert(typ, name, link)
		if e == errFormat {
			return nil // Android retains incompatible entries as unavailable; only usable nodes are returned here.
		}
		if e != nil {
			return e
		}
		nodes = append(nodes, n)
		return nil
	}
	links := false
	for line := range strings.SplitSeq(raw, "\n") {
		scheme, _, ok := strings.Cut(strings.TrimSpace(line), "://")
		if ok && protocols[scheme] != "" {
			links = true
			break
		}
	}
	if links && !strings.HasPrefix(raw, "{") && !strings.HasPrefix(raw, "[") {
		candidates := 0
		for line := range strings.SplitSeq(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			t, _, ok := strings.Cut(line, "://")
			if len(line) > 16384 {
				return nil, errFormat
			}
			if controlLine.MatchString(line) || strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[") {
				return nil, errUnsafe
			}
			if !ok || strings.ContainsAny(t, " \t:{}[]") {
				continue
			}
			if protocols[t] == "" {
				continue
			}
			candidates++
			if candidates > 2048 {
				return nil, errFormat
			}
			if err := add(t, "", line); err != nil {
				return nil, err
			}
		}
	} else {
		root, e := checkedTree([]byte(raw))
		if e != nil {
			return nil, e
		}
		if obj, ok := root.(object); ok {
			if len(obj) != 1 || obj["proxies"] == nil {
				return nil, errUnsafe
			}
			root = obj["proxies"]
		}
		entries, ok := root.([]any)
		if !ok || len(entries) > 2048 {
			return nil, errFormat
		}
		for _, v := range entries {
			s, ok := v.(object)
			if !ok {
				return nil, errFormat
			}
			if err := checkRestrictions(s); err != nil {
				return nil, err
			}
			typ := s.text("type")
			if typ == "shadowsocks" {
				typ = "ss"
			}
			if protocols[typ] == "" {
				continue
			}
			link, e := clashLink(s, typ)
			if e == errFormat {
				continue
			}
			if e != nil {
				return nil, e
			}
			name, _ := s["name"].(string)
			if name == "" {
				name, _ = s["tag"].(string)
			}
			if e = add(typ, name, link); e != nil {
				return nil, e
			}
		}
	}
	if len(nodes) == 0 {
		return nil, errFormat
	}
	return nodes, nil
}

type object map[string]any

// Preserve scalar spelling (especially 0123 and 1_000), as Android's TextResolver does.
func checkedTree(data []byte) (any, error) {
	reader := yaml.NewDecoder(bytes.NewReader(data))
	var document, extra yaml.Node
	if reader.Decode(&document) != nil || len(document.Content) != 1 || reader.Decode(&extra) != io.EOF {
		return nil, errFormat
	}
	count := 0
	var walk func(*yaml.Node, int) (any, error)
	walk = func(n *yaml.Node, depth int) (any, error) {
		count++
		if depth > 24 || count > 100000 || n.Kind == yaml.AliasNode || n.Anchor != "" || n.Style&yaml.TaggedStyle != 0 {
			return nil, errUnsafe
		}
		switch n.Kind {
		case yaml.MappingNode:
			m := object{}
			for i := 0; i < len(n.Content); i += 2 {
				key := n.Content[i]
				count++
				if key.Kind != yaml.ScalarNode || key.Anchor != "" || key.Style&yaml.TaggedStyle != 0 || key.Value == "<<" {
					return nil, errUnsafe
				}
				if _, dup := m[key.Value]; dup {
					return nil, errUnsafe
				}
				v, e := walk(n.Content[i+1], depth+1)
				if e != nil {
					return nil, e
				}
				m[key.Value] = v
			}
			return m, nil
		case yaml.SequenceNode:
			a := make([]any, 0, len(n.Content))
			for _, item := range n.Content {
				v, e := walk(item, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			return a, nil
		case yaml.ScalarNode:
			if n.Tag == "!!null" {
				return nil, nil
			}
			return n.Value, nil
		}
		return nil, errFormat
	}
	return walk(document.Content[0], 0)
}

func (o object) text(keys ...string) string {
	for _, k := range keys {
		v := o[k]
		if a, ok := v.([]any); ok && len(a) > 0 {
			v = a[0]
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
func (o object) child(k string) object { c, _ := o[k].(object); return c }
func (o object) list(k string) []string {
	var out []string
	if a, ok := o[k].([]any); ok {
		for _, v := range a {
			if s, ok := v.(string); ok {
				out = append(out, strings.TrimSpace(s))
			}
		}
	} else if s := o.text(k); s != "" {
		for x := range strings.SplitSeq(s, ",") {
			out = append(out, strings.TrimSpace(x))
		}
	}
	return out
}
func (o object) flag(k string) bool { return listed(strings.ToLower(o.text(k)), "true 1 yes") }

func checkRestrictions(v any) error {
	switch x := v.(type) {
	case object:
		for k, val := range x {
			key := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(k))
			if listed(key, "api inbounds outbounds routing rules externalcontroller controller listeners tun dns fakedns sockopt dialerproxy detour certificatefile keyfile certificates certificate certificatepath ca castr downloadsettings proxysettings proxygroups proxyproviders script plugin pluginopts") {
				return errUnsafe
			}
			if listed(key, "allowinsecure insecure skipcertverify") && !listed(strings.ToLower(x.text(k)), "0 false") {
				return errUnsafe
			}
			if e := checkRestrictions(val); e != nil {
				return e
			}
		}
	case []any:
		for _, c := range x {
			if e := checkRestrictions(c); e != nil {
				return e
			}
		}
	}
	return nil
}

const queryKeys = "type security sni alpn fp flow encryption pbk sid spx pcs path host headerType serviceName authority mode obfs obfs-password hop-interval allowInsecure insecure allow_insecure peer pinSHA256 mport ports obfsPassword congestion remarks"

func convert(typ, name, link string) (Node, error) {
	var zero Node
	if len(link) > 16384 {
		return zero, errFormat
	}
	if typ == "vmess" && !strings.Contains(strings.SplitN(link, "#", 2)[0], "@") {
		var err error
		link, name, err = legacyVMess(link, name)
		if err != nil {
			return zero, err
		}
	}
	// Split the query independently: Hysteria supports port ranges in its authority.
	base, fragment, _ := strings.Cut(link, "#")
	prefix, rawQuery, _ := strings.Cut(base, "?")
	desc, err := url.PathUnescape(fragment)
	if err != nil {
		return zero, errFormat
	}
	if name == "" {
		name = desc
	}
	unescaped, e := url.PathUnescape(base)
	if e != nil || strings.IndexFunc(unescaped, unicode.IsControl) >= 0 {
		return zero, errUnsafe
	}
	q, e := url.ParseQuery(rawQuery)
	if e != nil {
		return zero, errFormat
	}
	clean := url.Values{}
	unknown := false
	for k, a := range q {
		if len(a) != 1 || listed(strings.ToLower(k), "extra fm finalmask ech dialerproxy sockopt fakedns api inbounds outbounds routing proxysettings downloadsettings certificatefile keyfile") {
			return zero, errUnsafe
		}
		if listed(strings.ToLower(k), "allowinsecure insecure allow_insecure skip-cert-verify") && !listed(strings.ToLower(a[0]), "false 0") {
			return zero, errUnsafe
		}
		if listed(k, queryKeys) {
			clean[k] = a
		} else {
			unknown = true
		}
	}
	if network := strings.ToLower(q.Get("type")); network != "" && !listed(network, "tcp raw ws grpc httpupgrade xhttp splithttp") {
		return zero, errFormat
	}
	if q.Get("security") == "tls" && q.Get("sni") == "" && listed(typ, "vless vmess trojan") {
		if host, _, _ := strings.Cut(q.Get("host"), ","); hostPattern.MatchString(strings.TrimSpace(host)) {
			clean.Set("sni", strings.TrimSpace(host))
			q.Set("sni", strings.TrimSpace(host))
		}
	}
	// Validate port before the native converter narrows it to uint16.
	if typ != "hy2" && typ != "hysteria2" {
		u, e := url.Parse(prefix)
		if e != nil {
			return zero, errFormat
		}
		n, e := strconv.Atoi(u.Port())
		if e != nil || n < 1 || n > 65535 || !hostPattern.MatchString(u.Hostname()) {
			return zero, errFormat
		}
	}
	makeLink := func(query url.Values) string { return prefix + "?" + query.Encode() }
	cleaned, e := share.ConvertShareLinksToXrayJson(makeLink(clean), "")
	if e != nil {
		return zero, errFormat
	}
	if unknown {
		// Android accepts provider metadata only when removing it leaves the entire
		// native outbound unchanged. Embedded configs/file-bearing options were rejected above.
		full, e := share.ConvertShareLinksToXrayJson(makeLink(q), "")
		if e != nil || !bytes.Equal(full, cleaned) {
			return zero, errUnsafe
		}
	}
	var config struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if json.Unmarshal(cleaned, &config) != nil || len(config.Outbounds) != 1 {
		return zero, errFormat
	}
	o := config.Outbounds[0]
	if o["protocol"] != protocols[typ] {
		return zero, errFormat
	}
	o["tag"] = "selected"
	raw, e := json.Marshal(o)
	if e != nil || ValidateOutbound(raw) != nil {
		return zero, errUnsafe
	}
	label := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	if len(label) > 96 {
		label = label[:96]
	}
	if strings.TrimSpace(string(label)) == "" {
		label = []rune(strings.ToUpper(typ))
	}
	return Node{string(label), protocols[typ], raw}, nil
}
