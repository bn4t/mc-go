package blackbox

// TestRecipeCraftedAdvParity exercises minecraft:recipe_crafted via
// adventure/craft_decorated_pot_using_only_sherds. The criterion pins
// recipe_id=minecraft:decorated_pot plus a four-slot ingredient
// multiset of #decorated_pot_sherds — vanilla's TriggerInstance
// consumes one distinct input stack per predicate, so four bricks
// (which craft the same recipe through decorated_pot_ingredients)
// must NOT grant while four sherds must. Both legs craft at a table:
// one per edge-middle slot (2,4,6,8 of the 3x3), take the result, and
// the click driver's packet fold feeds every UpdateAdvancements into
// an advFlush for the diff.

import (
	"testing"
)

func TestRecipeCraftedAdvParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("PotSV", "PotSM", "PotBV", "PotBM"),
	}, nil)
	defer tg.cleanup()

	legs := []struct {
		name  string
		item  string
		vname string
		mname string
		grant bool
	}{
		{"sherds", "minecraft:flow_pottery_sherd 4", "PotSV", "PotSM", true},
		{"bricks", "minecraft:brick 4", "PotBV", "PotBM", false},
	}

	for _, lg := range legs {
		t.Run(lg.name, func(t *testing.T) {
			run := func(addr, name string) *advFlush {
				f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
				d := newClickDriverOn(t, addr, name, "minecraft:crafting_table", 12,
					[][2]string{{"hotbar.0", lg.item}}, nil)
				d.adv = f
				for _, st := range []struct {
					slot   int16
					button byte
				}{
					{37, 0},                        // lift the stack
					{2, 1}, {4, 1}, {6, 1}, {8, 1}, // one per edge
					{37, 0}, // return the leftover
					{0, 0},  // take result
					{10, 0}, // park the pot in main
				} {
					d.click("step", st.slot, st.button, 0, false, false)
				}
				d.fold()
				return f
			}
			want := run(tg.vanilla, lg.vname)
			got := run(tg.mcgo, lg.mname)

			const adv = "minecraft:adventure/craft_decorated_pot_using_only_sherds"
			if want.prog[adv]["pot_crafted_using_only_sherds"] != lg.grant {
				t.Fatalf("vanilla %s: grant=%v want %v (prog=%v)", lg.name, want.prog[adv], lg.grant, want.prog)
			}
			if got.prog[adv]["pot_crafted_using_only_sherds"] != lg.grant {
				t.Fatalf("mc-go %s: grant=%v want %v (prog=%v)", lg.name, got.prog[adv], lg.grant, got.prog)
			}
			want.chats, got.chats = nil, nil
			if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
				t.Fatalf("%s parity: %d diffs\n%s", lg.name, len(diffs), joinLines(diffs))
			}
		})
	}
}
