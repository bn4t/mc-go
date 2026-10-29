package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/server"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPlayerPotionThrowParity observes vanilla 26.2 item use: both potion
// entities carry their owner and item, consume in survival, splash on impact,
// and only the lingering kind leaves an area-effect cloud.
func TestPlayerPotionThrowParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("PotionThrow")}, nil)
	defer targets.cleanup()
	for _, tc := range []struct{ tag, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "PotionThrow")
			defer c.Close()
			query := func(command string) []eggEvent {
				sendCommand(t, pw, command)
				var out []eggEvent
				for attempt := 0; attempt < 16; attempt++ {
					events := eggDrainFor(t, c, pr, pw, 250*time.Millisecond)
					out = append(out, events...)
					for _, e := range events {
						if e.kind == "chat" && (strings.Contains(e.chat, "commands.data.entity.query") || strings.Contains(e.chat, "arguments.nbtpath.nothing_found")) {
							return out
						}
					}
				}
				t.Fatalf("data command did not complete: %s", command)
				return nil
			}

			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
			sendCommand(t, pw, "tp @s 2.5 -60 2.5 -90 0")
			sendCommand(t, pw, "fill 5 -61 0 5 -57 5 minecraft:stone")
			eggDrainFor(t, c, pr, pw, time.Second)
			for i, kind := range []string{"splash_potion", "lingering_potion"} {
				sendCommand(t, pw, "effect clear @s")
				sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:"+kind+"[minecraft:potion_contents=\"minecraft:long_swiftness\"]")
				replaced := false
				for attempts := 0; attempts < 12 && !replaced; attempts++ {
					for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
						if e.kind == "chat" && strings.Contains(e.chat, "commands.item.entity.set.success.single") && strings.Contains(e.chat, "minecraft:"+kind) && strings.Contains(e.chat, "minecraft:long_swiftness") {
							replaced = true
						}
					}
				}
				if !replaced {
					t.Fatal("potion inventory replacement did not complete")
				}
				sendUseItemAim(t, pw, 0, int32(i+1), -90, 10)
				ev := eggDrainFor(t, c, pr, pw, 4*time.Second)
				assertHasAck(t, tc.tag, ev, int32(i+1))
				assertHasSound(t, tc.tag, ev, "entity."+kind+".throw")
				soundID, _ := server.SoundEventID("entity." + kind + ".throw")
				wantCategory := int32(7)
				if kind == "lingering_potion" {
					wantCategory = 6
				}
				for _, e := range ev {
					if e.kind == "sound" && e.eid == soundID+1 && e.b0 != wantCategory {
						t.Errorf("%s throw sound category %d, want %d", kind, e.b0, wantCategory)
					}
				}
				effectFlags := false
				for _, event := range ev {
					if event.kind == "effect" && event.data == 0 {
						effectFlags = true
						if event.etype != 14 {
							t.Errorf("registered %s speed flags%d, want14", kind, event.etype)
						}
					}
				}
				if !effectFlags {
					t.Errorf("registered %s missing speed effect packet", kind)
				}
				eid := assertThrowSpawn(t, tc.tag, ev, kind)
				assertHasMeta(t, tc.tag, ev, eid)
				assertHasSlotShrink(t, tc.tag, ev)
				assertRemoved(t, tc.tag, ev, eid)
				cloudType, _ := server.EntityTypeID("area_effect_cloud")
				cloud, splash := false, false
				var cloudX, cloudY, cloudZ float64
				for _, e := range ev {
					if e.kind == "spawn" && e.etype == cloudType {
						cloud = true
						cloudX, cloudY, cloudZ = e.sx, e.sy, e.sz
					}
					if e.kind == "level" && e.b0 == 2002 {
						splash = true
						if e.data != -13374465 {
							t.Errorf("splash color %d", e.data)
						}
					}
				}
				if !splash {
					t.Error("missing splash level event")
				}
				effectWire := query("data get entity @s active_effects")
				speed := false
				for _, e := range effectWire {
					if e.kind == "chat" && strings.Contains(e.chat, "speed") {
						speed = true
					}
				}
				if !speed {
					t.Logf("cloud location %.4f %.4f %.4f effects %+v", cloudX, cloudY, cloudZ, effectWire)
					t.Errorf("%s did not apply speed to nearby thrower", kind)
				}
				if cloud != (kind == "lingering_potion") {
					t.Errorf("%s cloud=%v", kind, cloud)
				}
				if kind == "lingering_potion" && cloud {
					sendCommand(t, pw, "tp @s 12 -60 2")
					eggDrainFor(t, c, pr, pw, 300*time.Millisecond)
					sendCommand(t, pw, "tick sprint 100")
					report := false
					for attempt := 0; attempt < 20 && !report; attempt++ {
						for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
							if e.kind == "chat" && strings.Contains(e.chat, "commands.tick.sprint.report") {
								report = true
							}
						}
					}
					if !report {
						t.Fatal("missing tick sprint completion")
					}
					sendCommand(t, pw, "tick freeze")
					eggDrainFor(t, c, pr, pw, 300*time.Millisecond)
					sendCommand(t, pw, fmt.Sprintf("summon minecraft:villager %.5f %.5f %.5f {NoAI:1b,NoGravity:1b,Tags:[PotionDose]}", cloudX-0.8, cloudY, cloudZ))
					sendCommand(t, pw, fmt.Sprintf("summon minecraft:villager %.5f %.5f %.5f {NoAI:1b,NoGravity:1b,Tags:[PotionHigh]}", cloudX-0.8, cloudY+3, cloudZ))
					sendCommand(t, pw, "tick step 5")
					eggDrainFor(t, c, pr, pw, 600*time.Millisecond)
					duration := int64(-1)
					doseReplies := query("data get entity @e[type=minecraft:villager,tag=PotionDose,limit=1] active_effects[0].duration")
					for _, e := range doseReplies {
						if e.kind == "chat" && strings.Contains(e.chat, "query") {
							ms := dataNumLooseRe.FindAllStringSubmatch(e.chat, -1)
							if len(ms) > 0 {
								v, err := strconv.ParseFloat(ms[len(ms)-1][1], 64)
								if err == nil {
									duration = int64(v)
								}
							}
						}
					}
					if duration < 2395 || duration > 2400 {
						t.Logf("dose replies %+v", doseReplies)
						t.Errorf("late cloud dose duration %d, want 2395..2400", duration)
					}
					for _, e := range query("data get entity @e[type=minecraft:villager,tag=PotionHigh,limit=1] active_effects") {
						if e.kind == "chat" && strings.Contains(e.chat, "speed") {
							t.Error("cloud dosed elevated victim")
						}
					}
					// Instant cloud effects use a half dose, without a stored status.
					for j, instant := range []struct {
						name   string
						health int64
					}{{"instant_health", 12}, {"instant_damage", 7}} {
						tag := fmt.Sprintf("PotionInstant%d", j)
						x := 16 + j*10
						sendCommand(t, pw, fmt.Sprintf("summon minecraft:villager %d -60 2 {NoAI:1b,NoGravity:1b,Health:10f,Tags:[%s]}", x, tag))
						sendCommand(t, pw, fmt.Sprintf("summon minecraft:area_effect_cloud %d -60 2 {Radius:1f,Duration:100,WaitTime:0,RadiusOnUse:0f,potion_contents:{custom_effects:[{id:'minecraft:%s',duration:1}]}}", x, instant.name))
						sendCommand(t, pw, "tick step 5")
						eggDrainFor(t, c, pr, pw, 500*time.Millisecond)
						health := int64(-1)
						for _, e := range query(fmt.Sprintf("data get entity @e[type=minecraft:villager,tag=%s,limit=1] Health", tag)) {
							if e.kind == "chat" && strings.Contains(e.chat, "query") {
								ms := dataNumLooseRe.FindAllStringSubmatch(e.chat, -1)
								if len(ms) > 0 {
									v, err := strconv.ParseFloat(ms[len(ms)-1][1], 64)
									if err == nil {
										health = int64(v)
									}
								}
							}
						}
						if health != instant.health {
							t.Errorf("cloud %s health=%d, want %d", instant.name, health, instant.health)
						}
					}
					sendCommand(t, pw, "tick unfreeze")
				}

			}
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			sendCommand(t, pw, "effect clear @s")
			sendCommand(t, pw, "tp @s 12 -60 2")
			eggDrainFor(t, c, pr, pw, 500*time.Millisecond)
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "damage @s 10 minecraft:generic")
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:lingering_potion[minecraft:potion_contents=\"minecraft:strong_healing\"]")
			eggDrainFor(t, c, pr, pw, 500*time.Millisecond)
			sendUseItemAim(t, pw, 0, 3, 0, 90)
			frozen := eggDrainFor(t, c, pr, pw, 500*time.Millisecond)
			assertThrowSpawn(t, tc.tag, frozen, "lingering_potion")
			for _, e := range frozen {
				if e.kind == "spawn" {
					if math.Abs(e.motion[0]) > .04 || math.Abs(e.motion[2]) > .04 || e.motion[1] < -0.55 || e.motion[1] > -0.45 {
						t.Errorf("vertical potion motion (%.4f,%.4f,%.4f)", e.motion[0], e.motion[1], e.motion[2])
					}
				}
			}
			sendCommand(t, pw, "tick step 20")
			impact := eggDrainFor(t, c, pr, pw, 1400*time.Millisecond)
			instantEvent := false
			for _, e := range impact {
				if e.kind == "level" && e.b0 == 2007 {
					instantEvent = true
					if e.data != -515037 {
						t.Errorf("strong healing color%d", e.data)
					}
				}
			}
			if !instantEvent {
				t.Error("strong healing missing instant splash event2007")
			}
			health := float64(-1)
			for _, e := range query("data get entity @s Health") {
				if e.kind == "chat" && strings.Contains(e.chat, "query") {
					ms := dataNumLooseRe.FindAllStringSubmatch(e.chat, -1)
					if len(ms) > 0 {
						health, _ = strconv.ParseFloat(ms[len(ms)-1][1], 64)
					}
				}
			}
			if health != 14 {
				t.Errorf("strong healing lingering dose health %.1f, want14", health)
			}
			sendCommand(t, pw, "tick unfreeze")
		})
	}
}
