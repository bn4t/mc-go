package blackbox

import (
	"mc-go/internal/server"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPotionSplashGeometryParity pins the finite vertical and horizontal
// candidate box, separately from the distance falloff inside that box.
func TestPotionSplashGeometryParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("PotionBox")}, nil)
	defer targets.cleanup()
	doses := map[string]int64{}
	for _, tc := range []struct{ tag, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "PotionBox")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sendCommand(t, pw, "tp @s 2.5 -60 2.5 -90 0")
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "fill 5 -61 0 5 -54 8 minecraft:stone")
			sendCommand(t, pw, "summon minecraft:villager 4.5 -60 4 {NoAI:1b,NoGravity:1b,Tags:[PotionNear]}")
			sendCommand(t, pw, "summon minecraft:villager 4.5 -60 7.2 {NoAI:1b,NoGravity:1b,Tags:[PotionFar]}")
			sendCommand(t, pw, "summon minecraft:villager 4.5 -54.5 2.5 {NoAI:1b,NoGravity:1b,Tags:[PotionHigh]}")
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:splash_potion[minecraft:potion_contents=\"minecraft:long_swiftness\"]")
			replaced := false
			for attempt := 0; attempt < 40 && !replaced; attempt++ {
				for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
					if e.kind == "chat" && strings.Contains(e.chat, "commands.item.entity.set.success.single") {
						replaced = true
					}
				}
			}
			if !replaced {
				t.Fatal("potion inventory replacement did not complete")
			}
			sendUseItemAim(t, pw, 0, 1, -90, 10)
			sendCommand(t, pw, "tick step 20")
			events := eggDrainFor(t, c, pr, pw, 3*time.Second)
			assertThrowSpawn(t, tc.tag, events, "splash_potion")
			for _, victim := range []struct {
				tag      string
				affected bool
			}{{"PotionNear", true}, {"PotionFar", false}, {"PotionHigh", false}} {
				sendCommand(t, pw, "data get entity @e[type=minecraft:villager,tag="+victim.tag+",limit=1] active_effects")
				events = eggDrainFor(t, c, pr, pw, time.Second)
				found, reply := false, false
				for _, e := range events {
					if e.kind == "chat" && (strings.Contains(e.chat, "commands.data.entity.query") || strings.Contains(e.chat, "arguments.nbtpath.nothing_found")) {
						reply = true
						found = strings.Contains(e.chat, "minecraft:speed")
					}
				}
				if !reply {
					t.Fatalf("missing data reply for %s: %+v", victim.tag, events)
				}
				if found != victim.affected {
					t.Errorf("%s speed=%v, want %v", victim.tag, found, victim.affected)
				}
				if victim.affected {
					sendCommand(t, pw, "data get entity @e[type=minecraft:villager,tag="+victim.tag+",limit=1] active_effects[0].duration")
					duration := int64(-1)
					for _, e := range eggDrainFor(t, c, pr, pw, time.Second) {
						if e.kind == "chat" && strings.Contains(e.chat, "commands.data.entity.query") {
							ms := dataNumLooseRe.FindAllStringSubmatch(e.chat, -1)
							if len(ms) > 0 {
								v, _ := strconv.ParseFloat(ms[len(ms)-1][1], 64)
								duration = int64(v)
							}
						}
					}
					if duration <= 0 {
						t.Errorf("missing positive AABB falloff dose: %d", duration)
					}
					doses[tc.tag] = duration
					t.Logf("near-target dose %d", duration)
				}
			}
			for i, impact := range []struct {
				x        string
				min, max float64
			}{{"4.5", 4.09, 4.25}, {"5.6", 4.999, 5.001}} {
				// A lingering bottle exposes the exact direct impact position
				// through its cloud spawn. The wall remains behind the victim.
				sendCommand(t, pw, "kill @e[type=minecraft:villager]")
				sendCommand(t, pw, "summon minecraft:villager "+impact.x+" -60 2.5 {NoAI:1b,NoGravity:1b}")
				sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:lingering_potion[minecraft:potion_contents=\"minecraft:long_swiftness\"]")
				replaced = false
				for attempt := 0; attempt < 40 && !replaced; attempt++ {
					for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
						if e.kind == "chat" && strings.Contains(e.chat, "commands.item.entity.set.success.single") {
							replaced = true
						}
					}
				}
				if !replaced {
					t.Fatal("lingering potion replacement did not complete")
				}
				sendUseItemAim(t, pw, 0, int32(2+i), -90, 10)
				sendCommand(t, pw, "tick step 20")
				cloudType, _ := server.EntityTypeID("area_effect_cloud")
				cloudFound := false
				for _, e := range eggDrainFor(t, c, pr, pw, 3*time.Second) {
					if e.kind == "spawn" && e.etype == cloudType {
						cloudFound = true
						if e.sx < impact.min || e.sx > impact.max {
							t.Errorf("target X%s impact cloud X %.6f, want %.3f..%.3f", impact.x, e.sx, impact.min, impact.max)
						}
					}
				}
				if !cloudFound {
					t.Error("missing direct-hit lingering cloud")
				}
			}

		})
	}
	// Shot triangle jitter produces a small independent variation between
	// runs. The previous radius approximation differs by over 1000 ticks.
	if a, ok := doses["vanilla"]; ok {
		if b, ok := doses["mc-go"]; ok && (a-b > 250 || b-a > 250) {
			t.Errorf("dose vanilla=%d mc-go=%d differ by more than shot jitter", a, b)
		}
	}

}
