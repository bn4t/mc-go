//go:build blackbox

package blackbox

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestBeeReleaseParity pins the occupant-release surface of
// BeehiveBlock + BeehiveBlockEntity (26.2 measured):
//
//   - useItemOn (shears / glass_bottle, honey_level>=5): drop x3
//     honeycomb or fill the bottle, shear/bottle_fill sound at the
//     player, then split on CampfireBlock.isSmokeyPos — a lit
//     campfire within five blocks below keeps occupants in
//     (resetHoneyLevel only); otherwise EMERGENCY release.
//   - DispenseItemBehavior$9 + ShearsDispenseItemBehavior:
//     BEE_RELEASED release — the stay-in-hive and front-blocked
//     gates apply, no campfire check.
//   - releaseOccupant: each bee snaps to pos+0.5 stepped
//     0.55+width/2 out the nest's FACING side (0 when the exit is
//     blocked, emergency only), beehive_exit sounds at the nest.
//   - playerDestroy: non-silk break empties EMERGENCY — even a
//     blocked exit releases.
//
// Anger lives in beeanger_test.go (emptyAllLivingFromHive's sweep +
// angerNearbyBees + the sting chain); this test only asserts the
// release surface: bee spawn packets + positions, sounds, the
// honey_level reset, and the harvest item side effects.
func TestBeeReleaseParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BeeRel"),
	}, nil)
	defer targets.cleanup()

	beeType := mustEType(t, "bee")
	itemType := mustEType(t, "item")

	// Two-occupant nest with a clear east exit; the campfire nest
	// sits two cells south with a lit campfire directly below.
	// ticks_in_hive:0 + large min_ticks_in_hive keeps occupants inside
	// until a harvest/break releases them — with min=0 vanilla's hive
	// tick would release them automatically before the interact.
	nestNBT := `{bees:[{entity_data:{id:"minecraft:bee"},ticks_in_hive:0,min_ticks_in_hive:9999},` +
		`{entity_data:{id:"minecraft:bee"},ticks_in_hive:0,min_ticks_in_hive:9999}]}`

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "BeeRel")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			// Report terrain loaded — use_item_on/dig drop until it lands.
			if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}

			bees := func(ev []eggEvent) []eggEvent {
				var out []eggEvent
				for _, e := range ev {
					if e.kind == "spawn" && e.etype == beeType {
						out = append(out, e)
					}
				}
				return out
			}
			sounds := func(ev []eggEvent) map[int32]int {
				m := map[int32]int{}
				for _, e := range ev {
					if e.kind == "sound" {
						m[e.eid]++
					}
				}
				return m
			}
			items := func(ev []eggEvent) int {
				n := 0
				for _, e := range ev {
					if e.kind == "spawn" && e.etype == itemType {
						n++
					}
				}
				return n
			}
			// assertRelease checks the count and that every bee
			// snapped at the east-face exit of nest (63,-60,60):
			// x=63.5+0.55+0.275=64.325, y=-60+0.5-0.25=-59.75, z=60.5.
			assertRelease := func(what string, ev []eggEvent, want int) {
				t.Helper()
				bs := bees(ev)
				if len(bs) != want {
					t.Fatalf("%s: %s saw %d bee spawns — want %d", tc.tag, what, len(bs), want)
				}
				if want == 0 {
					return
				}
				for _, b := range bs {
					if math.Abs(b.sx-64.325) > 0.05 || math.Abs(b.sy+59.75) > 0.05 || math.Abs(b.sz-60.5) > 0.05 {
						t.Fatalf("%s: %s bee snapped to (%.3f,%.3f,%.3f) — want (64.325,-59.75,60.5)",
							tc.tag, what, b.sx, b.sy, b.sz)
					}
				}
			}
			probe := func(spec string, i int) string {
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZBR-ok-%d", spec, i))
				sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZBR-miss-%d", spec, i))
				deadline := time.Now().Add(8 * time.Second)
				for time.Now().Before(deadline) {
					for _, e := range eggDrainFor(t, c, pr, pw, 400*time.Millisecond) {
						if e.kind != "chat" {
							continue
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZBR-ok-%d", i)) {
							return "hit"
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZBR-miss-%d", i)) {
							return "miss"
						}
					}
				}
				return "none"
			}

			// === Leg 1: player shears interact → EMERGENCY release.
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 62.5 -59.5 60.5 -90 0")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			// Vanilla refuses setblock on an unloaded chunk — wait out
			// the stream first, then confirm the cell is loaded.
			for i := 0; i < 20; i++ {
				if probe("loaded 63 -60 60", 90) == "hit" {
					break
				}
				eggDrainFor(t, c, pr, pw, 400*time.Millisecond)
			}
			sendCommand(t, pw, fmt.Sprintf("setblock 63 -60 60 minecraft:bee_nest[facing=east,honey_level=5]%s", nestNBT))
			sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:shears")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			sendUseItemOn(t, pw, 63, -60, 60, 1, 1)
			ev := eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			assertRelease("shears interact", ev, 2)
			if snd := sounds(ev); snd[165] < 1 {
				t.Fatalf("%s: shears interact missing beehive_shear sound", tc.tag)
			}
			if items(ev) < 1 {
				t.Fatalf("%s: shears interact dropped no honeycomb items", tc.tag)
			}
			if p := probe("block 63 -60 60 minecraft:bee_nest[honey_level=0]", 0); p != "hit" {
				t.Fatalf("%s: shears honey_level probe = %s — want reset to 0", tc.tag, p)
			}
			if p := probe("block 63 -60 60 minecraft:bee_nest", 1); p != "hit" {
				t.Fatalf("%s: nest block vanished after shear", tc.tag)
			}

			// === Leg 2: lit campfire below → occupants stay inside.
			sendCommand(t, pw, "setblock 63 -62 62 minecraft:campfire[lit=true]")
			sendCommand(t, pw, "setblock 63 -60 62 minecraft:bee_nest[facing=east,honey_level=5]"+nestNBT)
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)
			sendUseItemOn(t, pw, 63, -60, 62, 1, 2)
			ev = eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			assertRelease("campfire harvest", ev, 0)
			if p := probe("block 63 -60 62 minecraft:bee_nest[honey_level=0]", 2); p != "hit" {
				t.Fatalf("%s: campfire harvest honey_level probe = %s — want reset to 0", tc.tag, p)
			}
			// The bees stay in — a dispenser harvest later would still
			// find them; the campfire gate is player-harvest only.

			// === Leg 3: dispenser shears → BEE_RELEASED release.
			sendCommand(t, pw, "setblock 66 -60 60 minecraft:bee_nest[facing=east,honey_level=5]"+nestNBT)
			sendCommand(t, pw, "setblock 65 -60 60 minecraft:dispenser[facing=east]")
			sendCommand(t, pw, "item replace block 65 -60 60 container.0 with minecraft:shears")
			sendCommand(t, pw, "setblock 65 -59 60 minecraft:redstone_block")
			ev = eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			if got := len(bees(ev)); got != 2 {
				t.Fatalf("%s: dispenser shears saw %d bee spawns — want 2", tc.tag, got)
			}
			if p := probe("block 66 -60 60 minecraft:bee_nest[honey_level=0]", 3); p != "hit" {
				t.Fatalf("%s: dispenser shears honey_level probe = %s", tc.tag, p)
			}

			// === Leg 4: dispenser glass_bottle → honey_bottle +
			// BEE_RELEASED release.
			sendCommand(t, pw, "setblock 69 -60 60 minecraft:bee_nest[facing=east,honey_level=5]"+nestNBT)
			sendCommand(t, pw, "setblock 68 -60 60 minecraft:dispenser[facing=east]")
			sendCommand(t, pw, "item replace block 68 -60 60 container.0 with minecraft:glass_bottle")
			sendCommand(t, pw, "setblock 68 -59 60 minecraft:redstone_block")
			ev = eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			if got := len(bees(ev)); got != 2 {
				t.Fatalf("%s: dispenser bottle saw %d bee spawns — want 2", tc.tag, got)
			}
			if p := probe("items block 68 -60 60 container.* minecraft:honey_bottle", 4); p != "hit" {
				t.Fatalf("%s: dispenser bottle probe = %s — want honey_bottle inside", tc.tag, p)
			}

			// === Leg 5: survival break non-silk → EMERGENCY release
			// even though the east exit faces into the dispenser —
			// place a solid block at the exit to prove the emergency
			// gate ignores the blocked front.
			sendCommand(t, pw, "setblock 72 -60 60 minecraft:bee_nest[facing=east,honey_level=5]"+nestNBT)
			sendCommand(t, pw, "setblock 73 -60 60 minecraft:stone")
			sendCommand(t, pw, "tp @s 70.5 -59.5 60.5 -90 0")
			sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:iron_axe")
			eggDrainFor(t, c, pr, pw, 1200*time.Millisecond)
			sendDig(t, pw, 0, 72, -60, 60, 10)
			eggDrainFor(t, c, pr, pw, 1200*time.Millisecond)
			sendDig(t, pw, 2, 72, -60, 60, 11)
			ev = eggDrainFor(t, c, pr, pw, 3000*time.Millisecond)
			// Blocked exit → snap lands at the nest centre offset:
			// x=72.5, y=-59.75, z=60.5.
			bs := bees(ev)
			if len(bs) != 2 {
				t.Fatalf("%s: blocked-exit break saw %d bee spawns — want 2", tc.tag, len(bs))
			}
			for _, b := range bs {
				if math.Abs(b.sx-72.5) > 0.05 || math.Abs(b.sy+59.75) > 0.05 || math.Abs(b.sz-60.5) > 0.05 {
					t.Fatalf("%s: blocked break bee snapped to (%.3f,%.3f,%.3f) — want (72.5,-59.75,60.5)",
						tc.tag, b.sx, b.sy, b.sz)
				}
			}
		})
	}
}
