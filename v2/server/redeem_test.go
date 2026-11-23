package server

import (
	"fmt"
	"testing"
	"time"
)

func TestRedeemPrefixAndGlobalLimit(t *testing.T) {
	var sources redeemSources
	now := time.Now()
	for prefix := 0; prefix < 16; prefix++ {
		addr := func(host int) string { return fmt.Sprintf("[2001:db8:0:%x::%x]:443", prefix, host) }
		for host := 1; host <= 16; host++ {
			if !sources.allow(addr(host), now) {
				t.Fatalf("prefix %d, address %d denied early", prefix, host)
			}
		}
		if sources.allow(addr(17), now) {
			t.Fatal("IPv6 addresses in the same /64 escaped the source limit")
		}
	}
	if sources.allow("192.0.2.1:443", now) || !sources.allow("192.0.2.1:443", now.Add(time.Second)) {
		t.Fatal("global window limit or reset failed")
	}
}
