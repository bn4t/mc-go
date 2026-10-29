package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCustomPotionPayloadParity observes optional potion fields through the
// item command, its inventory/entity wire payload, splash color, and dose NBT.
func TestCustomPotionPayloadParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("PotionPayload")}, nil)
	defer targets.cleanup()
	for _, tc := range []struct{ tag, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "PotionPayload")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sendCommand(t, pw, "tp @s 2.5 -60 2.5 -90 0")
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "fill 5 -61 0 5 -54 5 minecraft:stone")
			sendCommand(t, pw, "summon minecraft:villager 4.5 -60 4 {NoAI:1b,NoGravity:1b,Tags:[CustomDose]}")
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:splash_potion[minecraft:potion_contents={custom_color:1193046,custom_name:'probe',custom_effects:[{id:'minecraft:speed',duration:800,amplifier:2,ambient:true,show_particles:false,show_icon:true}]},minecraft:potion_duration_scale=0.5]")
			ready := false
			for i := 0; i < 40 && !ready; i++ {
				for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
					if e.kind == "chat" && strings.Contains(e.chat, "commands.item.entity.set.success.single") {
						ready = true
					}
				}
			}
			if !ready {
				t.Fatal("custom potion command did not complete")
			}
			sendUseItemAim(t, pw, 0, 1, -90, 10)
			sendCommand(t, pw, "tick step 20")
			events := eggDrainFor(t, c, pr, pw, 3*time.Second)
			assertThrowSpawn(t, tc.tag, events, "splash_potion")
			colored, effectWire := false, false
			for _, e := range events {
				if e.kind == "effect" && e.data == 0 && e.b0 == 2 {
					effectWire = true
					if e.etype != 9 {
						t.Errorf("custom splash effect flags %d, want9", e.etype)
					}
				}
				if e.kind == "level" && e.b0 == 2002 {
					colored = true
					if e.data != 1193046 {
						t.Errorf("custom splash color %d", e.data)
					}
				}
			}
			if !effectWire {
				t.Error("missing custom splash player effect packet")
			}
			if !colored {
				t.Error("missing custom splash event")
			}
			sendCommand(t, pw, "data get entity @e[type=minecraft:villager,tag=CustomDose,limit=1] active_effects")
			found := false
			for _, e := range eggDrainFor(t, c, pr, pw, time.Second) {
				if e.kind == "chat" && strings.Contains(e.chat, "commands.data.entity.query") && strings.Contains(e.chat, "minecraft:speed") {
					found = true
					// NBT styled text emits punctuation as unnamed string nodes,
					// while visibleChat keeps named text leaves (field/value pairs).
					text := strings.ReplaceAll(visibleChat(e.chat), " ", "")
					for _, field := range []string{"ambient1b", "show_particles0b", "show_icon0b"} {
						if !strings.Contains(text, field) {
							t.Errorf("missing custom effect flag %s in%s", field, text)
						}
					}
					if !strings.Contains(strings.ReplaceAll(visibleChat(e.chat), " ", ""), "amplifier2b") {
						t.Errorf("missing custom amplifier: %s", visibleChat(e.chat))
					}
				}
			}
			if !found {
				t.Error("custom speed dose absent")
			}
			sendCommand(t, pw, "data get entity @e[type=minecraft:villager,tag=CustomDose,limit=1] active_effects[0].duration")
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
			if duration < 200 || duration > 350 {
				t.Errorf("half-scale custom dose %d, want200..350", duration)
			}
			sendCommand(t, pw, "effect clear @e[type=minecraft:villager,tag=CustomDose]")
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:splash_potion[minecraft:potion_contents={custom_effects:[{id:'minecraft:speed',duration:20}]}]")
			ready = false
			for i := 0; i < 40 && !ready; i++ {
				for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
					if e.kind == "chat" && strings.Contains(e.chat, "commands.item.entity.set.success.single") {
						ready = true
					}
				}
			}
			if !ready {
				t.Fatal("short potion command did not complete")
			}
			sendUseItemAim(t, pw, 0, 2, -90, 10)
			sendCommand(t, pw, "tick step 20")
			eggDrainFor(t, c, pr, pw, 3*time.Second)
			sendCommand(t, pw, "data get entity @e[type=minecraft:villager,tag=CustomDose,limit=1] active_effects")
			for _, e := range eggDrainFor(t, c, pr, pw, time.Second) {
				if e.kind == "chat" && strings.Contains(e.chat, "commands.data.entity.query") && strings.Contains(e.chat, "minecraft:speed") {
					t.Errorf("20tick-or-shorter splash dose applied: %s", e.chat)
				}
			}

			sendCommand(t, pw, "effect clear @e[type=minecraft:villager,tag=CustomDose]")
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:lingering_potion[minecraft:potion_contents={potion:'minecraft:strong_swiftness',custom_effects:[{id:'minecraft:speed',duration:2400,ambient:true,show_particles:false,show_icon:false}]}]")
			ready = false
			for i := 0; i < 40 && !ready; i++ {
				for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
					if e.kind == "chat" && strings.Contains(e.chat, "commands.item.entity.set.success.single") {
						ready = true
					}
				}
			}
			if !ready {
				t.Fatal("mixed lingering potion command did not complete")
			}
			sendUseItemAim(t, pw, 0, 3, -90, 10)
			sendCommand(t, pw, "tick step 30")
			eggDrainFor(t, c, pr, pw, 3*time.Second)
			sendCommand(t, pw, "data get entity @e[type=minecraft:villager,tag=CustomDose,limit=1] active_effects[0].amplifier")
			amplifier := int64(-1)
			for _, e := range eggDrainFor(t, c, pr, pw, time.Second) {
				if e.kind == "chat" && strings.Contains(e.chat, "commands.data.entity.query") {
					ms := dataNumLooseRe.FindAllStringSubmatch(e.chat, -1)
					if len(ms) > 0 {
						v, _ := strconv.ParseFloat(ms[len(ms)-1][1], 64)
						amplifier = int64(v)
					}
				}
			}
			if amplifier != 1 {
				t.Errorf("base/custom duplicate cloud amplifier %d, want1", amplifier)
			}
			sendCommand(t, pw, "data get entity @e[type=minecraft:villager,tag=CustomDose,limit=1] active_effects[0].show_particles")
			visibility := int64(-1)
			for _, e := range eggDrainFor(t, c, pr, pw, time.Second) {
				if e.kind == "chat" && strings.Contains(e.chat, "commands.data.entity.query") {
					ms := dataNumLooseRe.FindAllStringSubmatch(e.chat, -1)
					if len(ms) > 0 {
						v, _ := strconv.ParseFloat(ms[len(ms)-1][1], 64)
						visibility = int64(v)
					}
				}
			}
			if visibility != 0 {
				t.Errorf("weaker custom dose did not update visibility: %d", visibility)
			}

			sendCommand(t, pw, "summon minecraft:villager 12 -60 2 {NoAI:1b,NoGravity:1b,Tags:[DirectCustomDose]}")
			sendCommand(t, pw, "summon minecraft:area_effect_cloud 12 -60 2 {Radius:1f,Duration:100,WaitTime:0,RadiusOnUse:0f,potion_contents:{custom_effects:[{id:'minecraft:speed',duration:200,hidden_effect:{duration:100}}]}}")
			eggDrainFor(t, c, pr, pw, time.Second)
			sendCommand(t, pw, "tick step 5")
			eggDrainFor(t, c, pr, pw, time.Second)
			sendCommand(t, pw, "data get entity @e[type=minecraft:villager,tag=DirectCustomDose,limit=1] active_effects[0].duration")
			duration = -1
			for _, e := range eggDrainFor(t, c, pr, pw, time.Second) {
				if e.kind == "chat" && strings.Contains(e.chat, "commands.data.entity.query") {
					ms := dataNumLooseRe.FindAllStringSubmatch(e.chat, -1)
					if len(ms) > 0 {
						v, _ := strconv.ParseFloat(ms[len(ms)-1][1], 64)
						duration = int64(v)
					}
				}
			}
			if duration < 195 || duration > 200 {
				t.Errorf("summoned custom cloud dose %d, want195..200", duration)
			}

		})
	}
}

