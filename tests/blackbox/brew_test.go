package blackbox

// TestBrewPotionParity drives a full brew cycle on both servers: load
// blaze powder and three water bottles into the stand, drop nether
// wart in the ingredient slot, wait out the 400-tick brew, and check
// the finished slots carry potion_contents=awkward (component 51 on
// the wire), the fuel and ingredient were spent, level event 1035
// fired, and taking a potion grants brewed_potion (nether/brew_potion
// "Local Brewery" — visible because obtain_blaze_rod is granted first
// so the child can announce).
//
// Brewing's per-tick slot/property fan-out differs in burst shape
// between the implementations, so the compare is on end state rather
// than packet counts: the same post-brew menu contents on both
// servers.

import (
	"testing"
	"time"
)

func TestBrewPotionParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BrewV", "BrewM"),
	}, nil)
	defer tg.cleanup()

	run := func(addr, name string) {
		d := newClickDriverOn(t, addr, name, "minecraft:brewing_stand", 11, [][2]string{
			{"hotbar.0", `minecraft:potion[minecraft:potion_contents="minecraft:water"]`},
			{"hotbar.1", `minecraft:potion[minecraft:potion_contents="minecraft:water"]`},
			{"hotbar.2", `minecraft:potion[minecraft:potion_contents="minecraft:water"]`},
			{"hotbar.3", "minecraft:nether_wart"},
			{"hotbar.4", "minecraft:blaze_powder 2"},
		}, nil)
		// Make nether/brew_potion's parent obtained so the child can
		// announce when the take lands.
		sendCommand(t, d.pw, "advancement grant @s only minecraft:nether/obtain_blaze_rod")
		digSentinel(t, d.pw, d.sink, "ADV", 10*time.Second)
		d.fold()

		// Quick-move (mode 1) from the hotbar lands each stack in its
		// role slot: bottles into 0-2, wart into 3, powder into 4. The
		// brewing menu runs 5 container slots, then main 5-31, then
		// hotbar 32-40 — so hotbar.i sits at menu slot 32+i.
		d.click("fuel", 36, 0, 1, false, false)
		for i, hb := range []int{32, 33, 34} {
			d.click("bottle-in", int16(hb), 0, 1, false, false)
			if got := d.snap.menu[int32(i)]; got == "" || got == "-" {
				t.Fatalf("%s: bottle %d never landed: %v", name, i, d.snap.menu)
			}
		}
		d.click("wart-in", 35, 0, 1, false, false)
		if got := d.snap.menu[3]; got == "" || got == "-" {
			t.Fatalf("%s: wart never landed: %v", name, d.snap.menu)
		}

		// Fuel 2 -> prop[1]=2 until the first brew charge spends; then
		// brewTime counts down from 400.
		if d.props[1] == 0 {
			t.Fatalf("%s: fuel property never lit", name)
		}

		// Wait out the 400 ticks (~20s) plus margin. The brew only
		// counts as finished once props[0] has gone positive and come
		// back to 0 — a still-zero brewTime is the pre-start state.
		deadline := time.Now().Add(30 * time.Second)
		started := false
		for time.Now().Before(deadline) {
			d.fold()
			if v, ok := d.props[0]; ok {
				if v > 0 {
					started = true
				} else if started {
					break
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !started {
			t.Fatalf("%s: brew never started", name)
		}
		d.fold()

		// All three potion slots hold water->awkward; the wart stack
		// went to empty; one fuel charge spent (2 -> 19 left shows as
		// prop[1]=19).
		for i := int32(0); i < 3; i++ {
			if got := d.snap.menu[i]; got != "1150#1[ph3]" {
				t.Fatalf("%s: potion slot %d=%s want potion 1150{ph3}: %v", name, i, got, d.snap.menu)
			}
		}
		if got := d.snap.menu[3]; got != "-" {
			t.Fatalf("%s: ingredient slot=%s want empty", name, got)
		}
		if d.props[1] != 19 {
			t.Fatalf("%s: fuel=%d want 19 after one brew", name, d.props[1])
		}
		if d.brewDone == 0 {
			t.Fatalf("%s: no level-event 1035", name)
		}

		// Take slot 0: brewed_potion fires and announces.
		before := d.announce
		d.click("take-potion", 0, 0, 0, false, false)
		if got := d.snap.carried; got != "1150#1[ph3]" {
			t.Fatalf("%s: carried=%s want potion{ph3}", name, got)
		}
		if d.announce == before {
			t.Fatalf("%s: no Local Brewery announce", name)
		}
	}

	run(tg.vanilla, "BrewV")
	run(tg.mcgo, "BrewM")
}
