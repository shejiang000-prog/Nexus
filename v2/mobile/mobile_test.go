package mobile

import (
	"bytes"
	"testing"
)

func TestSignatureBridge(t *testing.T) {
	der := []byte{48, 6, 2, 1, 1, 2, 1, 2}
	r, e := rawSignature(der)
	if e != nil || len(r) != 64 || r[31] != 1 || r[63] != 2 {
		t.Fatal("DER conversion")
	}
	for _, bad := range [][]byte{nil, append(bytes.Clone(der), 0), {48, 6, 2, 1, 0, 2, 1, 2}} {
		if _, e := rawSignature(bad); e == nil {
			t.Fatal("accepted invalid DER")
		}
	}
}
