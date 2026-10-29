package blackbox

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestLiveHiddenEffectLoadParity observes the save/data codec's id-less hidden
// Details, followed by a live downgrade, and unrestricted int32 potion color.
func TestLiveHiddenEffectLoadParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("HiddenLoad")}, nil)
	defer targets.cleanup()
	for _, tc := range []struct{ tag, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "HiddenLoad")
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
						if e.kind == "chat" && (strings.Contains(e.chat, "commands.data.entity.query") || strings.Contains(e.chat, "arguments.nbtpath.nothing_found")) {
							return out
						}
					}
				}
				t.Fatalf("query timeout%s", command)
				return nil
			}
			number := func(path string) int64 {
				n := int64(-1000)
				for _, e := range query("data get entity @e[tag=HiddenDose,limit=1] " + path) {
					if e.kind == "chat" && strings.Contains(e.chat, "commands.data.entity.query") {
						matches := dataNumLooseRe.FindAllStringSubmatch(e.chat, -1)
						if len(matches) > 0 {
							v, _ := strconv.ParseFloat(matches[len(matches)-1][1], 64)
							n = int64(v)
						}
					}
				}
				return n
			}
			sendCommand(t, pw, "tp @s 2.5 -60 2.5 -90 0")
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "fill 5 -61 0 5 -54 5 minecraft:stone")
			sendCommand(t, pw, "summon minecraft:villager 4 -60 4 {NoAI:1b,NoGravity:1b,Tags:[HiddenDose]}")
			sendCommand(t, pw, "data merge entity @e[tag=HiddenDose,limit=1] {active_effects:[{id:'minecraft:speed',duration:40,amplifier:1,hidden_effect:{duration:200,ambient:true,show_particles:false,show_icon:false}}]}")
			if n := number("active_effects[0].hidden_effect.duration"); n != 200 {
				t.Errorf("loaded hidden duration%d want200", n)
			}
			sendCommand(t, pw, "tick step 45")
			eggDrainFor(t, c, pr, pw, 3*time.Second)
			if n := number("active_effects[0].duration"); n != 155 {
				t.Errorf("downgraded duration%d want155", n)
			}
			if n := number("active_effects[0].show_particles"); n != 0 {
				t.Errorf("downgraded visible%d want0", n)
			}
			for i, color := range []int32{-1, 16777216, 2147483647} {
				sendCommand(t, pw, fmt.Sprintf("item replace entity @s weapon.mainhand with minecraft:splash_potion[minecraft:potion_contents={custom_color:%d,custom_effects:[{id:'minecraft:instant_health'}]}]", color))
				ready := false
				for j := 0; j < 40 && !ready; j++ {
					for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
						if e.kind == "chat" && strings.Contains(e.chat, "commands.item.entity.set.success.single") {
							ready = true
						}
					}
				}
				if !ready {
					t.Fatalf("int32 color%d rejected", color)
				}
				sendUseItemAim(t, pw, 0, int32(i+1), -90, 10)
				sendCommand(t, pw, "tick step 20")
				seen := false
				for _, e := range eggDrainFor(t, c, pr, pw, 3*time.Second) {
					if e.kind == "level" && (e.b0 == 2002 || e.b0 == 2007) {
						seen = true
						if e.data != color || e.b0 != 2002 {
							t.Errorf("custom instant color/event %d/%d want%d/2002", e.data, e.b0, color)
						}
					}
				}
				if !seen {
					t.Error("missing int32 custom color splash")
				}
			}
		})
	}
}
