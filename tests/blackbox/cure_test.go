package blackbox

import (
	"fmt"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// TestCuredZombieVillagerParity: a zombie_villager summoned with
// ConversionTime + ConversionPlayer NBT (vanilla's save fields for an
// in-progress cure) counts down, is replaced by a villager, and grants
// the curing player minecraft:story/cure_zombie_villager.
func TestCuredZombieVillagerParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("CureP"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type result struct {
		f          *advFlush
		zombieGone bool
		villSeen   bool
	}

	run := func(addr string) result {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "CureP")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "gamemode survival @s")
		u := server.OfflineUUID("CureP")
		sendCommand(t, pw, fmt.Sprintf(`summon minecraft:zombie_villager 8.5 -60 8.5 {NoAI:1b,ConversionTime:5,ConversionPlayer:[I;%d,%d,%d,%d]}`,
			int32(u[0])<<24|int32(u[1])<<16|int32(u[2])<<8|int32(u[3]),
			int32(u[4])<<24|int32(u[5])<<16|int32(u[6])<<8|int32(u[7]),
			int32(u[8])<<24|int32(u[9])<<16|int32(u[10])<<8|int32(u[11]),
			int32(u[12])<<24|int32(u[13])<<16|int32(u[14])<<8|int32(u[15])))

		r := result{f: f}
		var zid int32
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
				id, e1 := rp.VarInt()
				_, e2 := rp.Read(16)
				typ, e3 := rp.VarInt()
				if e1 == nil && e2 == nil && e3 == nil {
					if typ == 154 {
						zid = id
					}
					if typ == 140 && zid != 0 {
						r.villSeen = true
					}
				}
			case server.IDPlayRemoveEntity:
				rp := protocol.NewPacketReader(p.Data)
				n, _ := rp.VarInt()
				for i := int32(0); i < n; i++ {
					id, _ := rp.VarInt()
					if id == zid && zid != 0 {
						r.zombieGone = true
					}
				}
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			}
			if r.zombieGone && r.villSeen && f.prog["minecraft:story/cure_zombie_villager"]["cured_zombie"] {
				break
			}
		}
		return r
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	if !want.zombieGone || !want.villSeen {
		t.Fatalf("vanilla: cure did not swap entities (zombieGone=%v villSeen=%v)", want.zombieGone, want.villSeen)
	}
	if !want.f.prog["minecraft:story/cure_zombie_villager"]["cured_zombie"] {
		t.Fatalf("vanilla: story/cure_zombie_villager not granted")
	}
	diffAdvFlush(want.f, got.f)
	if got.zombieGone != want.zombieGone {
		t.Errorf("mc-go zombie removed = %v, vanilla %v", got.zombieGone, want.zombieGone)
	}
	if got.villSeen != want.villSeen {
		t.Errorf("mc-go villager spawned = %v, vanilla %v", got.villSeen, want.villSeen)
	}
}

// TestCureInteractParity: ZombieVillager.mobInteract — a golden apple
// on a zombie villager carrying weakness consumes one apple, starts
// the conversion timer, clears the weakness, and broadcasts entity
// event 16 (the cure sound cue). A second apple lands on CONSUME —
// weakness is already gone, so nothing is eaten or replayed.
func TestCureInteractParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("CureInt"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type result struct {
		f           *advFlush
		cureEvent   bool // entity event 16 on the zombie villager
		appleGone   bool // the held hotbar slot emptied after interact
		secondQuiet bool // second apple interact: no event, no consume
	}

	run := func(addr string) result {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "CureInt")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 8.5 -60 12.5 0 0")
		sendCommand(t, pw, `summon minecraft:zombie_villager 8.5 -60 10.5 {NoAI:1b,active_effects:[{id:"minecraft:weakness",duration:-1,amplifier:0b,ambient:0b,show_particles:0b}]}`)
		sendCommand(t, pw, "give @s minecraft:golden_apple 2")

		r := result{f: f}
		zvType, _ := server.EntityTypeID("zombie_villager")
		appleID, _ := world.ItemID("golden_apple")
		var zid int32
		interact := func() {
			var ib protocol.Buffer
			ib.VarInt(zid)
			ib.VarInt(0) // interact
			ib.VarInt(0) // main hand
			ib.Bool(false)
			_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		}
		start := time.Now()
		interacted := false // sent the first apple interact
		secondSent := false
		var secondAt time.Time
		secondEvent := false
		secondEmpty := false
		end := start.Add(12 * time.Second)
		for time.Now().Before(end) {
			if zid != 0 && !interacted && time.Since(start) > 700*time.Millisecond {
				interact()
				interacted = true
			}
			if r.cureEvent && r.appleGone && !secondSent {
				secondSent = true
				secondAt = time.Now()
				interact()
			}
			// 700ms of silence after the second interact proves the
			// vanilla CONSUME no-op.
			if secondSent && !secondEvent && !secondEmpty && time.Since(secondAt) > 700*time.Millisecond {
				r.secondQuiet = true
				break
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
				if typ == zvType && zid == 0 {
					zid = id
				}
			case server.IDPlayEntityEvent:
				rp := protocol.NewPacketReader(p.Data)
				id, _ := rp.Int()
				ev, _ := rp.Byte()
				if id == zid && ev == 16 {
					if !secondSent {
						r.cureEvent = true
					} else {
						secondEvent = true
					}
				}
			case server.IDPlayContainerSlot:
				rp := protocol.NewPacketReader(p.Data)
				wid, _ := rp.VarInt()
				_, _ = rp.VarInt() // state id
				slot, _ := rp.Short()
				it, _ := readSlotBB(rp)
				// apple count 2 -> 1 on the first cure; a second
				// drop would mean the CONSUME no-op ate another.
				if wid == 0 && slot == 36 && !it.Empty() && it.Count == 1 && it.ID == appleID {
					if !secondSent {
						r.appleGone = true
					} else {
						secondEmpty = true
					}
				}
				if wid == 0 && slot == 36 && it.Empty() && secondSent {
					secondEmpty = true
				}
			case server.IDPlaySetPlayerInventory:
				rp := protocol.NewPacketReader(p.Data)
				slot, _ := rp.VarInt()
				it, _ := readSlotBB(rp)
				if slot == 0 && !it.Empty() && it.Count == 1 && it.ID == appleID {
					if !secondSent {
						r.appleGone = true
					} else {
						secondEmpty = true
					}
				}
				if slot == 0 && it.Empty() && secondSent {
					secondEmpty = true
				}
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			}
		}
		return r
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	if !want.cureEvent || !want.appleGone {
		t.Fatalf("vanilla: cure interact did not fire (event=%v appleGone=%v)", want.cureEvent, want.appleGone)
	}
	diffAdvFlush(want.f, got.f)
	if got.cureEvent != want.cureEvent {
		t.Errorf("mc-go cure event = %v, vanilla %v", got.cureEvent, want.cureEvent)
	}
	if got.appleGone != want.appleGone {
		t.Errorf("mc-go apple consumed = %v, vanilla %v", got.appleGone, want.appleGone)
	}
	if got.secondQuiet != want.secondQuiet {
		t.Errorf("mc-go second interact quiet = %v, vanilla %v", got.secondQuiet, want.secondQuiet)
	}
}
