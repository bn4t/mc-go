package blackbox

// TestStartedRidingParity mounts a boat carrying a goat and diffs the
// advancement flush: husbandry/ride_a_boat_with_a_goat grants when a
// player starts riding a #minecraft:boat vehicle that already carries
// a goat passenger.
//
// Vanilla fires START_RIDING_TRIGGER inside Entity.startRiding for
// ServerPlayer riders; the criterion's player predicate carries a
// vehicle sub-predicate (vehicle.entity_type=#minecraft:boat and
// vehicle.passenger.entity_type=minecraft:goat). The goat arrives via
// the boat's Passengers summon NBT — vanilla's recursive entity load —
// and the player mounts through the entity Interact packet.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestStartedRidingParity(t *testing.T) {
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

		sendCommand(t, pw, "fill 8 -60 8 8 -60 12 minecraft:stone")
		sendCommand(t, pw, "summon minecraft:oak_boat 8.5 -59 11 {Passengers:[{id:\"minecraft:goat\"}]}")
		sendCommand(t, pw, "tp @s 8.5 -59 9.5 0 0")

		// The boat's spawn packet carries its entity id — entity type
		// 89 = minecraft:oak_boat.
		var boatID int32
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) && boatID == 0 {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			if p.ID == server.IDPlaySpawnEntity {
				r := protocol.NewPacketReader(p.Data)
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == 89 {
					boatID = id
				}
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
		}
		if boatID == 0 {
			t.Fatalf("no boat spawn packet")
		}
		var ib protocol.Buffer
		ib.VarInt(boatID)
		ib.VarInt(0) // main hand
		ib.Byte(0)   // LpVec3 zero
		ib.Bool(false)
		_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		advCollect(t, c, pr, pw, 3*time.Second, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:husbandry/ride_a_boat_with_a_goat"]["ride_a_boat_with_a_goat"] {
		t.Fatalf("vanilla: ride_a_boat_with_a_goat not obtained: %v", want.prog["minecraft:husbandry/ride_a_boat_with_a_goat"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("started_riding parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
