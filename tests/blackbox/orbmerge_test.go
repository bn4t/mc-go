package blackbox

import (
	"testing"
	"time"
)

// TestXpOrbMergeParity pins ExperienceOrb's merge + follow + count
// semantics: scanForMerges runs when tickCount%20==0 and the scanning
// orb absorbs every neighbour inside its inflate(0.5) box (count adds,
// age goes to the pair's minimum, neighbour discards);
// followNearbyPlayer pulls the orb toward the nearest non-spectator
// player inside 8 blocks with delta += normalize(dir)*(1-d/8)^2*0.1;
// and playerTouch grants value xp once per takeXpDelay(2-tick) window,
// decrementing count and discarding at zero.
//
// scanForMerges only absorbs a neighbour whose id differs by a
// multiple of 40 AND whose value equals self's — so the test
// summons orb A, 39 marker fillers, then orb B at the same spot
// (ids differ by 40 → mergeable), plus orb C 3 blocks north
// (outside the 0.5-inflated box → stays).
//
// Leg 1: with the player in spectator far away, exactly ONE
// remove lands within ~20 ticks (the merge victim) and the other
// two orbs stay.
// Leg 2: the player switches to survival at the orb spot — both
// survivors glide in and get absorbed: the merged one grants its
// value TWICE (count=2), the distant one once — three set_experience
// updates and two pickup removes.
func TestXpOrbMergeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("OrbMerge"),
	}, nil)
	defer targets.cleanup()

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "OrbMerge")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			sendCommand(t, pw, "gamemode spectator @s")
			sendCommand(t, pw, "tp @s 60.5 -58 60.5 -90 0")

			sendCommand(t, pw, "summon minecraft:experience_orb 63.5 -60 60.5 {Value:5}")
			for i := 0; i < 39; i++ {
				sendCommand(t, pw, "summon minecraft:marker 63.5 -60 60.5")
			}
			sendCommand(t, pw, "summon minecraft:experience_orb 63.5 -60 60.5 {Value:5}")
			sendCommand(t, pw, "summon minecraft:experience_orb 60.5 -60 57.5 {Value:5}")
			orbType := mustEType(t, "experience_orb")
			live := map[int32]bool{}
			everSpawned := map[int32]bool{}
			track := func(ev []eggEvent) {
				for _, e := range ev {
					switch e.kind {
					case "spawn":
						if e.etype == orbType {
							live[e.eid] = true
							everSpawned[e.eid] = true
						}
					case "remove":
						delete(live, e.eid)
					}
				}
			}
			ev := eggDrainFor(t, c, pr, pw, 2000*time.Millisecond)
			track(ev)
			if len(everSpawned) < 3 {
				for _, e := range ev {
					t.Logf("%s: dbg %+v", tc.tag, e)
				}
				t.Fatalf("%s: wanted ≥3 orb spawns, saw %d", tc.tag, len(everSpawned))
			}
			// The tracker churns remove+respawn cycles (re-idding entities
			// on re-add), so judge the merge by the live set alone: two
			// survivors = the merged pair + the distant orb.
			ev = eggDrainFor(t, c, pr, pw, 3500*time.Millisecond)
			track(ev)
			if len(live) != 2 {
				for _, e := range ev {
					t.Logf("%s: dbg %+v", tc.tag, e)
				}
				t.Fatalf("%s: %d live orbs — want 2 (merged pair + distant)",
					tc.tag, len(live))
			}

			// Leg 2 — survival player at the spot: the merged orb
			// (count 2) grants twice, the far one once; both discard.
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 63.5 -60 59 -90 0")
			ev = eggDrainFor(t, c, pr, pw, 5000*time.Millisecond)
			xpGrants := 0
			for _, e := range ev {
				if e.kind == "xp" {
					xpGrants++
				}
			}
			track(ev)
			if xpGrants != 3 || len(live) != 0 {
				for _, e := range ev {
					t.Logf("%s: dbg %+v", tc.tag, e)
				}
				t.Fatalf("%s: pickup window saw %d xp grants, %d live orbs — "+
					"want 3 grants (merged count=2 → two takes) and 0 left", tc.tag, xpGrants, len(live))
			}
		})
	}
}
