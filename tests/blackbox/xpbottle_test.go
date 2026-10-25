//go:build blackbox

package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestXpBottleParity pins ExperienceBottleItem.use +
// ThrownExperienceBottle.onHit wire-for-wire: the unconditional
// seq ack, the experience_bottle.throw sound (NEUTRAL), spawn_entity
// etype=experience_bottle with the shooter's entity id in data, the
// DATA_ITEM_STACK set_entity_data, the survival consume, then on
// impact a level_event 2002 splash (color -13083194), remove_entities,
// and 1-3 experience_orb spawns whose index-8 metadata values are
// vanilla denominations summing to 3-11. Orb spawn data must be 0 —
// vanilla's getAddEntityPacket leaves the field unused for orbs.
func TestXpBottleParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("XpBottle"),
	}, nil)
	defer targets.cleanup()

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "XpBottle")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			sendCommand(t, pw, "gamemode survival @s")

			// Leg 1 — side-face hit: bottle into a wall like the egg
			// volley. Block-hit direction = the face's unit normal.
			sendCommand(t, pw, "tp @s 60.5 -60 60.5 -90 0")
			sendCommand(t, pw, "fill 63 -61 58 63 -58 63 minecraft:stone")
			drainChunkWindow(t, tc.tag, c, pr, pw, 400*time.Millisecond)
			sendCommand(t, pw, "give @s minecraft:experience_bottle 8")
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			sendUseItemAim(t, pw, 0, 1, -90, 0)
			ev := eggDrainFor(t, c, pr, pw, 4*time.Second)
			assertHasAck(t, tc.tag, ev, 1)
			assertHasSound(t, tc.tag, ev, "entity.experience_bottle.throw")
			bottleID := assertThrowSpawn(t, tc.tag, ev, "experience_bottle")
			assertHasMeta(t, tc.tag, ev, bottleID)
			assertHasSlotShrink(t, tc.tag, ev)
			assertNoEntityEvent(t, tc.tag, ev, bottleID)
			assertRemoved(t, tc.tag, ev, bottleID)
			assertXpSplash(t, tc.tag, ev)
			assertXpOrbs(t, tc.tag, ev)

			// Leg 2 — top-face hit: lob onto flat ground (pitch up so
			// the -20 offset arcs it back down onto the floor).
			sendCommand(t, pw, "tp @s 0.5 -60 0.5 0 -60")
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			sendUseItemAim(t, pw, 0, 2, 0, -60)
			ev = eggDrainFor(t, c, pr, pw, 4*time.Second)
			assertHasAck(t, tc.tag, ev, 2)
			bottleID = assertThrowSpawn(t, tc.tag, ev, "experience_bottle")
			assertRemoved(t, tc.tag, ev, bottleID)
			assertXpSplash(t, tc.tag, ev)
			assertXpOrbs(t, tc.tag, ev)
		})
	}
}

// assertXpSplash requires the level_event 2002 broadcast that
// ThrownExperienceBottle.onHit sends with the splash color -13083194.
func assertXpSplash(t *testing.T, tag string, ev []eggEvent) {
	t.Helper()
	for _, e := range ev {
		if e.kind == "level" && e.b0 == 2002 {
			if e.data != -13083194 {
				t.Fatalf("%s: level_event 2002 data = %d — vanilla sends the splash color -13083194", tag, e.data)
			}
			return
		}
	}
	t.Fatalf("%s: no level_event 2002 on xp-bottle impact", tag)
}

// assertXpOrbs requires at least one experience_orb spawn carrying
// data=0, each with an index-8 metadata value drawn from the vanilla
// denomination set reachable for 3-11 xp ({1,3,7}), summing to 3-11.
func assertXpOrbs(t *testing.T, tag string, ev []eggEvent) {
	t.Helper()
	ot, _ := server.EntityTypeID("experience_orb")
	var orbs []int32
	for _, e := range ev {
		if e.kind == "spawn" && e.etype == ot {
			if e.data != 0 {
				t.Fatalf("%s: xp-orb spawn data = %d — vanilla leaves getAddEntityPacket's field at 0", tag, e.data)
			}
			orbs = append(orbs, e.eid)
		}
	}
	if len(orbs) < 1 || len(orbs) > 3 {
		t.Fatalf("%s: %d xp-orbs spawned — a 3-11 xp bottle splits into 1-3 denominations", tag, len(orbs))
	}
	// Vanilla emits an orb's metadata twice (spawn bundle + tracker
	// refresh), so dedupe by entity id before summing.
	vals := map[int32]int32{}
	for _, e := range ev {
		if e.kind != "meta" || !e.hasOval {
			continue
		}
		isOrb := false
		for _, id := range orbs {
			if e.eid == id {
				isOrb = true
			}
		}
		if !isOrb {
			continue
		}
		switch e.oval {
		case 1, 3, 7, 17, 37, 73, 149, 307, 617, 1237, 2477:
		default:
			t.Fatalf("%s: orb %d value %d is not a vanilla denomination", tag, e.eid, e.oval)
		}
		if prev, ok := vals[e.eid]; ok && prev != e.oval {
			t.Fatalf("%s: orb %d metadata value changed %d -> %d", tag, e.eid, prev, e.oval)
		}
		vals[e.eid] = e.oval
	}
	sum := int32(0)
	counted := len(vals)
	for _, v := range vals {
		sum += v
	}
	if counted < len(orbs) {
		t.Fatalf("%s: only %d/%d orbs carried an index-8 value entry", tag, counted, len(orbs))
	}
	if sum < 3 || sum > 11 {
		t.Fatalf("%s: orb values sum to %d — vanilla bottles award 3-11 xp", tag, sum)
	}
}
