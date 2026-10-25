// TestDropChancesParity pins Mob.dropCustomDeathLoot's per-slot
// equipment drop chances: the summon-time drop_chances compound, its
// re-emit through data get, the player-kill gate, the 0.0/2.0 edge
// values, and the preserved-slot unconditional drop — all measured on
// the 26.2 jar (Mob.dropCustomDeathLoot + DropChances.isPreserved).
package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDropChancesParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type res struct {
		nbtEmit    string
		playerDrop bool
		preserve   bool
		gated      bool
		never      bool
	}
	run := func(addr string) res {
		var r res
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		sink := newChatSink(pr, pw)

		// probe reports whether an entity matching sel exists — an
		// execute-if say whose marker lands in the chat stream.
		probe := func(sel string, i int) bool {
			sendCommand(t, pw, fmt.Sprintf("execute if entity %s run say ZDC-ok-%d", sel, i))
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, fmt.Sprintf("ZDC-ok-%d", i)) {
						return true
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			return false
		}
		sword := `@e[type=minecraft:item,nbt={Item:{id:"minecraft:iron_sword"}}]`
		summon := func(x float64, tag string) {
			sendCommand(t, pw, fmt.Sprintf(
				"summon minecraft:zombie %.1f -60 44.5 {NoAI:1b,equipment:{mainhand:{id:\"minecraft:iron_sword\",count:1}}%s}",
				x, tag))
			time.Sleep(500 * time.Millisecond)
		}
		clearItems := func() {
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			time.Sleep(300 * time.Millisecond)
		}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "tp P 40.5 -60 40.5")
		sendCommand(t, pw, "fill 40 -61 40 48 -61 48 minecraft:stone")

		// NBT emit: the drop_chances compound parses on summon and
		// re-emits through data get.
		summon(40.5, ",drop_chances:{mainhand:1.0}")
		r.nbtEmit = dataGetAll(t, sink, pw,
			"@e[type=minecraft:zombie,limit=1] drop_chances.mainhand")
		sendCommand(t, pw, "kill @e[type=minecraft:zombie]")

		// chance 1.0 + a player kill: the sword always drops.
		summon(41.5, ",drop_chances:{mainhand:1.0}")
		clearItems()
		sendCommand(t, pw, "damage @e[type=minecraft:zombie,limit=1] 100 minecraft:player_attack by @s")
		time.Sleep(700 * time.Millisecond)
		r.playerDrop = probe(sword, 1)

		// chance 2.0 (preserved): drops even without a player kill.
		summon(42.5, ",drop_chances:{mainhand:2.0}")
		clearItems()
		sendCommand(t, pw, "kill @e[type=minecraft:zombie]")
		time.Sleep(700 * time.Millisecond)
		r.preserve = probe(sword, 2)

		// chance 1.0 but a non-player kill: the player-kill gate
		// holds the sword.
		summon(43.5, ",drop_chances:{mainhand:1.0}")
		clearItems()
		sendCommand(t, pw, "kill @e[type=minecraft:zombie]")
		time.Sleep(700 * time.Millisecond)
		r.gated = probe(sword, 3)

		// chance 0.0 + a player kill: never drops.
		summon(44.5, ",drop_chances:{mainhand:0.0}")
		clearItems()
		sendCommand(t, pw, "damage @e[type=minecraft:zombie,limit=1] 100 minecraft:player_attack by @s")
		time.Sleep(700 * time.Millisecond)
		r.never = probe(sword, 4)
		clearItems()
		sendCommand(t, pw, "kill @e[type=minecraft:zombie]")
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)
	if van != mc {
		t.Fatalf("mismatch: vanilla %+v vs mc-go %+v", van, mc)
	}
	if !van.playerDrop || !van.preserve || van.gated || van.never {
		t.Fatalf("mismatched probes on vanilla: %+v", van)
	}
	// The emit pins the parse: both legs must read back the override.
	for _, nbt := range []string{van.nbtEmit, mc.nbtEmit} {
		if !strings.Contains(nbt, "1.0") {
			t.Fatalf("drop_chances.mainhand missing from emit: %q", nbt)
		}
	}
}
