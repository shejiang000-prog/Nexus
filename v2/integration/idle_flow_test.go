package integration

import (
	"io"
	"testing"
	"time"
)

func TestOpenFlowsSurviveIdle(t *testing.T) {
	for _, mode := range []string{"h2", "h3"} {
		for _, network := range []string{"tcp", "udp"} {
			t.Run(mode+"/"+network, func(t *testing.T) {
				t.Parallel()
				testNode(t, true, false, func(t *testing.T, l *nodeLab) {
					_, client := l.client(t, mode)
					deadline := time.AfterFunc(time.Minute, client.Stop)
					defer deadline.Stop()
					var echo func(string)
					if network == "tcp" {
						stream, err := client.DialTCP(t.Context(), l.tcp, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer stream.Close()
						echo = func(payload string) {
							t.Helper()
							if n, err := stream.Write([]byte(payload)); err != nil || n != len(payload) {
								t.Fatalf("write %q: %d bytes, %v", payload, n, err)
							}
							got := make([]byte, len(payload))
							if _, err := io.ReadFull(stream, got); err != nil || string(got) != payload {
								t.Fatalf("echo %q: got %q, %v", payload, got, err)
							}
						}
					} else {
						session, err := client.OpenUDP(t.Context(), l.udp)
						if err != nil {
							t.Fatal(err)
						}
						defer session.Close()
						echo = func(payload string) {
							t.Helper()
							if err := session.WritePacket([]byte(payload)); err != nil {
								t.Fatalf("write %q: %v", payload, err)
							}
							if got, err := session.ReadPacket(); err != nil || string(got) != payload {
								t.Fatalf("echo %q: got %q, %v", payload, got, err)
							}
						}
					}
					echo("before idle")
					time.Sleep(45 * time.Second)
					echo("after idle")
				})
			})
		}
	}
}
