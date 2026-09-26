package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestCrafterRecipeCraftedParity: a crafter holding a valid recipe
// craft-loads 4 ticks after a neighboring power source appears, ejects
// the result as an item entity toward its front face, and fires
// crafter_recipe_crafted for every player within 8.5 blocks —
// adventure/crafters_crafting_crafters pins recipe minecraft:crafter.
func TestCrafterRecipeCraftedParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("CraftP"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type result struct {
		f         *advFlush
		ejected   bool
		failEvent bool // level event 1050 = craft attempt with no match
	}

	run := func(addr string) result {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "CraftP")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 8.5 -60 12.5 0 0")
		sendCommand(t, pw, "setblock 8 -60 8 minecraft:crafter")
		// minecraft:crafter recipe: III / ICI / RDR
		grid := []string{
			"iron_ingot", "iron_ingot", "iron_ingot",
			"iron_ingot", "crafting_table", "iron_ingot",
			"redstone", "dropper", "redstone",
		}
		for i, it := range grid {
			sendCommand(t, pw, "item replace block 8 -60 8 container."+itoa(int32(i))+" with minecraft:"+it)
		}
		advCollect(t, c, pr, pw, 600*time.Millisecond, f)
		sendCommand(t, pw, "setblock 8 -61 8 minecraft:redstone_block")

		r := result{f: f}
		itemType, _ := server.EntityTypeID("item")
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySpawnEntity:
				rp := protocol.NewPacketReader(p.Data)
				_, e1 := rp.VarInt()
				_, e2 := rp.Read(16)
				typ, e3 := rp.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == itemType {
					r.ejected = true
				}
			case server.IDPlayLevelEvent:
				rp := protocol.NewPacketReader(p.Data)
				ev, _ := rp.Int()
				if ev == 1050 {
					r.failEvent = true
				}
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			}
			if r.ejected && f.prog["minecraft:adventure/crafters_crafting_crafters"]["crafter_crafted_crafter"] {
				break
			}
		}
		return r
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	if !want.ejected {
		t.Fatalf("vanilla: crafter never ejected the result")
	}
	if want.failEvent {
		t.Fatalf("vanilla: crafter failed to match the recipe (1050)")
	}
	if !want.f.prog["minecraft:adventure/crafters_crafting_crafters"]["crafter_crafted_crafter"] {
		t.Fatalf("vanilla: adventure/crafters_crafting_crafters not granted")
	}
	diffAdvFlush(want.f, got.f)
	if got.ejected != want.ejected {
		t.Errorf("mc-go ejected item = %v, vanilla %v", got.ejected, want.ejected)
	}
	if got.failEvent {
		t.Errorf("mc-go crafter reported craft failure (1050)")
	}
}

// TestCrafterRemainderParity: getRemainingItems runs per craft — a
// honey_block craft (4 honey bottles, shaped 2x2) ejects the result
// plus four glass_bottle remainders, each through dispenseItem's
// full tail (entity throw + level events 1049/2010). Pins the
// five-item spawn count and per-dispense level events.
func TestCrafterRemainderParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("CraftR"),
	}, nil)
	defer targets.cleanup()

	type result struct {
		spawns int
		ev1049 int
		ev2010 int
		ev1050 int
	}

	run := func(addr string) result {
		c, pr, pw := joinPlayBlackbox(t, addr, "CraftR")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)

		sendCommand(t, pw, "tp @s 8.5 -60 12.5 0 0")
		sendCommand(t, pw, "setblock 8 -60 8 minecraft:crafter")
		// honey_block: "##"/"##" honey bottles -> block + 4 bottles.
		for _, i := range []int{0, 1, 3, 4} {
			sendCommand(t, pw, "item replace block 8 -60 8 container."+itoa(int32(i))+" with minecraft:honey_bottle")
		}
		sendCommand(t, pw, "setblock 8 -61 8 minecraft:redstone_block")

		var r result
		itemType, _ := server.EntityTypeID("item")
		end := time.Now().Add(8 * time.Second)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySpawnEntity:
				rp := protocol.NewPacketReader(p.Data)
				_, e1 := rp.VarInt()
				_, e2 := rp.Read(16)
				typ, e3 := rp.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == itemType {
					r.spawns++
				}
			case server.IDPlayLevelEvent:
				rp := protocol.NewPacketReader(p.Data)
				ev, _ := rp.Int()
				px, _ := rp.Float()
				py, _ := rp.Float()
				pz, _ := rp.Float()
				if int(px) != 8 || int(py) != -60 || int(pz) != 8 {
					continue
				}
				switch ev {
				case 1049:
					r.ev1049++
				case 2010:
					r.ev2010++
				case 1050:
					r.ev1050++
				}
			}
		}
		return r
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	if want.spawns == 0 {
		t.Fatalf("vanilla: crafter never ejected any items")
	}
	if want != got {
		t.Errorf("crafter remainder mismatch: vanilla %+v, mc-go %+v", want, got)
	}
}
