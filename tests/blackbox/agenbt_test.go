// TestAgeableNBTParity pins AgeableMob.addAdditionalSaveData: every
// ageable type emits Age/ForcedAge/AgeLocked unconditionally while
// canBeABaby, and Animal subclasses add InLove — the water-creature,
// villager, and cube types skip it.
package blackbox

import (
	"testing"
	"time"
)

func TestAgeableNBTParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	types := []string{
		// animals (full quad incl. InLove)
		"armadillo", "axolotl", "bee", "camel", "camel_husk",
		"chicken", "cow", "mooshroom", "donkey", "horse", "llama",
		"mule", "skeleton_horse", "trader_llama", "zombie_horse",
		"cat", "ocelot", "fox", "frog", "goat", "happy_ghast",
		"nautilus", "zombie_nautilus", "panda", "parrot", "pig",
		"polar_bear", "rabbit", "sheep", "sniffer", "turtle",
		"wolf", "strider", "hoglin",
		// non-Animal ageables (no InLove)
		"squid", "glow_squid", "dolphin", "villager",
		"wandering_trader",
		// non-ageable controls — none of the four keys (Slime
		// sizes via Size, not Age, on vanilla)
		"slime", "magma_cube", "zombie",
	}
	// frog/camel_husk/parrot/zombie_nautilus are Animal but not
	// AgeableMob — vanilla emits InLove and no age keys for them.
	animalOnly := map[string]bool{
		"frog": true, "camel_husk": true, "parrot": true,
		"zombie_nautilus": true,
	}

	type vec struct{ forcedAge, ageLocked, inLove bool }
	run := func(addr string) map[string]vec {
		out := map[string]vec{}
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		sink := newChatSink(pr, pw)
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "tp P 40.5 -60 40.5")
		sendCommand(t, pw, "fill 40 -61 40 48 -61 48 minecraft:stone")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		for _, tn := range types {
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			time.Sleep(200 * time.Millisecond)
			sendCommand(t, pw, "summon minecraft:"+tn+" 44.5 -60 44.5 {NoAI:1b}")
			time.Sleep(300 * time.Millisecond)
			sel := "@e[type=minecraft:" + tn + ",limit=1] "
			// A path query on a missing key fails (no
			// data.entity.query line) — empty means absent.
			out[tn] = vec{
				forcedAge: dataGetAll(t, sink, pw, sel+"ForcedAge") != "",
				ageLocked: dataGetAll(t, sink, pw, sel+"AgeLocked") != "",
				inLove:    dataGetAll(t, sink, pw, sel+"InLove") != "",
			}
		}
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	for tn, v := range van {
		if mc[tn] != v {
			t.Errorf("%s: vanilla %+v vs mc-go %+v", tn, v, mc[tn])
		}
		// Every ageable must show the triple; the Animal-only
		// and non-ageable controls show none of it.
		if !animalOnly[tn] && tn != "zombie" && tn != "slime" &&
			tn != "magma_cube" && (!v.forcedAge || !v.ageLocked) {
			t.Errorf("%s: vanilla lacks the ageable triple: %+v", tn, v)
		}
	}
	if t.Failed() {
		for _, tn := range types {
			t.Logf("  %-18s van %+v  mc %+v", tn, van[tn], mc[tn])
		}
	}
}
