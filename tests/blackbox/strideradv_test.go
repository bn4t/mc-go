package blackbox

// TestRideStriderAdvParity pins the item_durability_changed
// advancement trigger end to end on both servers — measured from
// 26.2's FoodOnAStickItem.use: a successful boost spends
// consumeItemDamage=1 via hurtAndConvertOnBreak, whose
// ITEM_DURABILITY_CHANGED fire carries the worn stack and the ridden
// vehicle. nether/ride_strider's used_warped_fungus_on_a_stick
// criterion wants exactly that pair, so a survival mount + use
// grants This Boat Has Legs (def + progress + the task announce).
//
// Creative can't fire it — hurtAndConvertOnBreak early-returns on
// creative like vanilla's no-wear rule — so the leg runs survival.
// The boost emit itself is already pinned by TestStriderParity; this
// test compares only the advancement flush (defs, progress, chats).

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestRideStriderAdvParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Rider"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		c, pr, pw := joinPlayBlackbox(t, addr, "Rider")
		defer c.Close()
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		var sid int32 = -1

		fold := func(d time.Duration) {
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
				case server.IDPlaySpawnEntity:
					r := protocol.NewPacketReader(p.Data)
					eid, _ := r.VarInt()
					r.UUID()
					ty, _ := r.VarInt()
					ex, _ := r.Double()
					if ty == 129 && int(ex) == 8 { // strider wire id
						sid = eid
					}
				}
			}
		}

		fold(1200 * time.Millisecond)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 8 -60 10 -90 0")
		sendCommand(t, pw, `summon minecraft:strider 8.5 -60 10.5 {NoAI:1b,equipment:{saddle:{id:"minecraft:saddle",count:1}}}`)
		fold(1200 * time.Millisecond)
		if sid < 0 {
			t.Fatalf("%s: strider never spawned", addr)
		}
		sendCommand(t, pw, "give @s minecraft:warped_fungus_on_a_stick")
		fold(600 * time.Millisecond)

		// Mount: entity interact with empty mainhand → startRiding.
		var ib protocol.Buffer
		ib.VarInt(sid)
		ib.VarInt(0) // interact action
		ib.VarInt(0) // hand main
		ib.Bool(false)
		_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		fold(800 * time.Millisecond)

		// Boost: use_item against open air while controlling the
		// strider — ITEM_DURABILITY_CHANGED grants ride_strider.
		var ub protocol.Buffer
		ub.VarInt(0)
		ub.VarInt(0)
		ub.Float(0)
		ub.Float(0)
		_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		fold(1500 * time.Millisecond)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	if !want.prog["minecraft:nether/ride_strider"]["used_warped_fungus_on_a_stick"] {
		t.Fatalf("vanilla never granted ride_strider — measurement changed")
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("advancement parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
