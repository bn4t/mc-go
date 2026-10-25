// TestDimHopParity pins cross-dimension entity semantics measured on
// 26.2:
//
//   - `execute in <dim>` never narrows entity selection on its own: a
//     free @e (no position options) searches every level, so an
//     overworld pig resolves under `in the_nether`.
//   - A world-limited selector (x/y/z/dx/dy/dz/distance given) is the
//     one that scopes to the execute context's level: the same pig is
//     invisible to a distance-clipped @e under `in the_nether`.
//   - tp across dimensions (Entity.teleportCrossDimension) removes the
//     entity from its old level and parks it in the new one: the pig
//     is gone from @e in BOTH dimensions — vanilla's addDuringTeleport
//     drops it into an unticketed chunk where no selector reaches.
//   - `execute in <dim> run summon` parks the spawned entity the same
//     way: created, saved, but unreachable.
//   - A player arriving in the destination dimension wakes the parked
//     entities: they join the live entity set and resolve to @e again.
package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDimHopParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		owHit      bool // overworld pig resolves in overworld ctx
		netherHit  bool // free @e under `in nether` finds the ow pig
		netherMiss bool // world-limited @e under `in nether` misses it
		goneOW     bool // after cross-dim tp: invisible in overworld
		goneNether bool // ... and in the nether (parked)
		goneAny    bool // ... and to a clipped @e at the dest coords
		summonPark bool // summon into empty dim parks it too
		wakeNether bool // the parked netherpig wakes on player arrival
		wakeAny    bool // awakened pig resolves to a free @e
	}
	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		sink := newChatSink(pr, pw)

		probe := func(cmd string, i int) bool {
			sendCommand(t, pw, cmd)
			want := fmt.Sprintf("ZDH-ok-%d", i)
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, want) {
						return true
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			return false
		}
		hit := func(sel string, i int) bool {
			return probe(fmt.Sprintf("execute if entity %s run say ZDH-ok-%d", sel, i), i)
		}
		miss := func(sel string, i int) bool { return !hit(sel, i) }
		hitIn := func(dim, sel string, i int) bool {
			return probe(fmt.Sprintf("execute in %s if entity %s run say ZDH-ok-%d", dim, sel, i), i)
		}
		missIn := func(dim, sel string, i int) bool { return !hitIn(dim, sel, i) }

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(800 * time.Millisecond)
		sendCommand(t, pw, "tp P 40.5 -60 40.5")
		sendCommand(t, pw, `summon minecraft:pig 40.5 -60 40.5 {NoAI:1b}`)
		time.Sleep(400 * time.Millisecond)

		r.owHit = hit(`@e[type=minecraft:pig,limit=1]`, 1)
		// `in the_nether` does not narrow the selector: the free @e
		// walks every level and still resolves the overworld pig.
		r.netherHit = hitIn("minecraft:the_nether", `@e[type=minecraft:pig,limit=1]`, 2)
		// The distance-clipped form is world-limited: it searches only
		// the forked level, where the pig is not — the miss pins the
		// narrowing.
		r.netherMiss = missIn("minecraft:the_nether", `@e[type=minecraft:pig,x=40.5,y=-60,z=40.5,distance=..2]`, 3)

		sendCommand(t, pw, "execute in minecraft:the_nether run tp @e[type=minecraft:pig,limit=1] 5.5 70 5.5")
		time.Sleep(600 * time.Millisecond)
		// teleportCrossDimension parked the pig in the nether's
		// unticketed chunk: invisible from every context now.
		r.goneOW = miss(`@e[type=minecraft:pig]`, 4)
		r.goneNether = missIn("minecraft:the_nether", `@e[type=minecraft:pig]`, 5)
		r.goneAny = miss(`@e[type=minecraft:pig,x=5.5,y=70,z=5.5,distance=..2]`, 6)

		sendCommand(t, pw, `execute in minecraft:the_nether run summon minecraft:pig 60.5 -60 60.5 {NoAI:1b}`)
		time.Sleep(600 * time.Millisecond)
		r.summonPark = miss(`@e[type=minecraft:pig]`, 7)

		// The player's own arrival tickets the dimension: parked
		// entities wake and resolve to @e again. The tp'd pig wakes
		// near its parked coords and starts falling — the wide band
		// catches it mid-fall whether or not the ground below is
		// solid (the summoned pig's landing is terrain-dependent on
		// vanilla, so it is not the pin).
		sendCommand(t, pw, "execute in minecraft:the_nether run tp P 5.5 70 5.5")
		time.Sleep(4 * time.Second)
		r.wakeNether = hitIn("minecraft:the_nether", `@e[type=minecraft:pig,x=5.5,y=70,z=5.5,distance=..60]`, 8)
		r.wakeAny = hitIn("minecraft:overworld", `@e[type=minecraft:pig,limit=1]`, 9)

		sendCommand(t, pw, "execute in minecraft:overworld run tp P 40.5 -60 40.5")
		time.Sleep(600 * time.Millisecond)
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)
	if van != mc {
		t.Fatalf("mismatch: vanilla %+v vs mc-go %+v", van, mc)
	}
	if !van.owHit || !van.netherHit || !van.netherMiss ||
		!van.goneOW || !van.goneNether || !van.goneAny || !van.summonPark ||
		!van.wakeNether || !van.wakeAny {
		t.Fatalf("positive legs failed on vanilla: %+v", van)
	}
}
