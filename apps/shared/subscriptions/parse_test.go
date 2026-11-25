package subscriptions

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSubscriptionParity(t *testing.T) {
	data, err := os.ReadFile("testdata/parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []struct {
		Case, Input string
		// Android JVM output, including the complete converted configuration.
		Expected []any
	}
	if err := json.Unmarshal(data, &samples); err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		t.Run(s.Case, func(t *testing.T) {
			nodes, err := Parse([]byte(s.Input))
			if (err != nil) != (len(s.Expected) == 0) {
				t.Fatal("unexpected import result", err)
			}
			if err != nil {
				return
			}
			raw, err := json.Marshal(nodes)
			var actual []any
			if err != nil || json.Unmarshal(raw, &actual) != nil {
				t.Fatal("cannot serialize imported nodes")
			}
			if !reflect.DeepEqual(actual, s.Expected) {
				t.Fatal("subscription nodes differ from Android's host output")
			}
		})
	}
	for _, unsafe := range [][]byte{
		bytes.Repeat([]byte(" "), MaxBytes+1),
		[]byte(strings.Repeat("ss://bad\n", 2048) + "trojan://test@ok.test:443#one"),
		[]byte("proxies: " + strings.Repeat("[", 25) + "{}" + strings.Repeat("]", 25)),
		[]byte("vless://user@one.test:443#one\nrouting: {}"),
		[]byte("proxies: [{type: trojan, name: n, server: ok.test, port: 443, password: p}]\nexternal-controller: 127.0.0.1:8080"),
	} {
		if _, err := Parse(unsafe); err == nil {
			t.Fatal("accepted unsafe subscription")
		}
	}
	if ValidateOutbound([]byte(`{"tag":"selected","protocol":"trojan","settings":{"address":"ok.test","port":443,"password":"p"},"streamSettings":{"tlsSettings":{"masterKeyLog":"unsafe.log"}}}`)) == nil {
		t.Fatal("accepted a file-bearing TLS setting")
	}
}
