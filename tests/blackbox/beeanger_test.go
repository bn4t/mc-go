//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestBeeAngerParity pins BeehiveBlockEntity.emptyAllLivingFromHive's
// anger pass + Bee.doHurtTarget's sting (26.2 measured):
//
//   - Harvest without a campfire: EMERGENCY release, then every bee
//     within 4 blocks (distanceToSqr ≤ 16) of the harvester gets
//     setTarget on him — the bee flies straight at the player and
//     stings: sting damage (attack_damage 2) + stinger count on the
//     victim + POISON by difficulty (10s normal, 18s hard, none
//     easy/peaceful) + setHasStung + stopBeingAngry + BEE_STING.
//   - A nest break releases EMERGENCY then runs angerNearbyBees
//     unconditionally — a lit campfire under the nest does NOT
//     protect the breaker (the campfire gate only lives on the
//     harvest/use branch).
//   - Released bees >4 blocks away never anger (the sweep's distance
//     gate).
//
// The post-sting death clock (timeSinceSting, nextInt(clamp(1200-t,
// 1,1200))==0 every 5th tick → hurt(generic, health)) is implemented
// but not pinned — mean ~30s is too slow for the harness window.
func TestBeeAngerParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BeeAng"),
	}, nil)
	defer targets.cleanup()

	nestNBT := `{bees:[{entity_data:{id:"minecraft:bee"},ticks_in_hive:0,min_ticks_in_hive:9999},` +
		`{entity_data:{id:"minecraft:bee"},ticks_in_hive:0,min_ticks_in_hive:9999}]}`

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "BeeAng")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}

			probe := func(spec string, i int) string {
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZBA-ok-%d", spec, i))
				sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZBA-miss-%d", spec, i))
				deadline := time.Now().Add(8 * time.Second)
				for time.Now().Before(deadline) {
					for _, e := range eggDrainFor(t, c, pr, pw, 400*time.Millisecond) {
						if e.kind != "chat" {
							continue
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZBA-ok-%d", i)) {
							return "hit"
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZBA-miss-%d", i)) {
							return "miss"
						}
					}
				}
				return "none"
			}

			// === Leg 1: harvest no campfire → bees release, turn on
			// the harvester, and sting.
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 62.5 -59.5 60.5 -90 0")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			for i := 0; i < 20; i++ {
				if probe("loaded 63 -60 60", 90) == "hit" {
					break
				}
				eggDrainFor(t, c, pr, pw, 400*time.Millisecond)
			}
			sendCommand(t, pw, "clear @s")
			sendCommand(t, pw, fmt.Sprintf("setblock 63 -60 60 minecraft:bee_nest[facing=east,honey_level=5]%s", nestNBT))
			sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:shears")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			sendUseItemOn(t, pw, 63, -60, 60, 1, 1)
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)
			// The released bees should be out and angry — give the
			// chase a few seconds to land the sting.
			gotSting := false
			for i := 0; i < 20 && !gotSting; i++ {
				eggDrainFor(t, c, pr, pw, 500*time.Millisecond)
				if p := probe("entity @e[type=minecraft:bee,nbt={HasStung:1b}]", 1); p == "hit" {
					gotSting = true
				}
			}
			if !gotSting {
				// Present-but-calm read for the failure message.
				p1 := probe("entity @e[type=minecraft:bee]", 2)
				p2 := probe("entity @e[type=minecraft:bee,nbt={HasStung:0b}]", 3)
				t.Fatalf("%s: no bee reached HasStung — bee present=%s, calm=%s", tc.tag, p1, p2)
			}
			// Sting landed → the harvester carries POISON (10s normal)
			// and is no longer at full health. (Use Health≠20 — natural
			// regen and a possible second sting make the exact float
			// float.)
			if p := probe(`entity @s[nbt={active_effects:[{id:"minecraft:poison"}]}]`, 4); p != "hit" {
				t.Fatalf("%s: harvester poison probe = %s — want poisoned", tc.tag, p)
			}
			if p := probe("entity @s[nbt={Health:20f}]", 5); p != "miss" {
				t.Fatalf("%s: harvester full-health probe = %s — want damaged", tc.tag, p)
			}
			// Kill the released swarm before the next legs — post-sting
			// bees keep wandering and would false-positive the later
			// HasStung probes.
			sendCommand(t, pw, "kill @e[type=minecraft:bee]")
			sendCommand(t, pw, "effect clear @s minecraft:poison")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)

			// === Leg 2: harvest while >4 blocks from the exit —
			// released bees never anger (the sweep's distanceToSqr≤16
			// gate measures player↔bee, not player↔nest), never sting.
			// Player at (59.5,-59.5,70.5): exit bees land at
			// ~(64.3,-59.75,70.5) → 4.8 blocks out; nest block at
			// (63,-60,70) stays inside the ~4.5 survival reach.
			sendCommand(t, pw, "tp @s 59.5 -59.5 70.5 -90 0")
			sendCommand(t, pw, fmt.Sprintf("setblock 63 -60 70 minecraft:bee_nest[facing=east,honey_level=5]%s", nestNBT))
			eggDrainFor(t, c, pr, pw, 1200*time.Millisecond)
			sendUseItemOn(t, pw, 63, -60, 70, 1, 2)
			eggDrainFor(t, c, pr, pw, 4000*time.Millisecond)
			// Bees are out (release happened) but calm: none stung.
			// Leg-1's swarm was killed, so any live bee is a leg-2
			// releasee — released bees wander, so don't gate on
			// position.
			if p := probe("entity @e[type=minecraft:bee]", 7); p != "hit" {
				t.Fatalf("%s: far-harvest release probe = %s — want bees out", tc.tag, p)
			}
			if p := probe("entity @e[type=minecraft:bee,nbt={HasStung:1b}]", 8); p != "miss" {
				t.Fatalf("%s: far-harvest sting probe = %s — want no bee past 4 blocks to sting", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:bee]")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)

			// === Leg 3: nest break under a lit campfire —
			// emptyAllLivingFromHive(EMERGENCY) sedates the ≤4-block
			// releasees (stayOut=400) but playerDestroy then runs
			// angerNearbyBees unconditionally: every targetless bee in
			// the 8x6x8 box takes the breaker as target anyway.
			sendCommand(t, pw, "tp @s 80.5 -59.5 60.5 -90 0")
			sendCommand(t, pw, "setblock 81 -62 60 minecraft:campfire[lit=true]")
			sendCommand(t, pw, fmt.Sprintf("setblock 81 -60 60 minecraft:bee_nest[facing=east,honey_level=5]%s", nestNBT))
			sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:iron_axe")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			if p := probe("block 81 -62 60 minecraft:campfire[lit=true]", 9); p != "hit" {
				t.Fatalf("%s: campfire fixture probe = %s — setblock failed", tc.tag, p)
			}
			sendDig(t, pw, 0, 81, -60, 60, 10)
			eggDrainFor(t, c, pr, pw, 1200*time.Millisecond)
			sendDig(t, pw, 2, 81, -60, 60, 11)
			eggDrainFor(t, c, pr, pw, 1000*time.Millisecond)
			// Bees released AND angry at the breaker — poll for the
			// sting.
			if p := probe("entity @e[type=minecraft:bee]", 11); p != "hit" {
				t.Fatalf("%s: campfire-break release probe = %s — want bees out", tc.tag, p)
			}
			gotSting3 := false
			for i := 0; i < 20 && !gotSting3; i++ {
				eggDrainFor(t, c, pr, pw, 500*time.Millisecond)
				if p := probe("entity @e[type=minecraft:bee,nbt={HasStung:1b}]", 12); p == "hit" {
					gotSting3 = true
				}
			}
			if !gotSting3 {
				t.Fatalf("%s: campfire-break sting probe missed — campfire must not protect a breaker", tc.tag)
			}
		})
	}
}
