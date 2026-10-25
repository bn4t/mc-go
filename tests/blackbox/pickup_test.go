package blackbox

// TestThrownItemPickupParity drops two items in front of two NoAI
// pickup mobs and diffs the advancement flush: nether/distract_piglin
// (piglin takes a #piglin_loved gold ingot) and husbandry/uh_oh's
// pick_up_dropped_tnt criterion (sulfur_cube swallows
// #sulfur_cube_swallowable TNT).
//
// Vanilla fires THROWN_ITEM_PICKED_UP_BY_ENTITY from
// LivingEntity.onItemPickup when the item entity's Owner resolves to
// a ServerPlayer — the thrower, not the picker — so the test must
// throw via the drop_item player action (which stamps the owner),
// not /summon item. The mob-side scan lives at the top of Mob.aiStep
// (before the NoAi gate), gated by canPickUpLoot && !dead &&
// mobGriefing, sweeping the mob's box inflated by getPickupReach
// (1,0,1); a bare piglin summon has CanPickUpLoot=false, so the NBT
// must set it explicitly. The sulfur_cube's canPickUpLoot override is
// "body slot empty" — no flag needed.
//
// Each thrown stack carries a 40-tick pickup delay (drop sets it);
// pickup can only land ~2s after the drop, so each drop is followed
// by a 3s drain.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestThrownItemPickupParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Pickup"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Pickup")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
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
				}
			}
		}

		sendCommand(t, pw, "advancement grant @s only minecraft:adventure/kill_a_mob")
		sendCommand(t, pw, "fill 8 -60 6 8 -60 14 minecraft:stone")
		sendCommand(t, pw, "summon minecraft:sulfur_cube 8.5 -59 10.0 {NoAI:1b}")
		sendCommand(t, pw, "summon minecraft:piglin 8.5 -59 13.5 {NoAI:1b,CanPickUpLoot:1b,IsImmuneToZombification:1b}")
		// Dropped items slide on landing and drift laterally — open
		// runs overshot by blocks in both axes — so each mob sits in
		// a 1-wide pen (side walls at x=7/9, back wall 1.5 behind)
		// pinning any thrown item inside its pickup reach.
		sendCommand(t, pw, "fill 7 -59 8 7 -56 11 minecraft:stone")
		sendCommand(t, pw, "fill 9 -59 8 9 -56 11 minecraft:stone")
		sendCommand(t, pw, "fill 7 -59 11 9 -56 11 minecraft:stone")
		sendCommand(t, pw, "fill 7 -59 12 7 -56 15 minecraft:stone")
		sendCommand(t, pw, "fill 9 -59 12 9 -56 15 minecraft:stone")
		sendCommand(t, pw, "fill 7 -59 12 9 -56 12 minecraft:stone")
		sendCommand(t, pw, "tp @s 8.5 -59 8.0 0 0")
		drain(500 * time.Millisecond)

		drop := func() {
			var ab protocol.Buffer
			ab.VarInt(4) // DROP_ITEM (single item)
			ab.Position(0, 0, 0)
			ab.Byte(0)
			ab.VarInt(0)
			_ = pw.WritePacket(server.IDPlayPlayerActionSB, ab.Bytes())
		}

		// TNT dropped inside the sulfur_cube pen — then the player
		// teleports far out of its 1.425-block pickup halo so the
		// resting item can't race the mob's scan back into the
		// inventory once the 40-tick delay expires. The drains
		// matter: vanilla eats a drop that shares a tick with a
		// teleport.
		sendCommand(t, pw, "tp @s 8.5 -59 9.5 0 0")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:tnt")
		drain(300 * time.Millisecond)
		drop()
		drain(300 * time.Millisecond)
		sendCommand(t, pw, "tp @s 8.5 -59 0 0 0")
		drain(3 * time.Second)

		// The piglin drop approaches from +z facing north so the
		// player never enters the halo of the resting TNT (an item
		// that outlives its delay picks up whenever a player wanders
		// into range — on either implementation).
		sendCommand(t, pw, "tp @s 8.5 -59 15.5 180 0")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:gold_ingot")
		drain(300 * time.Millisecond)
		drop()
		drain(300 * time.Millisecond)
		sendCommand(t, pw, "tp @s 8.5 -59 20 0 0")
		drain(3 * time.Second)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:nether/distract_piglin"]["distract_piglin"] {
		t.Fatalf("vanilla: distract_piglin not obtained: %v", want.prog["minecraft:nether/distract_piglin"])
	}
	if !want.prog["minecraft:husbandry/uh_oh"]["pick_up_dropped_tnt"] {
		t.Fatalf("vanilla: uh_oh pick_up_dropped_tnt not obtained: %v", want.prog["minecraft:husbandry/uh_oh"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("thrown_item_picked_up_by_entity parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
