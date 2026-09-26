package blackbox

import (
	"fmt"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestCrafterWireParity: the crafter's broadcast layer end-to-end —
// the slot-enable + TRIGGERED containerData props on open, the prop
// packet a slot toggle pushes to its viewers, the TRIGGERED/CRAFTING
// blockstate updates on arm/craft/release, the silent full-insert into
// a front container, and the 1050 click on a failed craft.
func TestCrafterWireParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("CraftW"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	type lev struct{ id, data int32 }
	type phase struct {
		bu    []int32 // blockstate ids written at the crafter pos, in order
		ev    []lev   // level events at the crafter pos
		spawn bool    // an item entity spawned during the phase
	}
	type wire struct {
		win      int32
		menu     int32
		props    map[int16]int16
		phases   map[string]*phase
		prop2Seq []int16 // prop-2 values as they arrived (toggle trace)
	}

	cpos := [3]int32{8, -60, 8}
	itemType, _ := server.EntityTypeID("item")

	run := func(addr string) *wire {
		out := &wire{props: map[int16]int16{}, phases: map[string]*phase{}}
		cur := "join"
		out.phases[cur] = &phase{}
		fold := func(pkts []usePkt) {
			ph := out.phases[cur]
			for _, p := range pkts {
				r := protocol.NewPacketReader(p.data)
				switch p.id {
				case server.IDPlayOpenScreen:
					wv, e1 := r.VarInt()
					menu, e2 := r.VarInt()
					if e1 == nil && e2 == nil {
						out.win = wv
						out.menu = menu
					}
				case server.IDPlayContainerProperty:
					wv, e0 := r.VarInt()
					prop, e1 := r.Short()
					val, e2 := r.Short()
					if e0 != nil || e1 != nil || e2 != nil || wv != out.win {
						continue
					}
					out.props[prop] = val
					if prop == 2 {
						out.prop2Seq = append(out.prop2Seq, val)
					}
				case server.IDPlayBlockUpdateCB:
					x, y, z, e1 := r.Position()
					st, e2 := r.VarInt()
					if e1 == nil && e2 == nil && [3]int32{x, y, z} == cpos {
						ph.bu = append(ph.bu, st)
					}
				case server.IDPlayLevelEvent:
					id, e1 := r.Int()
					x, y, z, e2 := r.Position()
					data, e3 := r.Int()
					if e1 == nil && e2 == nil && e3 == nil && [3]int32{x, y, z} == cpos {
						ph.ev = append(ph.ev, lev{id, data})
					}
				case server.IDPlaySpawnEntity:
					if _, e := r.VarInt(); e != nil {
						continue
					}
					if _, e := r.Read(16); e != nil {
						continue
					}
					if typ, e := r.VarInt(); e == nil && typ == itemType {
						ph.spawn = true
					}
				}
			}
		}
		sink, pw := useCellSetup(t, addr, "CraftW", true, false)
		step := func(name string, do func()) {
			cur = name
			out.phases[name] = &phase{}
			if do != nil {
				do()
			}
			settleUse(t, sink, fold, name)
		}
		step("setup", func() {
			sendCommand(t, pw, "tp @s 8.5 -60 12.5 0 0")
			sendCommand(t, pw, "setblock 8 -60 8 minecraft:crafter[orientation=north_up]")
			sendCommand(t, pw, "say SETUP")
		})

		step("open", func() {
			sendUseItemOn(t, pw, 8, -60, 8, 1, 1)
		})
		if out.win == 0 {
			t.Fatalf("%s: crafter menu never opened", addr)
		}

		slotState := func(slot int32, enabled bool) func() {
			return func() {
				var b protocol.Buffer
				b.VarInt(slot)
				b.VarInt(out.win)
				b.Bool(enabled)
				_ = pw.WritePacket(server.IDPlayContainerSlotStateChangedSB, b.Bytes())
			}
		}
		step("toggleOff", slotState(2, false))
		step("toggleOn", slotState(2, true))
		step("close", func() {
			var b protocol.Buffer
			b.VarInt(out.win)
			_ = pw.WritePacket(server.IDPlayCloseContainerSB, b.Bytes())
		})

		fill := func() {
			// minecraft:crafter recipe — III / ICI / RDR.
			grid := []string{
				"iron_ingot", "iron_ingot", "iron_ingot",
				"iron_ingot", "crafting_table", "iron_ingot",
				"redstone", "dropper", "redstone",
			}
			for i, it := range grid {
				sendCommand(t, pw, fmt.Sprintf("item replace block 8 -60 8 container.%d with minecraft:%s", i, it))
			}
		}

		step("fill", func() {
			fill()
			sendCommand(t, pw, "say FILL")
		})
		// Arm: redstone below the crafter. The arm write latches
		// TRIGGERED (flag 2), the craft at +4t sets CRAFTING=true (flag
		// 2) and the clear at +6t writes CRAFTING=false (flag 3) — the
		// 800ms settle quiet window covers the ~10-tick sequence.
		step("arm", func() {
			sendCommand(t, pw, "setblock 8 -61 8 minecraft:redstone_block")
			sendCommand(t, pw, "say ARM")
		})
		step("off", func() {
			sendCommand(t, pw, "setblock 8 -61 8 minecraft:air")
			sendCommand(t, pw, "say OFF")
		})
		// A chest at the crafter's north face eats the whole result:
		// vanilla's dispenseItem early-outs on a full insert, so no
		// spawn, no 1049/2010.
		step("insert", func() {
			sendCommand(t, pw, "setblock 8 -60 7 minecraft:chest")
			fill()
			sendCommand(t, pw, "setblock 8 -61 8 minecraft:redstone_block")
			sendCommand(t, pw, "say INS")
		})
		// Empty grid on a fresh rising edge: arm BU + the 1050 click,
		// and nothing else.
		step("fail", func() {
			sendCommand(t, pw, "setblock 8 -61 8 minecraft:air")
			sendCommand(t, pw, "setblock 8 -61 8 minecraft:redstone_block")
			sendCommand(t, pw, "say FAIL")
		})
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	check := func(name string, a, b any) {
		t.Helper()
		if fmt.Sprintf("%v", a) != fmt.Sprintf("%v", b) {
			t.Errorf("%s: vanilla=%v mc-go=%v", name, a, b)
		}
	}
	check("menu", want.menu, got.menu)
	check("props", want.props, got.props)
	check("prop2Seq", want.prop2Seq, got.prop2Seq)
	check("buArm", want.phases["arm"].bu, got.phases["arm"].bu)
	check("buOff", want.phases["off"].bu, got.phases["off"].bu)
	check("buIns", want.phases["insert"].bu, got.phases["insert"].bu)
	check("buFail", want.phases["fail"].bu, got.phases["fail"].bu)
	check("evArm", want.phases["arm"].ev, got.phases["arm"].ev)
	check("evIns", want.phases["insert"].ev, got.phases["insert"].ev)
	check("evFail", want.phases["fail"].ev, got.phases["fail"].ev)
	check("spawnArm", want.phases["arm"].spawn, got.phases["arm"].spawn)
	check("spawnIns", want.phases["insert"].spawn, got.phases["insert"].spawn)
	check("spawnFail", want.phases["fail"].spawn, got.phases["fail"].spawn)
}
