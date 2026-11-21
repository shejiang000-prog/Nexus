package xconnect

import "testing"

func TestSetting(t *testing.T) {
	for _, s := range []string{"other=2", "http2xconnect=0,other=2,http2xconnect=1", "other=2,http2xconnect=1"} {
		if got := setting(s); got != "other=2,http2xconnect=1" || setting(got) != got {
			t.Fatal(got)
		}
	}
}
