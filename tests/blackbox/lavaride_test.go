package blackbox

// TestRideEntityInLavaParity rides a saddled strider in overworld lava
// and diffs the advancement flush: nether/ride_strider_in_overworld_lava
// grants once the player travels 50 horizontal blocks from the latched
// lava-entry position.
//
// Vanilla fires RIDE_ENTITY_IN_LAVA per ServerPlayer tick while
// getVehicle().isInLava(), latching the entry position
// (enteredLavaOnVehiclePosition) on the first tick; the criterion is a
// DistancePredicate (distance.horizontal.min:50) plus an
// entity_properties player predicate (location.dimension=overworld,
// vehicle.entity_type=strider). The strider is saddled via the
// equipment.saddle summon slot (bareback striders refuse riders), and
// the 50 blocks come from teleporting the vehicle — a ridden entity
// carries its passenger along.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestRideEntityInLavaParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("RideLav"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "RideLav")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "fill 8 -60 9 8 -60 12 minecraft:stone")
		sendCommand(t, pw, "fill 10 -61 8 75 -61 8 minecraft:lava")
		sendCommand(t, pw, `summon minecraft:strider 10.5 -60.3 8.5 {NoAI:1b,Saddle:1b,equipment:{saddle:{id:"minecraft:saddle",count:1}}}`)
		sendCommand(t, pw, "tp @s 10.5 -59 9.5 0 0")

		// The strider's spawn packet carries its entity id — type 129.
		var striderID int32
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) && striderID == 0 {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == 129 {
					striderID = id
				}
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			}
		}
		if striderID == 0 {
			t.Fatalf("no strider spawn packet")
		}
		var ib protocol.Buffer
		ib.VarInt(striderID)
		ib.VarInt(0) // main hand
		ib.Byte(0)   // LpVec3 zero
		ib.Bool(false)
		_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		// Let the mount and the first in-lava tick latch the entry
		// position before the vehicle teleports 60 blocks along the
		// trench.
		advCollect(t, c, pr, pw, 500*time.Millisecond, f)
		sendCommand(t, pw, "tp @e[type=minecraft:strider] 70.5 -60.3 8.5")
		advCollect(t, c, pr, pw, 3*time.Second, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:nether/ride_strider_in_overworld_lava"]["ride_entity_distance"] {
		t.Fatalf("vanilla: ride_entity_distance not obtained: %v", want.prog["minecraft:nether/ride_strider_in_overworld_lava"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("ride_entity_in_lava parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}

// TestItemDurabilityChangedParity uses a warped_fungus_on_a_stick while
// controlling a saddled strider: vanilla's FoodOnAStickItem.use boosts
// the steerable and wears the stick by 1, firing item_durability_changed
// — nether/ride_strider's criterion matches the item and the strider
// vehicle.
func TestItemDurabilityChangedParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("FungUs"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "FungUs")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		// Vanilla joins creative by harness default; hurtAndBreak is a
		// no-op there (creative never wears items), so the durability
		// trigger needs survival first.
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "fill 8 -60 8 8 -60 12 minecraft:stone")
		sendCommand(t, pw, `summon minecraft:strider 8.5 -59 10.5 {NoAI:1b,Saddle:1b,equipment:{saddle:{id:"minecraft:saddle",count:1}}}`)
		sendCommand(t, pw, "item replace entity @s container.0 with minecraft:warped_fungus_on_a_stick")
		sendCommand(t, pw, "tp @s 8.5 -59 9.5 0 0")

		var striderID int32
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) && striderID == 0 {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == 129 {
					striderID = id
				}
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			}
		}
		if striderID == 0 {
			t.Fatalf("no strider spawn packet")
		}
		var ib protocol.Buffer
		ib.VarInt(striderID)
		ib.VarInt(0) // main hand
		ib.Byte(0)   // LpVec3 zero
		ib.Bool(false)
		_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		advCollect(t, c, pr, pw, 500*time.Millisecond, f)

		var ub protocol.Buffer
		ub.VarInt(0) // main hand
		ub.VarInt(0) // sequence
		ub.Float(0)  // yaw
		ub.Float(0)  // pitch
		_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		advCollect(t, c, pr, pw, 3*time.Second, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:nether/ride_strider"]["used_warped_fungus_on_a_stick"] {
		t.Fatalf("vanilla: used_warped_fungus_on_a_stick not obtained: %v", want.prog["minecraft:nether/ride_strider"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("item_durability_changed parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
