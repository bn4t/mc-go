package blackbox

import (
	"strings"
	"testing"
	"time"
)

// TestOrdinaryEffectFlagsParity guards the default flag wrapper separately from
// explicitly supplied custom potion flags, including death-protection buffs.
func TestOrdinaryEffectFlagsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("EffectFlags")}, nil)
	defer targets.cleanup()
	for _, tc := range []struct{ tag, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "EffectFlags")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "gamemode survival @s")
			for _, sample := range []struct {
				command string
				flags   int32
				clear   bool
			}{{"effect give @s minecraft:speed 10 0 false", 14, false}, {"effect give @s minecraft:speed 11 0 true", 0, false}, {"effect give @s minecraft:speed 12 0 false", 6, false}, {"effect give @s minecraft:speed 10 0 true", 8, true}} {
				if sample.clear {
					sendCommand(t, pw, "effect clear @s")
					eggDrainFor(t, c, pr, pw, 250*time.Millisecond)
				}
				sendCommand(t, pw, sample.command)
				seen := false
				for _, e := range eggDrainFor(t, c, pr, pw, time.Second) {
					if e.kind == "effect" && e.data == 0 {
						seen = true
						if e.etype != sample.flags {
							t.Errorf("ordinary flags%d want%d", e.etype, sample.flags)
						}
					}
				}
				if !seen {
					t.Errorf("missing effect update for%s", sample.command)
				}
			}
			sendCommand(t, pw, "effect clear @s")
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:totem_of_undying")
			ready := false
			for i := 0; i < 40 && !ready; i++ {
				for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
					if e.kind == "chat" && strings.Contains(e.chat, "commands.item.entity.set.success.single") {
						ready = true
					}
				}
			}
			if !ready {
				t.Fatal("totem replacement did not complete")
			}
			sendCommand(t, pw, "damage @s 100 minecraft:generic")
			events := eggDrainFor(t, c, pr, pw, 2*time.Second)
			buffs := map[int32]struct{ amp, ticks int32 }{9: {1, 900}, 21: {1, 100}, 11: {0, 800}}
			pop := false
			for _, e := range events {
				if e.kind == "event" && e.b0 == 35 {
					pop = true
				}
				if e.kind == "effect" {
					if want, ok := buffs[e.data]; ok {
						if e.b0 != want.amp || e.oval != want.ticks || e.etype != 14 {
							t.Errorf("totem effect%d amp%d duration%d flags%d want%+v/14", e.data, e.b0, e.oval, e.etype, want)
						}
						delete(buffs, e.data)
					}
				}
			}
			if !pop || len(buffs) != 0 {
				t.Errorf("missing totem pop/buffs: pop%v remaining%v", pop, buffs)
			}
		})
	}
}
