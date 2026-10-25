package blackbox

// TestPigBoostParity pins ItemBasedSteering's boost on a saddled pig,
// measured from 26.2's Pig + FoodOnAStickItem + ItemBasedSteering:
//
//   - DATA_BOOST_TIME (idx18, int) — a use of carrot_on_a_stick while
//     controlling the pig rolls it to rand(841)+140 exactly once
//     (boost() latches boosting until tickBoost counts past the
//     total; a second use while boosting falls through to PASS and
//     never re-emits or re-wears).
//   - The stick takes consumeItemDamage=7 via hurtAndConvertOnBreak:
//     a stick at damage 20+7 >= 25 transmutes into a fresh
//     fishing_rod instead of breaking — visible on the hotbar slot
//     resync.
//
// Mount is the entity Interact on the saddled pig (empty hand), then
// a /give for the stick, then a use_item against open air.

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// boostEmits scans 0x63 set_entity_data lines for the pig's idx18
// int entry, returning the emit count and the last value.
func boostEmits(lines []string, pigID int32) (count int, val int32) {
	for _, l := range lines {
		i := strings.IndexByte(l, ':')
		if i < 0 || l[:i] != "0x63" {
			continue
		}
		r := protocol.NewPacketReader([]byte(l[i+1:]))
		id, e := r.VarInt()
		if e != nil || id != pigID {
			continue
		}
		for {
			idx, err := r.Byte()
			if err != nil || idx == 0xFF {
				break
			}
			ser, err2 := r.Byte()
			if err2 != nil {
				break
			}
			if idx == 18 && ser == 1 {
				v, _ := r.VarInt()
				count++
				val = v
				continue
			}
			// Holder/variant serializers (21+) read a VarInt; the
			// scalar set rides skipMetaVal.
			if ser >= 21 {
				if _, err := r.VarInt(); err != nil {
					break
				}
				continue
			}
			if !skipMetaVal(r, ser) {
				break
			}
		}
	}
	return
}

func TestPigBoostParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Boost"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) (emits int, boostVal int32, rodSwap bool) {
		c, pr, pw := joinPlayBlackbox(t, addr, "Boost")
		defer c.Close()
		sink := newChatSink(pr, pw)
		sendClientInformationVD(t, pw, 2)
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		time.Sleep(500 * time.Millisecond)
		sendCommand(t, pw, "gamemode survival Boost")
		sendCommand(t, pw, "tp @s 8.5 -60 8.5 0 0")
		sendCommand(t, pw, `summon minecraft:pig 8.5 -60 10.5 {equipment:{saddle:{id:"minecraft:saddle",count:1}},NoAI:1b}`)

		// Capture the pig's entity id from its spawn packet.
		var pigID int32
		end := time.Now().Add(8 * time.Second)
		for time.Now().Before(end) && pigID == 0 {
			for _, l := range sink.take() {
				i := strings.IndexByte(l, ':')
				if i < 0 {
					continue
				}
				if l[:i] != "0x1" {
					continue
				}
				r := protocol.NewPacketReader([]byte(l[i+1:]))
				id, e1 := r.VarInt()
				if e1 != nil {
					continue
				}
				if _, e2 := r.Read(16); e2 != nil {
					continue
				}
				typ, e3 := r.VarInt()
				if e3 == nil && typ == 100 {
					pigID = id
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if pigID == 0 {
			t.Fatalf("%s: no pig spawn packet", addr)
		}
		// Mount empty-handed, then take the stick and boost.
		var ib protocol.Buffer
		ib.VarInt(pigID)
		ib.VarInt(0) // interact
		ib.VarInt(0) // main hand
		ib.Bool(false)
		_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		time.Sleep(600 * time.Millisecond)
		sendCommand(t, pw, "give @s minecraft:carrot_on_a_stick[minecraft:damage=20]")
		time.Sleep(600 * time.Millisecond)
		sink.take() // drain give/mount noise

		use := func() {
			var ub protocol.Buffer
			ub.VarInt(0) // hand
			ub.VarInt(0) // sequence
			ub.Float(0)  // yaw
			ub.Float(0)  // pitch
			_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		}
		use()
		// Wait for the single boost emit, then reuse inside the same
		// boost window and confirm nothing new lands.
		lines := []string{}
		end = time.Now().Add(5 * time.Second)
		for time.Now().Before(end) {
			lines = append(lines, sink.take()...)
			if c, _ := boostEmits(lines, pigID); c > 0 {
				break
			}
			time.Sleep(80 * time.Millisecond)
		}
		emits, boostVal = boostEmits(lines, pigID)
		use()
		time.Sleep(1200 * time.Millisecond)
		lines = append(lines, sink.take()...)
		emits, boostVal = boostEmits(lines, pigID)

		// The 20+7 wear crossed 25 — the transform writes back a fresh
		// fishing_rod, which vanilla resyncs through set_content
		// (0x12: window, state, count, count×slot stacks, carried).
		rod, _ := world.ItemID("fishing_rod")
		for _, l := range lines {
			i := strings.IndexByte(l, ':')
			if i < 0 || (l[:i] != "0x12" && l[:i] != "0x14") {
				continue
			}
			r := protocol.NewPacketReader([]byte(l[i+1:]))
			_, _ = r.VarInt() // window
			_, _ = r.VarInt() // state
			if l[:i] == "0x12" {
				n, err := r.VarInt()
				if err != nil || n < 0 || n > 128 {
					continue
				}
				// Slots lead count-then-id; only slot 36 matters and
				// it is always the first (only) non-empty slot —
				// earlier slots read count=0 with no id. Bail at 36:
				// component payloads can't be skipped untyped.
				for sl := int32(0); sl < n && sl <= 36; sl++ {
					cnt, _ := r.VarInt()
					if cnt <= 0 {
						continue
					}
					id, _ := r.VarInt()
					if sl == 36 && id == rod {
						rodSwap = true
					}
					break // a non-empty slot's component tail isn't skippable
				}
				continue
			}
			slot, err := r.Short()
			if err != nil || slot != 36 {
				continue
			}
			cnt, _ := r.VarInt()
			id, _ := r.VarInt()
			if cnt == 1 && id == rod {
				rodSwap = true
			}
		}
		return emits, boostVal, rodSwap
	}

	ve, vv, vr := run(targets.vanilla)
	me, mv, mr := run(targets.mcgo)
	if ve != 1 || vv < 140 || vv > 980 {
		t.Fatalf("vanilla: emits=%d boost=%v, want 1 emit in [140,980]", ve, vv)
	}
	if !vr {
		t.Fatalf("vanilla: no fishing_rod slot resync")
	}
	if me != ve || (mv < 140 || mv > 980) {
		t.Fatalf("mcgo: emits=%d boost=%v, want %d emit in [140,980]", me, mv, ve)
	}
	if mr != vr {
		t.Fatalf("mcgo: rodSwap=%v, want %v", mr, vr)
	}
}

// TestStriderParity pins Strider's metadata + tick suffocating model,
// measured from 26.2's Strider (javap):
//
//   - DATA_SUFFOCATING (idx19, boolean): set per tick under !isNoAi —
//     warm = STRIDER_WARM_BLOCKS ({lava}) at the strider's own or
//     on-block, lava fluid height > 0, or riding a non-suffocating
//     strider; suffocating = !warm. A strider summoned on land flips
//     to true on its first tick.
//   - setSuffocating also flips the transient "suffocating"
//     movement_speed modifier (-0.34 add_multiplied_base), which
//     re-arms the update_attributes resend — the modifier id is
//     literal in the packet bytes ("suffocating" = 737566666f636174696e67).
//   - DATA_BOOST_TIME (idx18) carries ItemBasedSteering the same as
//     the pig: warped_fungus_on_a_stick (vehicle strider, damage 1,
//     durability 100) boosts without breaking.
func TestStriderParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Boost"),
	}, nil)
	defer targets.cleanup()

	suffocHex := "737566666f636174696e67" // "suffocating"

	// metaBoolEmits counts idx:ser8 entries on eid's set_entity_data
	// lines and returns the count + last bool value.
	metaBoolEmits := func(lines []string, eid int32, idx int) (count int, last bool) {
		for _, l := range lines {
			i := strings.IndexByte(l, ':')
			if i < 0 || l[:i] != "0x63" {
				continue
			}
			r := protocol.NewPacketReader([]byte(l[i+1:]))
			id, e := r.VarInt()
			if e != nil || id != eid {
				continue
			}
			for {
				mi, err := r.Byte()
				if err != nil || mi == 0xFF {
					break
				}
				ser, err2 := r.Byte()
				if err2 != nil {
					break
				}
				if mi == byte(idx) && ser == 8 {
					v, _ := r.Byte()
					count++
					last = v != 0
					continue
				}
				if ser >= 21 {
					if _, err := r.VarInt(); err != nil {
						break
					}
					continue
				}
				if !skipMetaVal(r, ser) {
					break
				}
			}
		}
		return
	}

	run := func(addr string) (suffocCount int, suffocLast bool, attrEmit bool, bEmits int) {
		c, pr, pw := joinPlayBlackbox(t, addr, "Boost")
		defer c.Close()
		sink := newChatSink(pr, pw)
		sendClientInformationVD(t, pw, 2)
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		time.Sleep(500 * time.Millisecond)
		sendCommand(t, pw, "gamemode survival Boost")
		sendCommand(t, pw, "tp @s 8.5 -60 8.5 0 0")
		sendCommand(t, pw, `summon minecraft:strider 8.5 -60 10.5 {equipment:{saddle:{id:"minecraft:saddle",count:1}}}`)

		// Capture the strider's entity id (type wire id 129).
		var sid int32
		end := time.Now().Add(8 * time.Second)
		for time.Now().Before(end) && sid == 0 {
			for _, l := range sink.take() {
				i := strings.IndexByte(l, ':')
				if i < 0 || l[:i] != "0x1" {
					continue
				}
				r := protocol.NewPacketReader([]byte(l[i+1:]))
				id, e1 := r.VarInt()
				if e1 != nil {
					continue
				}
				if _, e2 := r.Read(16); e2 != nil {
					continue
				}
				typ, e3 := r.VarInt()
				if e3 == nil && typ == 129 {
					sid = id
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if sid == 0 {
			t.Fatalf("%s: no strider spawn packet", addr)
		}

		// Out of lava → suffocating flips true on the first tick.
		lines := []string{}
		end = time.Now().Add(5 * time.Second)
		for time.Now().Before(end) {
			lines = append(lines, sink.take()...)
			if c, _ := metaBoolEmits(lines, sid, 19); c > 0 {
				break
			}
			time.Sleep(80 * time.Millisecond)
		}
		suffocCount, suffocLast = metaBoolEmits(lines, sid, 19)
		for _, l := range lines {
			if strings.Contains(l, suffocHex) {
				attrEmit = true
			}
		}

		// Lava under the strider → warm → suffocating back to false.
		sendCommand(t, pw, `execute at @e[type=minecraft:strider,limit=1] run setblock ~ ~-1 ~ minecraft:lava`)
		end = time.Now().Add(5 * time.Second)
		for time.Now().Before(end) {
			lines = append(lines, sink.take()...)
			if c, _ := metaBoolEmits(lines, sid, 19); c >= 2 {
				break
			}
			time.Sleep(80 * time.Millisecond)
		}
		suffocCount, suffocLast = metaBoolEmits(lines, sid, 19)

		// Mount + fungus stick → the same single idx18 boost emit.
		var ib protocol.Buffer
		ib.VarInt(sid)
		ib.VarInt(0)
		ib.VarInt(0)
		ib.Bool(false)
		_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		time.Sleep(600 * time.Millisecond)
		sendCommand(t, pw, "give @s minecraft:warped_fungus_on_a_stick")
		time.Sleep(600 * time.Millisecond)
		sink.take()
		var ub protocol.Buffer
		ub.VarInt(0)
		ub.VarInt(0)
		ub.Float(0)
		ub.Float(0)
		_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		end = time.Now().Add(5 * time.Second)
		for time.Now().Before(end) {
			lines = append(lines, sink.take()...)
			if c, _ := boostEmits(lines, sid); c > 0 {
				break
			}
			time.Sleep(80 * time.Millisecond)
		}
		bEmits, _ = boostEmits(lines, sid)
		return
	}

	vc, vl, va, vb := run(targets.vanilla)
	mc, ml, ma, mb := run(targets.mcgo)
	if vc < 2 || vl != false {
		t.Fatalf("vanilla: suffocating emits=%d last=%v, want >=2 ending false", vc, vl)
	}
	if vb != 1 {
		t.Fatalf("vanilla: boost emits=%d, want 1", vb)
	}
	if mc != vc || ml != vl {
		t.Fatalf("mcgo: suffocating emits=%d last=%v, want %d ending %v", mc, ml, vc, vl)
	}
	if mb != vb {
		t.Fatalf("mcgo: boost emits=%d, want %d", mb, vb)
	}
	if ma != va {
		t.Fatalf("mcgo: suffocating attr emit=%v, want %v", ma, va)
	}
}
