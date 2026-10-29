package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// Player effects keep ticking when the world and non-player effects freeze.
func TestPlayerFrozenEffectTimeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("FrozenEffects")}, nil)
	defer targets.cleanup()
	for _, tc := range []struct{ tag, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "FrozenEffects")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			number := func(target, path string) int64 {
				sendCommand(t, pw, "data get entity "+target+" "+path)
				for range 40 {
					for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
						if e.kind != "chat" {
							continue
						}
						if strings.Contains(e.chat, "arguments.nbtpath.nothing_found") {
							return -1
						}
						if strings.Contains(e.chat, "commands.data.entity.query") {
							m := dataNumLooseRe.FindAllStringSubmatch(e.chat, -1)
							if len(m) > 0 {
								v, _ := strconv.ParseFloat(m[len(m)-1][1], 64)
								return int64(v)
							}
						}
					}
				}
				t.Fatalf("no query response for%s%s", target, path)
				return -2
			}
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "summon minecraft:villager 4 -60 4 {NoAI:1b,NoGravity:1b,Tags:[FrozenEffectMob]}")
			mob := "@e[tag=FrozenEffectMob,limit=1]"
			sendCommand(t, pw, "effect give "+mob+" minecraft:speed 10 0")
			sendCommand(t, pw, "effect give @s minecraft:speed 10 0")
			before := number("@s", "active_effects[0].duration")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			after := number("@s", "active_effects[0].duration")
			npc := number(mob, "active_effects[0].duration")
			t.Logf("frozen player remaining%d->%d; NPC%d", before, after, npc)
			if before-after < 20 || before-after > 70 {
				t.Errorf("frozen player duration delta%d want20..70", before-after)
			}
			if npc != 200 {
				t.Errorf("frozen NPC remaining%d want200", npc)
			}
			before = number("@s", "active_effects[0].duration")
			sendCommand(t, pw, "tick step 20")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)
			after = number("@s", "active_effects[0].duration")
			npc = number(mob, "active_effects[0].duration")
			t.Logf("stepped20 plus wallwait player%d->%d; NPC%d", before, after, npc)
			if npc != 180 {
				t.Errorf("stepped NPC remaining%d want180", npc)
			}
			sendCommand(t, pw, "effect clear @s")
			sendCommand(t, pw, "effect give @s minecraft:speed 10 0 false")
			sendCommand(t, pw, "effect give @s minecraft:speed 1 1 false")
			events := eggDrainFor(t, c, pr, pw, 2*time.Second)
			resumed := false
			for _, e := range events {
				if e.kind == "effect" && e.data == 0 && e.b0 == 0 && e.etype == 6 {
					resumed = true
				}
			}
			if !resumed {
				t.Error("hidden player effect did not resume during freeze with updateflags6")
			}
			if amp := number("@s", "active_effects[0].amplifier"); amp != 0 && amp != -1 {
				t.Errorf("frozen player amplifier%d want0", amp)
			}
		})
	}
}