// TestCustomPotionDrinkParity pins the consumable arm separately: unlike a
// splash dose, drinking preserves the item's explicit icon flag while dropping
// hidden details through withScaledDuration's scalar copy.
func TestCustomPotionDrinkParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("PotionDrink")}, nil)
	defer targets.cleanup()
	for _, tc := range []struct{ tag, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "PotionDrink")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:potion[minecraft:potion_contents={custom_effects:[{id:'minecraft:speed',duration:800,amplifier:2,ambient:true,show_particles:false,show_icon:true,hidden_effect:{duration:100}}]},minecraft:potion_duration_scale=0.5]")
			ready := false
			for i := 0; i < 40 && !ready; i++ {
				for _, e := range eggDrainFor(t, c, pr, pw, 250*time.Millisecond) {
					if e.kind == "chat" && strings.Contains(e.chat, "commands.item.entity.set.success.single") {
						ready = true
					}
				}
			}
			if !ready {
				t.Fatal("drink item replacement did not complete")
			}
			sendUseItemAim(t, pw, 0, 1, 0, 0)
			sendCommand(t, pw, "tick step 40")
			applied := false
			for _, e := range eggDrainFor(t, c, pr, pw, 3*time.Second) {
				if e.kind == "effect" && e.data == 0 && e.b0 == 2 {
					applied = true
					if e.etype != 13 || e.oval != 400 {
						t.Errorf("drink effect flags=%d duration=%d, want13/400", e.etype, e.oval)
					}
				}
			}
			if !applied {
				t.Error("missing consumed custom effect packet")
			}
			sendCommand(t, pw, "data get entity @s active_effects[0].duration")
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
			// Players keep ticking while vanilla freezes world entities. The
			// packet above pins the exact immutable400tick dose.
			if duration <= 0 || duration > 400 {
				t.Errorf("drink remaining duration %d, want1..400", duration)
			}
			sendCommand(t, pw, "data get entity @s active_effects[0].hidden_effect")
			absent := false
			for _, e := range eggDrainFor(t, c, pr, pw, time.Second) {
				if e.kind == "chat" && strings.Contains(e.chat, "arguments.nbtpath.nothing_found") {
					absent = true
				}
				if e.kind == "chat" && strings.Contains(e.chat, "commands.data.entity.query") {
					t.Error("consumed potion retained hidden item details")
				}
			}
			if !absent {
				t.Error("missing absent hidden_effect reply")
			}
		})
	}
}
