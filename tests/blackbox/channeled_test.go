package blackbox

// TestChanneledLightningParity throws a channeling trident at a
// villager under /weather thunder and diffs the advancement flush:
// adventure/very_very_frightening's struck_villager criterion is
// channeled_lightning with a victims predicate. Channeling is
// data-driven in 26.2 — the trident's post_attack effect summons a
// lightning_bolt at the victim gated by thundering + can_see_sky —
// and the bolt's tick fires the trigger on the thrower with the
// entities inside its ±3 box as victims.
//
// Geometry matches TestThrowTridentParity: villager 9 blocks north,
// pitch 8 down lands the strike inside its box. The throw also
// grants adventure/throw_trident, which is the parent — the child
// becomes visible the same tick on both servers.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestChanneledLightningParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Channeled"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Channeled")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		// Survival on both legs — vanilla joins creative, and a
		// creative throw marks the entity CREATIVE_ONLY (it still
		// channels, but survival keeps the consumption identical).
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "weather thunder")
		sendCommand(t, pw, "advancement grant @s only minecraft:adventure/kill_a_mob")
		// Vanilla's thunder level ramps 0.01/tick and isThundering
		// needs >0.9 — the channeling weather_check fails for roughly
		// the first 90 ticks after the command. Wait it out.
		advCollect(t, c, pr, pw, 6*time.Second, f)
		sendCommand(t, pw, "tp @s 8 -60 14 180 8")
		sendCommand(t, pw, "summon minecraft:villager 8 -60 5 {NoAI:1b}")
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

		struck := func() bool {
			return f.prog["minecraft:adventure/very_very_frightening"]["struck_villager"]
		}

		for attempt := 0; attempt < 3; attempt++ {
			if struck() {
				break
			}
			sendCommand(t, pw, `item replace entity @s weapon.mainhand with minecraft:trident[minecraft:enchantments={"minecraft:channeling":1}]`)
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
	if !want.prog["minecraft:adventure/very_very_frightening"]["struck_villager"] {
		t.Fatalf("vanilla: struck_villager not obtained: %v", want.prog["minecraft:adventure/very_very_frightening"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("channeled_lightning parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
