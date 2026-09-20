package blackbox

// TestSleptInBedParity sets a bed at night and right-clicks it:
// adventure/sleep_in_bed's bare "slept_in_bed" criterion fires and the
// task advancement announces in chat.

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestSleptInBedParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("SleepB"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	drain := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, f *advFlush, d time.Duration) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			if p.ID == server.IDPlayUpdateAdvance {
				decodeUpdateAdv(t, p.Data, f)
			}
			if p.ID == server.IDPlaySystemChatCB {
				f.chats = append(f.chats, chatKey(p.Data))
			}
			if p.ID == server.IDPlayKeepAliveCB {
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			}
			if p.ID == server.IDPlaySyncPos {
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			}
			if p.ID == server.IDPlayChunkBatchDone {
				_ = ackChunkBatch(pw)
			}
		}
	}

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "SleepB")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		drain(c, pr, pw, f, 1200*time.Millisecond)
		sendCommand(t, pw, "tp @s 30.5 -60 28.5 0 0")
		sendCommand(t, pw, "time set night")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:red_bed")
		drain(c, pr, pw, f, 1500*time.Millisecond)
		// Place the bed through the real item-use path — a setblock'd
		// bed is only a foot block, which vanilla refuses to sleep in.
		use := func(x, y, z int32, face int32) {
			var b protocol.Buffer
			b.VarInt(0)         // main hand
			b.Position(x, y, z) // clicked block
			b.VarInt(face)      // face
			b.Float(0.5)        // cursor x
			b.Float(0.5)        // cursor y
			b.Float(0.5)        // cursor z
			b.Bool(false)       // inside block
			b.Bool(false)       // hit-miss / world-border flag
			b.VarInt(0)         // sequence
			_ = pw.WritePacket(server.IDPlayUseItemOnSB, b.Bytes())
		}
		use(30, -61, 30, 1) // top of the ground row → bed lands at -60
		drain(c, pr, pw, f, 800*time.Millisecond)
		// Empty hand, then click the placed bed.
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:air")
		drain(c, pr, pw, f, 400*time.Millisecond)
		use(30, -60, 30, 1)
		drain(c, pr, pw, f, 6500*time.Millisecond)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:adventure/sleep_in_bed"]["slept_in_bed"] {
		t.Fatalf("vanilla: sleep_in_bed not granted: %v", want.prog["minecraft:adventure/sleep_in_bed"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("slept_in_bed parity: %d diffs\n%v", len(diffs), diffs)
	}
}
