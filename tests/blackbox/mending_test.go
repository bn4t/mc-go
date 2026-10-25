package blackbox

import (
	"testing"
	"time"
)

// TestMendingOrbParity pins ExperienceOrb.repairPlayerItems: each orb
// take routes its value through a random damaged equipment item with
// mending (hands + 4 armor slots) before giveExperiencePoints sees it.
// Mending's REPAIR_WITH_XP component repairs 2 durability per xp:
// repaired = min(value*2, damage), and the leftover
// value - repaired*value/(value*2) recurses into the next damaged
// mending item; only the remainder becomes experience.
//
// A chestplate at damage 100 drinks a Value:5 orb completely —
// repaired 10, leftover 0, NO set_experience at all — while a
// Value:60 orb over-repairs the remaining 90 damage and 15 xp
// reaches the bar (repaired 90 → leftover 60 - 90*60/120 = 15).
func TestMendingOrbParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Mending"),
	}, nil)
	defer targets.cleanup()

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "Mending")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 63.5 -60 60.5 -90 0")
			// /give lands in the held hotbar slot — mainhand counts
			// as a mending equipment slot on both servers.
			sendCommand(t, pw, "give @s minecraft:diamond_chestplate[minecraft:enchantments={mending:1},minecraft:damage=100]")
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)

			// Value:5 — 10 repaired of 100, leftover 0: no xp packet.
			sendCommand(t, pw, "summon minecraft:experience_orb 63.5 -60 60.5 {Value:5}")
			ev := eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			xpGrants := 0
			for _, e := range ev {
				if e.kind == "xp" {
					xpGrants++
				}
			}
			if xpGrants != 0 {
				for _, e := range ev {
					t.Logf("%s: dbg %+v", tc.tag, e)
				}
				t.Fatalf("%s: mending leg-1 saw %d xp grants — want 0 (all 5 drunk by the chestplate)",
					tc.tag, xpGrants)
			}

			// Value:60 — 90 repaired of 90 remaining damage, 15 left.
			sendCommand(t, pw, "summon minecraft:experience_orb 63.5 -60 60.5 {Value:60}")
			ev = eggDrainFor(t, c, pr, pw, 2500*time.Millisecond)
			xpGrants = 0
			totalXP := int32(-1)
			for _, e := range ev {
				if e.kind == "xp" {
					xpGrants++
					totalXP = e.data
				}
			}
			if xpGrants != 1 || totalXP != 15 {
				for _, e := range ev {
					t.Logf("%s: dbg %+v", tc.tag, e)
				}
				t.Fatalf("%s: mending leg-2 saw %d xp grants total %d — want 1 grant of 15",
					tc.tag, xpGrants, totalXP)
			}
		})
	}
}
