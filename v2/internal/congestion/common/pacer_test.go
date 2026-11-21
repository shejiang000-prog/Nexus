package common

import (
	"github.com/apernet/quic-go/congestion"
	"github.com/apernet/quic-go/monotime"
	"testing"
)

func TestBudget(t *testing.T) {
	p := NewPacer(func() congestion.ByteCount { return 2000000 })
	now := monotime.Now()
	budget := p.Budget(now)
	p.SentPacket(now, budget)
	if budget <= 0 || p.Budget(now) != 0 || p.TimeUntilSend() <= now {
		t.Fatal("pacing budget")
	}
}
