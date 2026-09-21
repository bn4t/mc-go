package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// lpVec3 encodes a compact LpVec3 for the interact packet's click
// position: the smallest 2-bit scale that keeps every coordinate in
// [-1, 1), three 15-bit fields packed into a 48-bit word (x at bit 3,
// y at bit 18, z at bit 33), low bits of the first byte carrying the
// scale. Values beyond scale 3 (|c| > 3) need a continuation VarInt —
// armor-stand click heights never do.
func lpVec3(x, y, z float64) []byte {
	scale := 1.0
	for scale < 3 && (math.Abs(x) >= scale || math.Abs(y) >= scale || math.Abs(z) >= scale) {
		scale++
	}
	pack := func(d float64) int64 {
		q := int64(math.Round((d/scale + 1) * 0.5 * 32766))
		if q < 0 {
			q = 0
		}
		if q > 32766 {
			q = 32766
		}
		return q
	}
	v := pack(x)<<3 | pack(y)<<18 | pack(z)<<33 | int64(scale)&3
	return []byte{byte(v), byte(v >> 8), byte(v >> 40), byte(v >> 32), byte(v >> 24), byte(v >> 16)}
}

// TestArmorStandInteractParity: ArmorStand.interact_at — a held
// equippable stack targets its own slot regardless of click height;
// an empty hand picks the slot by click height (feet < 0.55, legs
// 0.4-1.2, chest 0.9-1.6, head 1.6+); and swapItem moves the whole
// stack in, and the old piece out to the hand. DisabledSlots and the
// armless-stand hand gate apply in both directions.
func TestArmorStandInteractParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("StandP"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type equipEntry struct {
		slot int32
		item int32
		cnt  int32
	}
	type result struct {
		f       *advFlush
		equips  []equipEntry // equipment updates for the stand, in order
		handLog []int32      // hotbar-slot-0 updates (item id or 0/air)
		swings  int          // arm-swing animations on the player
	}

	run := func(addr string) result {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "StandP")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)

		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 8.5 -60 12.5 0 0")
		sendCommand(t, pw, `summon minecraft:armor_stand 8.5 -60 10.5 {NoAI:1b,ShowArms:1b,equipment:{feet:{id:"minecraft:iron_boots",count:1b},head:{id:"minecraft:iron_helmet",count:1b}}}`)
		sendCommand(t, pw, "give @s minecraft:diamond_chestplate")

		interact := func(zid int32, y float64) {
			var ib protocol.Buffer
			ib.VarInt(zid)
			ib.VarInt(0) // hand: main
			ib.Raw(lpVec3(0.4, y, 0.4))
			ib.Bool(false)
			_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		}

		r := result{f: f}
		asType, _ := server.EntityTypeID("armor_stand")
		airID := int32(-1)
		var zid int32
		step := 0
		var stepAt time.Time
		start := time.Now()
		advance := func() {
			step++
			stepAt = time.Now()
		}
		stepAt = start
		end := start.Add(14 * time.Second)
		for time.Now().Before(end) {
			if zid != 0 && time.Since(stepAt) > 450*time.Millisecond {
				switch step {
				case 0: // put the diamond_chestplate on (equippable -> chest)
					interact(zid, 1.3)
					advance()
				case 1: // empty hand, click the feet band -> boots off
					interact(zid, 0.2)
					advance()
				case 2: // boots in hand -> equippable -> back onto feet
					interact(zid, 1.7)
					advance()
				case 3: // empty hand, click the head band -> helmet off
					interact(zid, 1.7)
					advance()
				default:
					advance()
				}
			}
			if step > 4 && time.Since(stepAt) > 600*time.Millisecond {
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
				if typ == asType && zid == 0 {
					zid = id
				}
			case server.IDPlaySetEquipment:
				rp := protocol.NewPacketReader(p.Data)
				eid, _ := rp.VarInt()
				for rp.Remaining() > 0 {
					slot, err := rp.Byte()
					if err != nil {
						break
					}
					last := slot&0x80 == 0
					slot &= 0x7F
					st, _ := readSlotBB(rp)
					if eid == zid {
						iid := airID
						if !st.Empty() {
							iid = st.ID
						}
						r.equips = append(r.equips, equipEntry{int32(slot), iid, st.Count})
					}
					if last {
						break
					}
				}
			case server.IDPlayContainerSlot:
				rp := protocol.NewPacketReader(p.Data)
				wid, _ := rp.VarInt()
				_, _ = rp.VarInt()
				slot, _ := rp.Short()
				it, _ := readSlotBB(rp)
				if wid == 0 && slot == 36 {
					iid := airID
					if !it.Empty() {
						iid = it.ID
					}
					r.handLog = append(r.handLog, iid)
				}
			case server.IDPlaySetPlayerInventory:
				rp := protocol.NewPacketReader(p.Data)
				slot, _ := rp.VarInt()
				it, _ := readSlotBB(rp)
				if slot == 0 {
					iid := airID
					if !it.Empty() {
						iid = it.ID
					}
					r.handLog = append(r.handLog, iid)
				}
			case server.IDPlayEntityAnimation:
				rp := protocol.NewPacketReader(p.Data)
				eid, _ := rp.VarInt()
				if eid != zid {
					r.swings++
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

	bootsID, _ := world.ItemID("iron_boots")
	helmetID, _ := world.ItemID("iron_helmet")
	dChestID, _ := world.ItemID("diamond_chestplate")

	// Vanilla must show the full swap chain before comparing streams.
	var chestPut, feetTake, feetPut, headTake bool
	for _, e := range want.equips {
		switch {
		case e.slot == 4 && e.item == dChestID:
			chestPut = true
		case e.slot == 2 && e.item == -1:
			feetTake = true
		case e.slot == 2 && e.item == bootsID:
			feetPut = true
		case e.slot == 5 && e.item == -1:
			headTake = true
		}
	}
	if !chestPut || !feetTake || !feetPut || !headTake {
		t.Fatalf("vanilla: incomplete swap chain (chest=%v feetOff=%v feetOn=%v head=%v) equips=%v",
			chestPut, feetTake, feetPut, headTake, want.equips)
	}
	_ = helmetID

	if len(got.equips) != len(want.equips) {
		t.Errorf("mc-go equipment packets %d, vanilla %d\nmc-go=%v\nvanilla=%v", len(got.equips), len(want.equips), got.equips, want.equips)
	}
	for i := 0; i < len(got.equips) && i < len(want.equips); i++ {
		if got.equips[i] != want.equips[i] {
			t.Errorf("equips[%d]: mc-go %+v, vanilla %+v", i, got.equips[i], want.equips[i])
		}
	}
	if len(got.handLog) != len(want.handLog) {
		t.Errorf("mc-go hand updates %d, vanilla %d (%v vs %v)", len(got.handLog), len(want.handLog), got.handLog, want.handLog)
		t.Logf("want.equips=%v\ngot.equips=%v", want.equips, got.equips)
	} else {
		for i := range got.handLog {
			if got.handLog[i] != want.handLog[i] {
				t.Errorf("handLog[%d]: mc-go %d, vanilla %d", i, got.handLog[i], want.handLog[i])
			}
		}
	}
	diffAdvFlush(want.f, got.f)
}
