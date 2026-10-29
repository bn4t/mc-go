package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMobFallAttributesParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("MobFall")}, nil)
	defer targets.cleanup()
	cases := []struct {
		name, block string
		commands    []string
		health      float64
	}{
		{"safe", "stone", []string{"attribute %s minecraft:safe_fall_distance base set 6"}, 13},
		{"multiplier", "stone", []string{"attribute %s minecraft:fall_damage_multiplier base set 0.5"}, 15},
		{"jump_modifier", "stone", []string{"effect give %s minecraft:jump_boost 60 1 true", "attribute %s minecraft:safe_fall_distance modifier add minecraft:fall_test 1 add_multiplied_total"}, 17},
		{"hay", "hay_block", nil, 18},
		{"honey", "honey_block", nil, 18},
		{"honey_fractional", "honey_block", []string{"attribute %s minecraft:safe_fall_distance base set 3.5"}, 19},
		{"hay_double", "hay_block", []string{"attribute %s minecraft:fall_damage_multiplier base set 2"}, 16},
	}
	run := func(addr string) map[string]float64 {
		conn, pr, pw := joinPlayBlackbox(t, addr, "MobFall")
		defer conn.Close()
		sink := newChatSinkTP(pr, pw)
		await := func(key string) {
			t.Helper()
			until := time.Now().Add(5 * time.Second)
			for time.Now().Before(until) {
				for _, line := range sink.take() {
					if strings.Contains(line, key) {
						return
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("missing command completion %s", key)
		}
		send := func(cmd, key string) { sendCommand(t, pw, cmd); await(key) }
		send("tp @s 0 -55 4", "teleport")
		send("time set 18000", "time")
		send("fill -4 -61 -4 28 -45 4 air", "fill")
		send("fill -4 -62 -4 28 -62 4 stone", "fill")
		out := map[string]float64{}
		for i, tc := range cases {
			x := i * 4
			send(fmt.Sprintf("fill %d -62 -3 %d -62 3 %s", x-1, x+1, tc.block), "fill")
			tag := "fall_" + tc.name
			selector := "@e[tag=" + tag + ",limit=1]"
			send(fmt.Sprintf(`summon zombie %d -48 0 {Tags:["%s"],NoGravity:1b,PersistenceRequired:1b}`, x, tag), "summon")
			for _, slot := range []string{"armor.head", "armor.chest", "armor.legs", "armor.feet"} {
				sendCommand(t, pw, "item replace entity "+selector+" "+slot+" with air")
			}
			for _, command := range tc.commands {
				sendCommand(t, pw, fmt.Sprintf(command, selector))
			}
			send("data merge entity "+selector+" {NoGravity:0b}", "data.entity.modified")
			time.Sleep(1800 * time.Millisecond)
			out[tc.name] = nbtFloatOf(t, sink, pw, selector, "Health")
		}
		return out
	}
	vanilla, mcgo := run(targets.vanilla), run(targets.mcgo)
	for _, tc := range cases {
		if vanilla[tc.name] != tc.health || mcgo[tc.name] != vanilla[tc.name] {
			t.Errorf("%s vanilla=%v mcgo=%v want=%v", tc.name, vanilla[tc.name], mcgo[tc.name], tc.health)
		}
	}
}
