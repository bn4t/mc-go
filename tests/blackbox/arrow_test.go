package blackbox

// TestShootArrowParity draws a bow, releases at a summoned zombie, and
// diffs the advancement flush: adventure/shoot_arrow's shot_arrow
// criterion is player_hurt_entity with direct_entity #arrows +
// is_projectile — the arrow-hit path has to satisfy all of it.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestShootArrowParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Arrow"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Arrow")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		// A zombie in survival draws aggro (and melee AI would chase);
		// NoAI keeps the geometry fixed for the shot. Creative mode on
		// the player avoids the survival arrow consumption check.
		sendCommand(t, pw, "tp @s 4 -60 2 0 0")
		sendCommand(t, pw, "summon minecraft:zombie 4 -60 12 {NoAI:1b}")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:bow")
		advCollect(t, c, pr, pw, 900*time.Millisecond, f)

		drain := func(d time.Duration) {
			end := time.Now().Add(d)
			for time.Now().Before(end) {
				_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
				p, err := pr.ReadPacket()
				if err != nil {
					return
				}
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					var kb protocol.Buffer
					kb.Raw(p.Data)
					_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
				case server.IDPlaySyncPos:
					r := protocol.NewPacketReader(p.Data)
					if id, err := r.VarInt(); err == nil {
						var b protocol.Buffer
						b.VarInt(id)
						_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
					}
				case server.IDPlayUpdateAdvance:
					decodeUpdateAdv(t, p.Data, f)
				case server.IDPlaySystemChatCB:
					f.chats = append(f.chats, chatKey(p.Data))
				}
			}
		}

		// Use Item (hand 0) begins the draw; Player Action status 5 is
		// the release. Hold ~1.4s — past the 20-tick full-charge mark.
		var ub protocol.Buffer
		ub.VarInt(0) // main hand
		ub.VarInt(0) // sequence
		ub.Float(0)  // yaw
		ub.Float(0)  // pitch
		_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		drain(1400 * time.Millisecond)
		var ab protocol.Buffer
		ab.VarInt(5) // RELEASE_USE_ITEM
		ab.Position(0, 0, 0)
		ab.Byte(0)   // face (unused)
		ab.VarInt(0) // sequence
		_ = pw.WritePacket(server.IDPlayPlayerActionSB, ab.Bytes())
		// The arrow needs ~5 ticks to cover 10 blocks plus the tick-end
		// flush; leave generous drain room for scheduling jitter.
		drain(3 * time.Second)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:adventure/shoot_arrow"]["shot_arrow"] {
		t.Fatalf("vanilla: shot_arrow not obtained: %v", want.prog["minecraft:adventure/shoot_arrow"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("shoot_arrow parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
