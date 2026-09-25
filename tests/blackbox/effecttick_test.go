//go:build blackbox

package blackbox

import (
	"testing"
	"time"
)

// TestEffectTickParity pins the periodic MobEffect.applyEffectTick
// hooks — MobEffectInstance.tick calls
// shouldApplyEffectTickThisTick(remaining, amplifier) on every live
// tick of a finite instance, fires the hook when the rate matches,
// then decrements. The rates measured off the 26.2 jar:
//
//	poison        25 >> amplifier ticks — hurtServer(magic(), 1.0)
//	              while health > 1 (it can never land the kill)
//	wither        40 >> amplifier ticks — hurtServer(wither(), 1.0)
//	regeneration  50 >> amplifier ticks — heal(1.0) below max
//	hunger        every tick — causeFoodExhaustion(0.005*(amp+1)),
//	              player-only
//
// The wire pins: damage_event carries magic=27 / wither=49 with
// cause=direct=0 (entity-less sources), and the mob heal surfaces as
// an index-9 float metadata update.
func TestEffectTickParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("FxTick"),
	}, nil)
	defer targets.cleanup()

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "FxTick")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 60.5 -60 60.5 -90 0")

			// Leg A — poison on a mob: /effect give's duration arg is
			// seconds, so 4s = 80 ticks. Fires at remaining 75/50/25 —
			// three type-27 magic damage_events 25 ticks apart, then
			// the instance expires. 80%25 != 0, so the duration-edge
			// convention (pre-decrement vs post-decrement) can't skew
			// the count.
			sendCommand(t, pw, "summon minecraft:villager 63.5 -60 60.5 {NoAI:1b,NoGravity:1b}")
			ev := eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			var vidA int32
			for _, e := range ev {
				if e.kind == "spawn" && e.etype == mustEType(t, "villager") {
					vidA = e.eid
				}
			}
			if vidA == 0 {
				for _, e := range ev {
					t.Logf("%s: dbg %+v", tc.tag, e)
				}
				t.Fatalf("%s: poison villager never spawned", tc.tag)
			}
			sendCommand(t, pw, "effect give @e[type=villager,distance=..4] minecraft:poison 4 0")
			ev = eggDrainFor(t, c, pr, pw, 4500*time.Millisecond)
			poisonDmg := 0
			for _, e := range ev {
				if e.kind == "dmg" && e.eid == vidA {
					poisonDmg++
					if e.etype != 27 {
						t.Fatalf("%s: poison dose type id %d — want 27 magic", tc.tag, e.etype)
					}
					if e.b0 != 0 || e.data != 0 {
						t.Fatalf("%s: poison dose cause/direct %d/%d — entity-less source wires 0/0",
							tc.tag, e.b0, e.data)
					}
				}
			}
			if poisonDmg != 3 {
				for _, e := range ev {
					if e.kind == "dmg" || e.kind == "meta" || e.kind == "spawn" {
						t.Logf("%s: dbg %+v", tc.tag, e)
					}
				}
				t.Fatalf("%s: poison fired %d times — want 3 (remaining 75,50,25)", tc.tag, poisonDmg)
			}

			// Leg B — regeneration on a hurt mob: Health:10f +
			// regen 4s (80t) heals once at remaining 50 → the
			// index-9 float health entry lands 1100.
			sendCommand(t, pw, "summon minecraft:villager 63.5 -60 61.5 {NoAI:1b,NoGravity:1b,Health:10f}")
			ev = eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			var vidB int32
			for _, e := range ev {
				if e.kind == "spawn" && e.etype == mustEType(t, "villager") && e.eid != vidA {
					vidB = e.eid
				}
			}
			if vidB == 0 {
				t.Fatalf("%s: regen villager never spawned", tc.tag)
			}
			sendCommand(t, pw, "effect give @e[type=villager,distance=..4] minecraft:regeneration 4 0")
			ev = eggDrainFor(t, c, pr, pw, 3500*time.Millisecond)
			heals := 0
			for _, e := range ev {
				if e.kind == "meta" && e.eid == vidB && e.hasOval && e.oval >= 1100 {
					heals++
				}
			}
			if heals < 1 {
				for _, e := range ev {
					if e.kind == "meta" || e.kind == "dmg" || e.kind == "spawn" {
						t.Logf("%s: dbg %+v", tc.tag, e)
					}
				}
				t.Fatalf("%s: villager never healed — no health meta ≥ 1100", tc.tag)
			}

			// Leg C — wither on a mob: 3s (60t) fires once at
			// remaining 40 — a single type-49 wither damage_event.
			sendCommand(t, pw, "summon minecraft:villager 63.5 -60 62.5 {NoAI:1b,NoGravity:1b}")
			ev = eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			var vidC int32
			for _, e := range ev {
				if e.kind == "spawn" && e.etype == mustEType(t, "villager") && e.eid != vidA && e.eid != vidB {
					vidC = e.eid
				}
			}
			if vidC == 0 {
				t.Fatalf("%s: wither villager never spawned", tc.tag)
			}
			sendCommand(t, pw, "effect give @e[type=villager,distance=..4] minecraft:wither 3 0")
			ev = eggDrainFor(t, c, pr, pw, 3500*time.Millisecond)
			witherDmg := 0
			for _, e := range ev {
				if e.kind == "dmg" && e.eid == vidC {
					witherDmg++
					if e.etype != 49 {
						t.Fatalf("%s: wither dose type id %d — want 49 wither", tc.tag, e.etype)
					}
					if e.b0 != 0 || e.data != 0 {
						t.Fatalf("%s: wither dose cause/direct %d/%d — entity-less source wires 0/0",
							tc.tag, e.b0, e.data)
					}
				}
			}
			if witherDmg != 1 {
				for _, e := range ev {
					if e.kind == "dmg" || e.kind == "meta" || e.kind == "spawn" {
						t.Logf("%s: dbg %+v", tc.tag, e)
					}
				}
				t.Fatalf("%s: wither fired %d times — want 1 (remaining 40)", tc.tag, witherDmg)
			}
		})
	}
}
