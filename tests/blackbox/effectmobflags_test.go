package blackbox

import (
	"strings"
	"testing"
	"time"
)

// A declined weaker dose still replaces visibility/icon and can clear ambient.
func TestMobWeakerEffectFlagsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("MobFlags")}, nil)
	defer targets.cleanup()
	for _, tc := range []struct{ tag, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "MobFlags")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			target := "@e[tag=MobFlagDose,limit=1]"
			query := func() string {
				sendCommand(t, pw, "data get entity "+target+" active_effects[0]")
				for range 40 {
					for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
						if e.kind == "chat" && strings.Contains(e.chat, "commands.data.entity.query") {
							return visibleChat(e.chat)
						}
					}
				}
				t.Fatal("no active effect NBT response")
				return ""
			}
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "summon minecraft:villager 4 -60 4 {NoAI:1b,NoGravity:1b,Tags:[MobFlagDose],active_effects:[{id:'minecraft:speed',duration:200,amplifier:1,ambient:true,show_particles:false,show_icon:false}]}")
			initial := query()
			if !strings.Contains(initial, "ambient1b") || !strings.Contains(initial, "show_particles0b") || !strings.Contains(initial, "show_icon0b") {
				t.Fatalf("initial hidden ambient dose%s", initial)
			}
			sendCommand(t, pw, "effect give "+target+" minecraft:speed 1 0 false")
			visible := query()
			if strings.Contains(visible, "ambient1b") || strings.Contains(visible, "show_particles0b") || strings.Contains(visible, "show_icon0b") {
				t.Errorf("weaker ordinary visible dose retained hidden/ambient flags%s", visible)
			}
			if !strings.Contains(visible, "amplifier1b") || !strings.Contains(visible, "duration200") {
				t.Errorf("weaker dose changed amplitude/duration%s", visible)
			}
			sendCommand(t, pw, "effect give "+target+" minecraft:speed 1 0 true")
			hidden := query()
			if !strings.Contains(hidden, "show_particles0b") || !strings.Contains(hidden, "show_icon0b") {
				t.Errorf("weaker hidden dose flags%s", hidden)
			}
		})
	}
}
