package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Dispensers avoid the underwater-use spread fallback and isolate each
// BonemealableBlock target gate and its consumption result.
func TestBonemealDoublePlantParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()
	cases := []struct{ plant, above, grown string }{
		{"seagrass", "water", "tall_seagrass"},
		{"seagrass", "water[level=3]", "tall_seagrass"},
		{"seagrass", "oak_slab[type=bottom,waterlogged=true]", ""},
		{"seagrass", "stone", ""},
		{"short_grass", "air", "tall_grass"},
		{"fern", "air", "large_fern"},
		{"short_grass", "stone", ""},
		{"fern", "short_grass", ""},
	}
	run := func(addr string) []bool {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		time.Sleep(2 * time.Second)
		sendCommand(t, pw, "tp @s 30 -55 20")
		time.Sleep(2 * time.Second)
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "gamerule random_tick_speed 0")
		for i, tc := range cases {
			x := 20 + i*3
			// A walled two-cell tank keeps flowing water available without
			// letting scheduled fluid updates alter other probes.
			for _, cmd := range []string{
				fmt.Sprintf("fill %d -61 29 %d -58 31 minecraft:stone", x-1, x+1),
				fmt.Sprintf("setblock %d -61 30 minecraft:grass_block", x),
				fmt.Sprintf("setblock %d -60 30 minecraft:%s", x, tc.plant),
				fmt.Sprintf("setblock %d -59 30 minecraft:%s", x, tc.above),
				fmt.Sprintf("setblock %d -60 30 minecraft:dispenser[facing=east]", x-1),
				fmt.Sprintf("item replace block %d -60 30 container.0 with minecraft:bone_meal 2", x-1),
				fmt.Sprintf("setblock %d -60 30 minecraft:redstone_block", x-2),
			} {
				sendCommand(t, pw, cmd)
			}
		}
		waitDrained(t, pw, sink)
		sink.take()
		sendCommand(t, pw, "tick sprint 6")
		finished := false
		deadline := time.Now().Add(60 * time.Second)
		for !finished && time.Now().Before(deadline) {
			for _, line := range sink.take() {
				finished = finished || strings.Contains(line, "commands.tick.sprint.report")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !finished {
			t.Fatal("dispenser sprint did not complete")
		}
		var out []bool
		for i, tc := range cases {
			x := 20 + i*3
			name, count := tc.plant, 2
			if tc.grown != "" {
				name, count = tc.grown+"[half=lower]", 1
			}
			probes := []string{
				fmt.Sprintf("block %d -60 30 minecraft:%s", x, name),
				fmt.Sprintf("items block %d -60 30 container.0 minecraft:bone_meal[count=%d]", x-1, count),
			}
			if tc.grown != "" {
				probes = append(probes, fmt.Sprintf("block %d -59 30 minecraft:%s[half=upper]", x, tc.grown))
			}
			for j, spec := range probes {
				marker := fmt.Sprintf("BDP%d_%d", i, j)
				sink.take()
				sendCommand(t, pw, "execute if "+spec+" run say "+marker)
				sendCommand(t, pw, "say DONE"+marker)
				hit := false
				done := false
				deadline := time.Now().Add(8 * time.Second)
				for !done && time.Now().Before(deadline) {
					for _, line := range sink.take() {
						if strings.Contains(line, "DONE"+marker) {
							done = true
						} else if strings.Contains(line, marker) {
							hit = true
						}
					}
					time.Sleep(20 * time.Millisecond)
				}
				out = append(out, hit)
			}
		}
		return out
	}
	want, got := run(targets.vanilla), run(targets.mcgo)
	for i := range want {
		if !want[i] || !got[i] {
			t.Errorf("probe %d: vanilla=%v mc-go=%v; both must pass", i, want[i], got[i])
		}
	}
}
