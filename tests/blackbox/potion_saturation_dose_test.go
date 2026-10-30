package blackbox

import (
	"fmt"
	"testing"
	"time"
)

// Direct potion delivery ignores the stored instantaneous duration and duration
// scale. Each fresh player starts with saturation five, so one dose is visible.
func TestSaturationPotionDrinkDoseParity(t *testing.T) {
	names := []string{"SatDrinkZero", "SatDrinkLong", "SatDrinkScale"}
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			for i, c := range []struct {
				duration, amp int
				scale         string
				want          int
			}{{0, 0, "1", 7}, {200, 1, "0", 9}, {200, 0, "0.5", 7}} {
				t.Run(names[i], func(t *testing.T) {
					conn, pr, pw := joinPlayBlackbox(t, leg.addr, names[i])
					defer conn.Close()
					awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
					sink := newChatSinkTP(pr, pw)
					collect := func(marker string) []string {
						var lines []string
						deadline := time.Now().Add(12 * time.Second)
						for !structureSaidBB(lines, marker) && time.Now().Before(deadline) {
							lines = append(lines, sink.take()...)
							time.Sleep(10 * time.Millisecond)
						}
						if !structureSaidBB(lines, marker) {
							t.Fatal("drink barrier absent", marker)
						}
						return lines
					}
					setupPlayerTickBarrierBB(t, pw)
					for _, cmd := range []string{"difficulty normal", "gamerule minecraft:natural_health_regeneration false", "gamemode survival @s"} {
						sendCommand(t, pw, cmd)
					}
					sendCommand(t, pw, `execute if data entity @s {foodLevel:20,foodSaturationLevel:5.0f} run say SATDRINKINITIAL`)
					sendCommand(t, pw, "say SATDRINKREADY")
					if !structureSaidBB(collect("SATDRINKREADY"), "SATDRINKINITIAL") {
						t.Fatal("fresh food pools absent")
					}
					sendCommand(t, pw, fmt.Sprintf(`item replace entity @s weapon.mainhand with minecraft:potion[minecraft:potion_contents={custom_effects:[{id:"minecraft:saturation",duration:%d,amplifier:%d}]},minecraft:potion_duration_scale=%s]`, c.duration, c.amp, c.scale))
					sendCommand(t, pw, "say SATDRINKITEM")
					collect("SATDRINKITEM")
					sendUseItemAim(t, pw, 0, 1, 0, 0)
					waitPlayerTicksBB(t, pw, collect, "SATDRINKCONSUMED", 45)
					sendCommand(t, pw, fmt.Sprintf(`execute if items entity @s weapon.mainhand minecraft:glass_bottle if data entity @s {foodLevel:20,foodSaturationLevel:%d.0f} unless data entity @s {active_effects:[{id:"minecraft:saturation"}]} run say SATDRINKDOSE`, c.want))
					sendCommand(t, pw, "say SATDRINKEND")
					if !structureSaidBB(collect("SATDRINKEND"), "SATDRINKDOSE") {
						t.Errorf("duration%d amp%d scale%s: expected one immediate dose saturation%d, bottle, no active status", c.duration, c.amp, c.scale, c.want)
					}
				})
			}
		})
	}
}
