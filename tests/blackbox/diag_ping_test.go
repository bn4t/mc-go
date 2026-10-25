package blackbox

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestDiagPingDiagnostic probes the play-stage Ping Request framing
// against the real server: each candidate encoding gets a fresh
// connection, and the one vanilla decodes without disconnecting is the
// wire shape. The wiki does not document this packet's body, so the
// oracle is the server itself.
//
//	MCGO_BLACKBOX=1 MCGO_DIAG=1 go test ./tests/blackbox -run TestDiagPingDiagnostic
func TestDiagPingDiagnostic(t *testing.T) {
	if os.Getenv("MCGO_DIAG") != "1" {
		t.Skip("set MCGO_DIAG=1 to run the ping diagnostic")
	}
	tg := requireBlackbox(t)
	defer tg.cleanup()

	candidates := []struct {
		name string
		body func() []byte
	}{
		{"int", func() []byte {
			var b protocol.Buffer
			b.Int(0x5EED1234)
			return b.Bytes()
		}},
		{"long", func() []byte {
			var b protocol.Buffer
			b.Long(0x5EED1234)
			return b.Bytes()
		}},
		{"varint", func() []byte {
			var b protocol.Buffer
			b.VarInt(0x5EED1234)
			return b.Bytes()
		}},
		{"empty", func() []byte { return nil }},
	}
	for _, cand := range candidates {
		c, pr, pw := joinPlayBlackbox(t, tg.vanilla, "PingProbe")
		_ = c.SetDeadline(time.Now().Add(20 * time.Second))
		sendClientInformation(t, pw)
		if err := pw.WritePacket(server.IDPlayPingRequestSB, cand.body()); err != nil {
			t.Fatal(err)
		}
		verdict := "no reply"
		for i := 0; i < 40; i++ {
			p, err := pr.ReadPacket()
			if err != nil {
				verdict = "closed: " + err.Error()
				break
			}
			if p.ID == server.IDPlayDisconnect {
				verdict = "disconnect: " + printable(p.Data)
				break
			}
			if p.ID == server.IDPlayPongResponseCB {
				verdict = "pong raw " + printable(p.Data)
				break
			}
			if p.ID == server.IDPlayKeepAliveCB {
				r := protocol.NewPacketReader(p.Data)
				if id, err := r.Long(); err == nil {
					var b protocol.Buffer
					b.Long(id)
					_ = pw.WritePacket(server.IDPlayKeepAliveSB, b.Bytes())
				}
			}
			if p.ID == server.IDPlayChunkBatchDone {
				var b protocol.Buffer
				b.Float(25)
				_ = pw.WritePacket(server.IDPlayChunkBatchRecvSB, b.Bytes())
			}
		}
		t.Logf("ping_request body %-7s -> %s", cand.name, verdict)
		_ = c.Close()
	}
	if out, err := os.ReadFile(filepath.Join(vanillaDirForDebug, "vanilla.log")); err == nil {
		t.Logf("vanilla log tail:\n%s", tail(out, 1200))
	}
}

// printable renders a packet body as ASCII, so an NBT text component
// can be read without a full decoder.
func printable(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c >= 32 && c < 127 {
			sb.WriteByte(c)
		} else {
			sb.WriteByte('.')
		}
	}
	return sb.String()
}

func itoa(v int32) string { return strconv.FormatInt(int64(v), 10) }
