package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestItemBurnParity: lava contact applies Entity.lavaHurt to item
// entities — hurt(lava, 4) once per tick against health 5, so a
// common item burns one tick and dies on the second, while a
// damage_resistant:#is_fire stack (netherite_ingot, ancient_debris)
// is fireImmune and rides the lava forever. Measured on the 26.2 jar:
// ItemEntity.health=5, fireImmune = item.canBeHurtBy(inFire).
func TestItemBurnParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("IB")}, nil)
	defer targets.cleanup()

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "IB")
			defer c.Close()
			sendClientInformation(t, pw)
			sink := newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(400 * time.Millisecond)
			sendCommand(t, pw, "gamerule do_mob_spawning false")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			// Walled pit: stone ring four tall around a one-cell
			// lava pool — the item sits inside the lava cell.
			for _, cmd := range []string{
				"fill 9 -60 9 11 -56 9 minecraft:stone",
				"fill 9 -60 11 11 -56 11 minecraft:stone",
				"fill 9 -60 9 9 -56 11 minecraft:stone",
				"fill 11 -60 9 11 -56 11 minecraft:stone",
				"fill 10 -60 10 10 -60 10 minecraft:lava",
			} {
				sendCommand(t, pw, cmd)
			}
			probe := func(n int) bool {
				sendCommand(t, pw,
					fmt.Sprintf("execute if entity @e[type=minecraft:item,limit=1,sort=nearest] run say BURN-%d", n))
				deadline := time.Now().Add(4 * time.Second)
				for time.Now().Before(deadline) {
					for _, line := range sink.take() {
						if strings.Contains(line, fmt.Sprintf("BURN-%d", n)) {
							return true
						}
					}
					time.Sleep(100 * time.Millisecond)
				}
				return false
			}
			summon := func(item string) {
				sendCommand(t, pw,
					`summon minecraft:item 10.5 -59.8 10.5 `+
						`{Item:{id:"minecraft:`+item+`",count:1}}`)
			}
			// A plain item dies on its second lava tick — too fast
			// to ever probe alive. Assert it is gone after a sprint
			// on both servers.
			summon("stone")
			sendCommand(t, pw, "tick sprint 20")
			time.Sleep(500 * time.Millisecond)
			if probe(1) {
				t.Fatalf("%s: stone item still alive after 20 lava ticks", s.label)
			}
			// The netherite family is fireImmune — verify it landed
			// and is still riding the lava 80 ticks on.
			summon("netherite_ingot")
			if !probe(2) {
				t.Fatalf("%s: netherite_ingot never landed in the lava", s.label)
			}
			sendCommand(t, pw, "tick sprint 80")
			time.Sleep(600 * time.Millisecond)
			if !probe(3) {
				t.Fatalf("%s: netherite_ingot died in lava", s.label)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			time.Sleep(400 * time.Millisecond)
			summon("ancient_debris")
			if !probe(4) {
				t.Fatalf("%s: ancient_debris never landed in the lava", s.label)
			}
			sendCommand(t, pw, "tick sprint 80")
			time.Sleep(600 * time.Millisecond)
			if !probe(5) {
				t.Fatalf("%s: ancient_debris died in lava", s.label)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			time.Sleep(400 * time.Millisecond)
		})
	}
}
