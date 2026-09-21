package blackbox

// TestSpearMobsParity holds a spear jab through a dense zombie line
// and diffs the advancement flush: adventure/spear_many_mobs's
// spear_mobs trigger fires when ≥5 distinct living entities sit in
// the holder's recentKineticEnemies map — entities the jab ray
// collected within the last contact_cooldown_ticks (10), whether or
// not any damage condition passed.
//
// 18 NoAI zombies stand 0.4 apart on a stone row; the player holds
// use and immediately walks forward (~6 b/s, above the 5.1 knockback
// threshold; on-foot speeds can never reach the ≥10 damage gate).
// The jab goes hot ~delay_ticks (0.6s = 12t for iron) into the walk,
// so the ray lands inside the mob line on the first hot tick.
// Zombies (1.95 tall) keep the pitch-0 eye ray inside their hitbox;
// a pig's 0.9 box tops out below it at hitbox_margin 0.125.

import (
	"fmt"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestSpearMobsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Spear"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Spear")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		// The real client reports loaded once terrain is in; without
		// it both servers drop use_item for the first 60 ticks
		// (clientLoadedTimeoutTimer), which would eat the jab.
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

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
				case 0x74, 0x75:
					r := protocol.NewPacketReader(p.Data)
					sid, _ := r.VarInt()
					if sid == 1526 || sid == 1527 || sid == 1528 {
						println("SND", p.ID, sid)
					}
				}
			}
		}

		sendCommand(t, pw, "advancement grant @s only minecraft:adventure/kill_a_mob")
		// Dense zombie row (0.4 spacing) on a stone step at feet -59.
		// Zombies are 1.95 tall (top -57.05) so the pitch-0 eye ray at
		// -57.38 clips their box directly; pigs (top -58.1) sit below
		// hitbox_margin 0.125 and would need a downward pitch.
		sendCommand(t, pw, "fill 8 -60 6 8 -60 14 minecraft:stone")
		for i := 0; i < 18; i++ {
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:zombie 8.5 -59 %.1f {NoAI:1b}", 6.5+0.4*float64(i)))
		}
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:iron_spear")
		// Decimal coords dodge vanilla's block-center snap so the
		// jab ray stays dead-center on the pig line.
		sendCommand(t, pw, "tp @s 8.5 -59 15.0 180 0")
		drain(1200 * time.Millisecond)

		// Hold the jab and walk into the pigs IMMEDIATELY: vanilla's
		// damageEntities collects hits every tick past delay_ticks and
		// remembers each hit entity for contact_cooldown_ticks even
		// when no condition passes — stationary ticks would park the
		// pigs in cooldown while the ray slides past them. The jab
		// goes hot ~10 ticks in (delay_ticks=0.5s); the 2.8-deep
		// segment window then lands >=5 pigs in a single tick, which
		// is what conditions.count:5 on the trigger measures.
		var ub protocol.Buffer
		ub.VarInt(0) // main hand
		ub.VarInt(0) // sequence
		ub.Float(180)
		ub.Float(0)
		_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		z := 15.0
		for i := 0; i < 45; i++ {
			z -= 0.3
			var mb protocol.Buffer
			mb.Double(8.5)
			mb.Double(-59.0)
			mb.Double(z)
			mb.Bool(true)
			_ = pw.WritePacket(server.IDPlayMovePosSB, mb.Bytes())
			time.Sleep(45 * time.Millisecond)
			if i%10 == 9 {
				drain(40 * time.Millisecond)
			}
		}
		drain(3 * time.Second)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:adventure/spear_many_mobs"]["spear_many_mobs"] {
		t.Fatalf("vanilla: spear_many_mobs not obtained: %v", want.prog["minecraft:adventure/spear_many_mobs"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("spear_mobs parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
