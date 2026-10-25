//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestNestSilkCopyParity pins the bee nest/hive silk-touch model the
// loot tables declare (datagen-measured, 26.2): the match_tool
// silk_touch pool's entry carries copy_components[bees] (block_entity
// source — the Occupant list as {entity_data:{id:..},ticks_in_hive,
// min_ticks_in_hive}) plus copy_state[honey_level] into the item's
// minecraft:block_state component. Placement then runs
// updateBlockStateFromTag (block_state → the placed state) and the
// implicit bees component restores the occupants. A non-silk break
// drops the bare block item and EMERGENCY-releases the occupants
// instead.
func TestNestSilkCopyParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Bee"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	occ := `{entity_data:{id:"minecraft:bee"},ticks_in_hive:0,min_ticks_in_hive:600}`
	nest := func(extra string) string {
		return `bee_nest[facing=south,honey_level=4]{bees:[` + occ + `,` + occ + `,` + occ + `]` + extra + `}`
	}

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "Bee")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}

			// probeHit polls `execute if <spec>` until the condition
			// holds (returns "hit") or the deadline passes ("none").
			probeHit := func(spec string, i int) string {
				deadline := time.Now().Add(9 * time.Second)
				for time.Now().Before(deadline) {
					sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZNS-ok-%d", spec, i))
					for _, e := range eggDrainFor(t, c, pr, pw, 400*time.Millisecond) {
						if e.kind != "chat" {
							continue
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZNS-ok-%d", i)) {
							return "hit"
						}
					}
				}
				return "none"
			}
			// probeAbsent asks once after the settle window: a hit
			// means the thing exists when it should not.
			probeAbsent := func(spec string, i int) string {
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZNS-bad-%d", spec, i))
				deadline := time.Now().Add(1500 * time.Millisecond)
				for time.Now().Before(deadline) {
					for _, e := range eggDrainFor(t, c, pr, pw, 300*time.Millisecond) {
						if e.kind != "chat" {
							continue
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZNS-bad-%d", i)) {
							return "present"
						}
					}
				}
				return "absent"
			}

			dig := func(action int, pos [3]int32, seq int) {
				var b protocol.Buffer
				b.VarInt(int32(action))
				b.Position(pos[0], pos[1], pos[2])
				b.VarInt(1)
				b.VarInt(int32(seq))
				if err := pw.WritePacket(server.IDPlayPlayerActionSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
			}
			digBreak := func(pos [3]int32, seq int) {
				dig(0, pos, seq)
				eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
				dig(2, pos, seq+1)
				eggDrainFor(t, c, pr, pw, 900*time.Millisecond)
			}

			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 12.5 -60 12.5 0 0")
			sendCommand(t, pw, "clear @s")
			sendCommand(t, pw, `item replace entity @s hotbar.0 with diamond_pickaxe[minecraft:enchantments={silk_touch:1}] 1`)
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)

			// === Leg 1: silk break — the drop carries bees + block_state.
			sendCommand(t, pw, "setblock 12 -60 14 "+nest(""))
			eggDrainFor(t, c, pr, pw, 600*time.Millisecond)
			if p := probeHit(`block 12 -60 14 minecraft:bee_nest[honey_level=4]`, 0); p != "hit" {
				t.Fatalf("%s: leg0 setblock probe = %s — want the nest placed", tc.tag, p)
			}
			digBreak([3]int32{12, -60, 14}, 0)
			// A survival player vacuums the drop before the probe;
			// step out of pickup range first.
			sendCommand(t, pw, "tp @s 40 -60 40 0 0")
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)
			if p := probeHit(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:bee_nest",components:{"minecraft:bees":[{entity_data:{id:"minecraft:bee"}}]}}}]`, 1); p != "hit" {
				t.Fatalf("%s: leg1 bees-component probe = %s — want the occupant list", tc.tag, p)
			}
			if p := probeHit(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:bee_nest",components:{"minecraft:block_state":{honey_level:"4"}}}}]`, 2); p != "hit" {
				t.Fatalf("%s: leg1 block_state probe = %s — want honey_level:4", tc.tag, p)
			}
			// Silk never releases the occupants.
			if p := probeAbsent(`entity @e[type=minecraft:bee]`, 3); p != "absent" {
				t.Fatalf("%s: leg1 released-bees probe = %s — want none", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			sendCommand(t, pw, "tp @s 12.5 -60 12.5 0 0")
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)

			// === Leg 2: non-silk break — bare drop, occupants
			// EMERGENCY-release.
			sendCommand(t, pw, "setblock 12 -60 14 "+nest(""))
			sendCommand(t, pw, "clear @s")
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			digBreak([3]int32{12, -60, 14}, 4)
			sendCommand(t, pw, "tp @s 40 -60 40 0 0")
			eggDrainFor(t, c, pr, pw, 900*time.Millisecond)
			if p := probeAbsent(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:bee_nest"}}]`, 4); p != "absent" {
				t.Fatalf("%s: leg2 nest-drop probe = %s — want no bee_nest item at all", tc.tag, p)
			}
			if p := probeHit(`entity @e[type=minecraft:bee]`, 5); p != "hit" {
				t.Fatalf("%s: leg2 released-bees probe = %s — want EMERGENCY bees", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:bee]")
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			sendCommand(t, pw, "tp @s 12.5 -60 12.5 0 0")
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)

			// === Leg 3: placement round-trip — block_state applies
			// the honey_level and the bees occupants come back on
			// the next non-silk break.
			sendCommand(t, pw, `item replace entity @s hotbar.0 with bee_nest[minecraft:bees=[`+occ+`,`+occ+`,`+occ+`],minecraft:block_state={honey_level:"4"}] 1`)
			eggDrainFor(t, c, pr, pw, 600*time.Millisecond)
			sendUseItemOn(t, pw, 12, -61, 15, 1, 8)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			if p := probeHit(`block 12 -60 15 minecraft:bee_nest[honey_level=4]`, 6); p != "hit" {
				t.Fatalf("%s: leg3 honey_level probe = %s — want 4", tc.tag, p)
			}
			sendCommand(t, pw, "clear @s")
			eggDrainFor(t, c, pr, pw, 600*time.Millisecond)
			digBreak([3]int32{12, -60, 15}, 10)
			sendCommand(t, pw, "tp @s 40 -60 40 0 0")
			eggDrainFor(t, c, pr, pw, 900*time.Millisecond)
			if p := probeHit(`entity @e[type=minecraft:bee]`, 7); p != "hit" {
				t.Fatalf("%s: leg3 restored-bees probe = %s — want the occupants back", tc.tag, p)
			}
		})
	}
}
