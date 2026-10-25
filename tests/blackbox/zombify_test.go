// TestOverworldZombifyParity pins AbstractPiglin/Hoglin
// customServerAiStep: a zombifiable mob in the overworld ticks
// TimeInOverworld past 300 and converts (piglin → zombified_piglin,
// hoglin → zoglin), IsImmuneToZombification and NoAI each block it,
// and the counter stays 0 while blocked.
package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOverworldZombifyParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		tioPre                                bool
		convPig, convZog                      bool
		immuneHolds, noaiHolds, immuneTioZero bool
	}
	run := func(addr string) result {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		sink := newChatSink(pr, pw)
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "tp P 40.5 -30 40.5")
		// A 21x21 pen: zombifiable mobs wander on AI, so wall the
		// arena in or they drift out of probe/despawn range.
		sendCommand(t, pw, "fill 36 -61 36 56 -61 56 minecraft:stone")
		sendCommand(t, pw, "fill 35 -61 35 57 -55 35 minecraft:stone")
		sendCommand(t, pw, "fill 35 -61 57 57 -55 57 minecraft:stone")
		sendCommand(t, pw, "fill 35 -61 35 35 -55 57 minecraft:stone")
		sendCommand(t, pw, "fill 57 -61 35 57 -55 57 minecraft:stone")
		probe := func(sel string, i int) bool {
			sendCommand(t, pw, fmt.Sprintf(
				"execute if entity %s run say ZZO-%d", sel, i))
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, fmt.Sprintf("ZZO-%d", i)) {
						return true
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			return false
		}
		killAll := func() {
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			time.Sleep(300 * time.Millisecond)
		}
		var r result

		// Phase 1: plain piglin — timer climbs, type flips past 300.
		killAll()
		sendCommand(t, pw, "summon minecraft:piglin 44.5 -60 44.5")
		time.Sleep(300 * time.Millisecond)
		t.Logf("piglin post-summon: %v", probe("@e[type=minecraft:piglin]", 10))
		sendCommand(t, pw, "tick sprint 200")
		time.Sleep(300 * time.Millisecond)
		tio := dataGetAll(t, sink, pw,
			"@e[type=minecraft:piglin,limit=1] TimeInOverworld")
		r.tioPre = tio != "" && strings.TrimSpace(tio) != "0"
		sendCommand(t, pw, "tick sprint 160")
		time.Sleep(300 * time.Millisecond)
		r.convPig = probe("@e[type=minecraft:zombified_piglin]", 2)
		t.Logf("post-360: zpig=%v pig=%v tio=%q", r.convPig,
			probe("@e[type=minecraft:piglin]", 12), tio)
		killAll()

		// Phase 2: hoglin → zoglin.
		sendCommand(t, pw, "summon minecraft:hoglin 44.5 -60 44.5")
		time.Sleep(300 * time.Millisecond)
		t.Logf("hoglin post-summon: %v", probe("@e[type=minecraft:hoglin]", 11))
		sendCommand(t, pw, "tick sprint 200")
		time.Sleep(300 * time.Millisecond)
		t.Logf("hoglin sprint200: %v tio=%q",
			probe("@e[type=minecraft:hoglin]", 31),
			dataGetAll(t, sink, pw,
				"@e[type=minecraft:hoglin,limit=1] TimeInOverworld"))
		sendCommand(t, pw, "tick sprint 160")
		time.Sleep(300 * time.Millisecond)
		r.convZog = probe("@e[type=minecraft:zoglin]", 3)
		t.Logf("post-360 zog=%v hog=%v anyEnt=%v", r.convZog,
			probe("@e[type=minecraft:hoglin]", 32),
			probe("@e[type=!minecraft:player]", 33))
		killAll()

		// Phase 3: immune piglin never converts and its counter
		// stays 0 (the non-converting branch resets it).
		sendCommand(t, pw, "summon minecraft:piglin 44.5 -60 44.5 {IsImmuneToZombification:1b}")
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, pw, "tick sprint 310")
		time.Sleep(300 * time.Millisecond)
		r.immuneHolds = probe("@e[type=minecraft:piglin]", 4) &&
			!probe("@e[type=minecraft:zombified_piglin]", 5)
		r.immuneTioZero = strings.TrimSpace(dataGetAll(t, sink, pw,
			"@e[type=minecraft:piglin,limit=1] TimeInOverworld")) == "0"
		killAll()

		// Phase 4: NoAI blocks conversion too.
		sendCommand(t, pw, "summon minecraft:piglin 44.5 -60 44.5 {NoAI:1b}")
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, pw, "tick sprint 310")
		time.Sleep(300 * time.Millisecond)
		r.noaiHolds = probe("@e[type=minecraft:piglin]", 7) &&
			!probe("@e[type=minecraft:zombified_piglin]", 8)
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	if van != mc {
		t.Errorf("vanilla %+v vs mc-go %+v", van, mc)
	}
	// Non-vacuous: vanilla must convert and hold both gates.
	if !van.tioPre || !van.convPig || !van.convZog ||
		!van.immuneHolds || !van.noaiHolds || !van.immuneTioZero {
		t.Errorf("vanilla leg wrong: %+v", van)
	}
}
