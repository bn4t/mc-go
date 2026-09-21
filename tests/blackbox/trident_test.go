package blackbox

// TestThrowTridentParity charges a trident past the 10-tick floor,
// releases at a NoAI pig, and diffs the advancement flush:
// adventure/throw_trident's shot_trident criterion is
// player_hurt_entity with direct_entity trident + the is_projectile
// damage tag. The parent (adventure/kill_a_mob) is granted first so
// the child is visible — vanilla hides it otherwise.
//
// Geometry: pig stands 9 blocks north of the player; pitch 8 down
// lands the bolt inside the pig's box (0.9 high, inflated 0.3).
// A fresh trident is equipped each attempt since survival consumes it.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestThrowTridentParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Trident"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Trident")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "advancement grant @s only minecraft:adventure/kill_a_mob")
		sendCommand(t, pw, "tp @s 8 -60 14 180 8")
		sendCommand(t, pw, "summon minecraft:pig 8 -60 5 {NoAI:1b}")
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

		thrown := func() bool {
			return f.prog["minecraft:adventure/throw_trident"]["shot_trident"]
		}

		for attempt := 0; attempt < 3; attempt++ {
			if thrown() {
				break
			}
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:trident")
			drain(400 * time.Millisecond)
			var ub protocol.Buffer
			ub.VarInt(0) // main hand
			ub.VarInt(0) // sequence
			ub.Float(180)
			ub.Float(8)
			_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
			drain(800 * time.Millisecond) // past the 10-tick floor
			var ab protocol.Buffer
			ab.VarInt(5) // RELEASE_USE_ITEM
			ab.Position(0, 0, 0)
			ab.Byte(0)
			ab.VarInt(0)
			_ = pw.WritePacket(server.IDPlayPlayerActionSB, ab.Bytes())
			drain(3 * time.Second)
		}
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:adventure/throw_trident"]["shot_trident"] {
		t.Fatalf("vanilla: shot_trident not obtained: %v", want.prog["minecraft:adventure/throw_trident"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("throw_trident parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
