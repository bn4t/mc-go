package blackbox

// TestSculkKillParity kills a NoAI pig within 8 blocks of a
// sculk_catalyst and diffs the advancement flush:
// adventure/kill_mob_near_sculk_catalyst carries no conditions — the
// trigger fires purely on a player-caused death inside the catalyst's
// listener range. The parent (kill_a_mob) is granted first so the
// child is visible. The pig takes two full-charge arrows (10 hp vs
// ~6 each); the catalyst sits 3.5 blocks from the death point.
// Sculk spread/bloom is intentionally outside this test's scope.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestSculkKillParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Sculk"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Sculk")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "advancement grant @s only minecraft:adventure/kill_a_mob")
		sendCommand(t, pw, "setblock 8 -61 8 minecraft:sculk_catalyst")
		sendCommand(t, pw, "summon minecraft:pig 8 -60 5 {NoAI:1b}")
		// Equip the bow BEFORE giving arrows: `give` prefers the
		// selected hotbar slot, and `item replace` would overwrite a
		// stack that landed there.
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:bow")
		sendCommand(t, pw, "give @s minecraft:arrow 8")
		sendCommand(t, pw, "tp @s 8 -60 14 180 8")
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

		bloomed := func() bool {
			return f.prog["minecraft:adventure/kill_mob_near_sculk_catalyst"]["kill_mob_near_sculk_catalyst"]
		}
		pigDead := func() bool {
			return f.prog["minecraft:adventure/kill_all_mobs"]["pig"]
		}

		for shot := 0; shot < 4 && !pigDead(); shot++ {
			var ub protocol.Buffer
			ub.VarInt(0)
			ub.VarInt(0)
			ub.Float(180)
			ub.Float(8)
			_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
			drain(1400 * time.Millisecond) // full charge
			var ab protocol.Buffer
			ab.VarInt(5) // RELEASE_USE_ITEM
			ab.Position(0, 0, 0)
			ab.Byte(0)
			ab.VarInt(0)
			_ = pw.WritePacket(server.IDPlayPlayerActionSB, ab.Bytes())
			drain(3 * time.Second)
		}
		drain(1500 * time.Millisecond)
		_ = bloomed()
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:adventure/kill_mob_near_sculk_catalyst"]["kill_mob_near_sculk_catalyst"] {
		t.Fatalf("vanilla: catalyst kill not obtained: %v", want.prog["minecraft:adventure/kill_mob_near_sculk_catalyst"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("sculk kill parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
