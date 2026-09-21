package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestAllayDropItemOnBlockParity: an allay that took a cake from a
// player (MAINHAND equipment + LIKED_PLAYER) picks up a matching cake
// item entity into its single-slot inventory — the equipment hand and
// inventory are separate slots in vanilla, and only inventory items
// throw. Once the allay hears a played noteblock within 30 blocks and
// sits within 3 blocks of its above(), it throws one item per tick at
// it — firing allay_drop_item_on_block for the liked player and
// granting husbandry/allay_deliver_cake_to_note_block.
func TestAllayDropItemOnBlockParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("AllayP"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type result struct {
		f      *advFlush
		picked bool // allay removed the ground item
		thrown bool // an item entity appeared on the noteblock
	}

	run := func(addr string) result {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "AllayP")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 8.5 -60 12.5 0 0")
		sendCommand(t, pw, "setblock 8 -60 8 minecraft:note_block")
		sendCommand(t, pw, "summon minecraft:allay 9.5 -59.5 8.5")
		sendCommand(t, pw, "give @s minecraft:cake")

		r := result{f: f}
		itemType, _ := server.EntityTypeID("item")
		allayType, _ := server.EntityTypeID("allay")
		var allayID int32
		spawnedCakes := map[int32]bool{}
		pendingCakes := 0
		fed := false
		var nextSummon time.Time
		end := time.Now().Add(20 * time.Second)
		for time.Now().Before(end) {
			// The allay drifts once it hears the noteblock, so the
			// ground cake is re-dropped where it spawned until the
			// pickup lands — vanilla and mc-go alike.
			if fed && !r.picked && !time.Now().Before(nextSummon) {
				sendCommand(t, pw, "summon minecraft:item 9.5 -59.5 8.5 {Item:{id:\"minecraft:cake\",count:1b}}")
				pendingCakes++
				nextSummon = time.Now().Add(400 * time.Millisecond)
			}
			_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			case server.IDPlaySpawnEntity:
				rp := protocol.NewPacketReader(p.Data)
				id, _ := rp.VarInt()
				_, _ = rp.Read(16)
				typ, _ := rp.VarInt()
				if typ == allayType && allayID == 0 {
					allayID = id
					var ib protocol.Buffer
					ib.VarInt(id)
					ib.VarInt(0) // interact
					ib.VarInt(0) // main hand
					ib.Bool(false)
					_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
				}
				if typ == itemType && fed {
					if pendingCakes > 0 {
						pendingCakes--
						spawnedCakes[id] = true // our re-dropped ground cake
					} else {
						// An item entity we did not summon is the allay's
						// throw landing on the noteblock.
						r.thrown = true
					}
				}
			case server.IDPlayRemoveEntity:
				rp := protocol.NewPacketReader(p.Data)
				for rp.Remaining() > 0 {
					id, e := rp.VarInt()
					if e != nil {
						break
					}
					if spawnedCakes[id] {
						delete(spawnedCakes, id)
						r.picked = true
					}
				}
			case server.IDPlaySetEquipment:
				// The cake landing on the allay's hand means the
				// feed ran; the hand is empty now, so play the
				// noteblock before the allay can commit to the
				// player deposit target.
				if !fed {
					fed = true
					var ub2 protocol.Buffer
					ub2.VarInt(0)
					ub2.Position(8, -60, 8)
					ub2.VarInt(1)
					ub2.Float(0.5)
					ub2.Float(0.0)
					ub2.Float(0.5)
					ub2.Bool(false)
					ub2.Bool(false)
					ub2.VarInt(2)
					_ = pw.WritePacket(server.IDPlayUseItemOnSB, ub2.Bytes())
				}
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			}
			if r.thrown && f.prog["minecraft:husbandry/allay_deliver_cake_to_note_block"]["allay_deliver_cake_to_note_block"] {
				break
			}
		}
		return r
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	if !want.picked {
		t.Fatalf("vanilla: allay never picked up the cake")
	}
	if !want.thrown {
		t.Fatalf("vanilla: allay never threw the cake")
	}
	if !want.f.prog["minecraft:husbandry/allay_deliver_cake_to_note_block"]["allay_deliver_cake_to_note_block"] {
		t.Fatalf("vanilla: husbandry/allay_deliver_cake_to_note_block not granted")
	}
	diffAdvFlush(want.f, got.f)
	if got.thrown != want.thrown {
		t.Errorf("mc-go allay threw = %v, vanilla %v", got.thrown, want.thrown)
	}
}
