package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestWaterPotionImpactParity separates water's box/radius branches from the
// effect splash falloff, and observes the face-centered block dowse cross.
func TestWaterPotionImpactParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("WaterPotion")}, nil)
	defer targets.cleanup()
	for _, tc := range []struct{ tag, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "WaterPotion")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			query := func(command string) []eggEvent {
				sendCommand(t, pw, command)
				var out []eggEvent
				for i := 0; i < 40; i++ {
					events := eggDrainFor(t, c, pr, pw, 250*time.Millisecond)
					out = append(out, events...)
					for _, e := range events {
						if e.kind == "chat" && (strings.Contains(e.chat, "commands.data.entity.query") || strings.Contains(e.chat, "arguments.nbtpath.nothing_found") || strings.Contains(e.chat, "commands.execute.conditional.")) {
							return out
						}
					}
				}
				t.Fatalf("query did not complete: %s", command)
				return nil
			}
			number := func(tag, field string) int64 {
				value := int64(-100000)
				for _, e := range query("data get entity @e[tag=" + tag + ",limit=1] " + field) {
					if e.kind == "chat" && strings.Contains(e.chat, "commands.data.entity.query") {
						m := dataNumLooseRe.FindAllStringSubmatch(e.chat, -1)
						if len(m) > 0 {
							v, _ := strconv.ParseFloat(m[len(m)-1][1], 64)
							value = int64(v)
						}
					}
				}
				return value
			}
			for _, command := range []string{
				"tp @s 2.5 -60 2.5 -90 -20", "tick freeze", "fill 5 -61 0 5 -54 8 minecraft:stone",
				"setblock 4 -59 1 minecraft:stone", "setblock 4 -59 2 minecraft:stone", "setblock 4 -59 3 minecraft:stone", "setblock 3 -59 3 minecraft:stone", "setblock 3 -58 1 minecraft:oak_planks",
				"setblock 4 -58 2 minecraft:fire", "setblock 3 -58 2 minecraft:fire", "setblock 3 -58 3 minecraft:fire", "setblock 4 -58 1 minecraft:campfire[lit=true]", "setblock 4 -58 3 minecraft:candle[lit=true]",
				"summon minecraft:villager 4.5 -60 4 {NoAI:1b,NoGravity:1b,Fire:1000s,Tags:[WaterNear]}",
				"summon minecraft:villager 4.5 -54.7 2.5 {NoAI:1b,NoGravity:1b,Fire:1000s,Tags:[WaterHigh]}",
				"summon minecraft:axolotl 3.8 -58 6.4 {NoAI:1b,NoGravity:1b,Tags:[WaterAxCorner]}",
				"summon minecraft:axolotl 8.5 -58 3.5 {NoAI:1b,NoGravity:1b,Tags:[WaterAxCap]}",
				"summon minecraft:dolphin 12 -60 6 {NoAI:1b,NoGravity:1b,Tags:[WaterDolphin]}",
				"summon minecraft:nautilus 12 -60 9 {NoAI:1b,NoGravity:1b,Tags:[WaterNautilus]}",
				"data merge entity @e[tag=WaterNautilus,limit=1] {Air:100s}",
				"data merge entity @e[tag=WaterAxCorner,limit=1] {Air:1000s}", "data merge entity @e[tag=WaterAxCap,limit=1] {Air:5000s}",
				"item replace entity @s weapon.mainhand with minecraft:splash_potion[minecraft:potion_contents='minecraft:water']",
			} {
				sendCommand(t, pw, command)
			}
			ready := false
			for i := 0; i < 40 && !ready; i++ {
				for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
					if e.kind == "chat" && strings.Contains(e.chat, "commands.item.entity.set.success.single") {
						ready = true
					}
				}
			}
			if !ready {
				t.Fatal("water potion replacement did not complete")
			}
			sendUseItemAim(t, pw, 0, 1, -90, -20)
			sendCommand(t, pw, "tick step 20")
			events := eggDrainFor(t, c, pr, pw, 3*time.Second)
			assertThrowSpawn(t, tc.tag, events, "splash_potion")
			assertHasSound(t, tc.tag, events, "block.candle.extinguish")
			camp, splash := -1, -1
			for i, e := range events {
				if e.kind == "level" && e.b0 == 1009 {
					camp = i
				}
				if e.kind == "level" && e.b0 == 2002 {
					splash = i
					if e.data != -13083194 {
						t.Errorf("water color%d", e.data)
					}
				}
			}
			if camp < 0 || splash < 0 || camp >= splash {
				t.Errorf("dowse/splash event order camp=%d splash=%d", camp, splash)
			}
			if v := number("WaterNear", "Fire"); v > 0 || v == -100000 {
				t.Errorf("near burning victim Fire%d, want extinguished", v)
			}
			if v := number("WaterHigh", "Fire"); v <= 0 {
				t.Errorf("vertically excluded victim Fire%d, want still burning", v)
			}
			if v := number("WaterAxCorner", "Air"); v != 2880 {
				t.Errorf("box corner axolotl Air%d, want2880", v)
			}
			if v := number("WaterAxCap", "Air"); v != 6000 {
				t.Errorf("capped axolotl Air%d, want6000", v)
			}
			if v := number("WaterDolphin", "Air"); v != 4800 {
				t.Errorf("dolphin max Air%d, want4800", v)
			}
			if v := number("WaterNautilus", "Air"); v != 180 {
				t.Errorf("NoAI nautilus refill Air%d, want180", v)
			}
			for _, predicate := range []string{"4 -58 2 minecraft:air", "3 -58 2 minecraft:air", "3 -58 3 minecraft:fire", "4 -58 1 minecraft:campfire[lit=false,waterlogged=false]", "4 -58 3 minecraft:candle[lit=false,waterlogged=false]"} {
				pass := false
				for _, e := range query("execute if block " + predicate) {
					if e.kind == "chat" && strings.Contains(e.chat, "commands.execute.conditional.pass") {
						pass = true
					}
				}
				if !pass {
					t.Errorf("water dowse block predicate failed: %s", predicate)
				}
			}
		})
	}
}
