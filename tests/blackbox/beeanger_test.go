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
//   - An explosion carve angers free bees in the box too — the
//     occupants themselves vanish with the block entity (vanilla
//     releases only through the explicit emptyAllLivingFromHive
//     calls — harvest, survival break, fire-evacuation).
//   - Every other removal (setblock/fill overwrite, creative dig,
//     setblock-destroy) vanishes the occupants silently: nothing
//     calls emptyAllLivingFromHive on those paths.
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
			for i := 0; i < 40 && !gotSting3; i++ {
				eggDrainFor(t, c, pr, pw, 500*time.Millisecond)
				if p := probe("entity @e[type=minecraft:bee,nbt={HasStung:1b}]", 12); p == "hit" {
					gotSting3 = true
				}
			}
			if !gotSting3 {
				t.Fatalf("%s: campfire-break sting probe missed — campfire must not protect a breaker", tc.tag)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:bee]")
			sendCommand(t, pw, "effect clear @s minecraft:poison")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)

			// === Leg 4: explosion-destroyed nest — onExplosionHit
			// runs angerNearbyBees unconditionally (the occupants
			// themselves vanish with the block entity — vanilla
			// releases only via emptyAllLivingFromHive, which
			// onExplosionHit never calls). A FREE bee summoned
			// far enough west to survive the blast is the
			// discriminating probe: it is no releasee — only the
			// 8x6x8 box pass can anger it.
			// Player AND bees must stay inside the 8x6x8 box after
			// the blast — hurtEntities runs before the block pass
			// and the sweep returns early on an empty player list —
			// so everyone stands beyond blast reach (d>8).
			sendCommand(t, pw, "tp @s 57 -59.5 80.5 -90 0")
			sendCommand(t, pw, fmt.Sprintf("setblock 63 -60 80 minecraft:bee_nest[facing=west,honey_level=5]%s", nestNBT))
			sendCommand(t, pw, "setblock 67 -60 80 minecraft:redstone_block")
			eggDrainFor(t, c, pr, pw, 1200*time.Millisecond)
			sendCommand(t, pw, "setblock 66 -60 80 minecraft:tnt")
			// Free bees deep inside the anger box (x∈[55,71]) but
			// beyond blast reach (damage/knockback reaches 2*power=8
			// from 66): hurtEntities runs BEFORE interactWithBlocks,
			// so a bee close enough to get flung lands outside the
			// box and the sweep never sees it. Summon AFTER the fuse
			// starts to keep wander time short.
			sendCommand(t, pw, "summon minecraft:bee 57 -59 78")
			sendCommand(t, pw, "summon minecraft:bee 57 -59 82")
			// Fuse 80 (~4s), then the carve destroys the nest.
			eggDrainFor(t, c, pr, pw, 5000*time.Millisecond)
			if p := probe("entity @e[type=minecraft:bee]", 20); p != "hit" {
				t.Fatalf("%s: explosion release probe = %s — want live bees", tc.tag, p)
			}
			gotSting4 := false
			for i := 0; i < 30 && !gotSting4; i++ {
				eggDrainFor(t, c, pr, pw, 500*time.Millisecond)
				if p := probe("entity @e[type=minecraft:bee,nbt={HasStung:1b}]", 21); p == "hit" {
					gotSting4 = true
				}
			}
			if !gotSting4 {
				t.Fatalf("%s: explosion sting probe missed — angerNearbyBees must run onExplosionHit", tc.tag)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:bee]")
			sendCommand(t, pw, "effect clear @s minecraft:poison")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)

			// === Leg 5: setblock overwrite — command writes place
			// with flag 256, which skips preRemoveSideEffects: the
			// block entity is removed silently and the occupants
			// vanish — no release, no anger.
			sendCommand(t, pw, "tp @s 75.5 -59.5 70.5 -90 0")
			sendCommand(t, pw, fmt.Sprintf("setblock 75 -60 70 minecraft:bee_nest[facing=east,honey_level=5]%s", nestNBT))
			eggDrainFor(t, c, pr, pw, 1200*time.Millisecond)
			sendCommand(t, pw, "setblock 75 -60 70 air")
			eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			if p := probe("entity @e[type=minecraft:bee]", 22); p != "miss" {
				t.Fatalf("%s: setblock-overwrite release probe = %s — flag-256 place must not release occupants", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:bee]")
			eggDrainFor(t, c, pr, pw, 1000*time.Millisecond)

			// === Leg 6: creative dig — destroyBlock with drops off
			// skips playerDestroy entirely: no emptyAllLivingFromHive,
			// no angerNearbyBees — the occupants vanish with the
			// block entity and nothing spawns.
			sendCommand(t, pw, "gamemode creative @s")
			sendCommand(t, pw, "tp @s 75.5 -59.5 70.5 -90 0")
			sendCommand(t, pw, fmt.Sprintf("setblock 75 -60 70 minecraft:bee_nest[facing=east,honey_level=5]%s", nestNBT))
			eggDrainFor(t, c, pr, pw, 1200*time.Millisecond)
			sendDig(t, pw, 0, 75, -60, 70, 20)
			eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			if p := probe("block 75 -60 70 minecraft:air", 24); p != "hit" {
				t.Fatalf("%s: creative-break probe = %s — want nest gone", tc.tag, p)
			}
			if p := probe("entity @e[type=minecraft:bee]", 25); p != "miss" {
				t.Fatalf("%s: creative-break release probe = %s — occupants must vanish, not release", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:bee]")
			sendCommand(t, pw, "gamemode survival @s")
			eggDrainFor(t, c, pr, pw, 1000*time.Millisecond)

			// === Leg 7: setblock destroy — `setblock ... destroy`
			// goes through destroyBlock (not BlockInput.place), so
			// it drops + removes like a dig: still no
			// emptyAllLivingFromHive call → occupants vanish.
			sendCommand(t, pw, fmt.Sprintf("setblock 75 -60 70 minecraft:bee_nest[facing=east,honey_level=5]%s", nestNBT))
			eggDrainFor(t, c, pr, pw, 1200*time.Millisecond)
			sendCommand(t, pw, "setblock 75 -60 70 air destroy")
			eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			if p := probe("entity @e[type=minecraft:bee]", 26); p != "miss" {
				t.Fatalf("%s: setblock-destroy release probe = %s — destroy mode must not release occupants", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:bee]")
			eggDrainFor(t, c, pr, pw, 1000*time.Millisecond)

			// === Leg 8: fire evacuation — BeehiveBlockEntity.
			// setChanged evacuates EMERGENCY when isFireNearby (any
			// FireBlock in pos±1). Player is null on this path:
			// occupants release but nobody angers.
			sendCommand(t, pw, "tp @s 75.5 -59.5 70.5 -90 0")
			sendCommand(t, pw, fmt.Sprintf("setblock 75 -60 70 minecraft:bee_nest[facing=east,honey_level=5]%s", nestNBT))
			eggDrainFor(t, c, pr, pw, 1200*time.Millisecond)
			sendCommand(t, pw, "setblock 74 -60 70 minecraft:fire")
			eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			if p := probe("entity @e[type=minecraft:bee]", 27); p != "hit" {
				t.Fatalf("%s: fire-evacuation release probe = %s — isFireNearby must evacuate the nest", tc.tag, p)
			}
			eggDrainFor(t, c, pr, pw, 4000*time.Millisecond)
			if p := probe("entity @e[type=minecraft:bee,nbt={HasStung:1b}]", 28); p != "miss" {
				t.Fatalf("%s: fire-evacuation sting probe = %s — evacuation must not anger", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:bee]")
			eggDrainFor(t, c, pr, pw, 1000*time.Millisecond)
		})
	}
}
