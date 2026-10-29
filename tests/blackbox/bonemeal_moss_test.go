package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBonemealMossPatchParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		time.Sleep(2 * time.Second)
		sendCommand(t, pw, "tp @s 30 -55 30")
		time.Sleep(2 * time.Second)
		for _, cmd := range []string{
			"tick freeze", "gamerule random_tick_speed 0",
			"fill 18 -61 28 28 -61 38 minecraft:stone",
			"setblock 23 -61 33 minecraft:moss_block",
			"setblock 24 -61 34 minecraft:diamond_block",
			"setblock 22 -61 33 minecraft:dispenser[facing=east]",
			"item replace block 22 -61 33 container.0 with minecraft:bone_meal 2",
			"setblock 21 -61 33 minecraft:redstone_block",
			"setblock 39 -61 33 minecraft:dispenser[facing=east]",
			"setblock 40 -61 33 minecraft:moss_block",
			"setblock 40 -60 33 minecraft:stone",
			"item replace block 39 -61 33 container.0 with minecraft:bone_meal 2",
			"setblock 38 -61 33 minecraft:redstone_block",
		} {
			sendCommand(t, pw, cmd)
		}
		waitDrained(t, pw, sink)
		sink.take()
		sendCommand(t, pw, "tick sprint 6")
		completed := false
		deadline := time.Now().Add(time.Minute)
		for !completed && time.Now().Before(deadline) {
			for _, line := range sink.take() {
				completed = completed || strings.Contains(line, "commands.tick.sprint.report")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !completed {
			t.Fatal("moss dispenser sprint did not finish")
		}
		probes := []string{
			"block 23 -61 33 minecraft:moss_block",
			"block 23 -61 32 minecraft:moss_block",
			"block 23 -61 34 minecraft:moss_block",
			"block 24 -61 32 minecraft:moss_block",
			"block 24 -61 33 minecraft:moss_block",
			"block 24 -61 34 minecraft:diamond_block",
			"block 27 -61 33 minecraft:stone",
			"block 23 -62 33 minecraft:dirt",
			"items block 22 -61 33 container.0 minecraft:bone_meal[count=1]",
			"block 40 -61 33 minecraft:moss_block",
			"block 40 -60 33 minecraft:stone",
			"items block 39 -61 33 container.0 minecraft:bone_meal[count=2]",
		}
		for i, spec := range probes {
			sink.take()
			marker := fmt.Sprintf("BMP_%d_PASS", i)
			sendCommand(t, pw, "execute if "+spec+" run say "+marker)
			sendCommand(t, pw, "say BMP_DONE")
			lines := waitForMarker(t, sink, "BMP_DONE", 10*time.Second)
			if !sawMarker(lines, marker) {
				t.Errorf("%s: missing %s", addr, spec)
			}
		}
		// Provider outcomes are independent per server. Require actual
		// vegetation, while bounding its possible species and volume.
		plants := 0
		for _, name := range []string{"flowering_azalea", "azalea", "moss_carpet", "short_grass", "tall_grass"} {
			plants += countFill(t, pw, sink, "minecraft:"+name, 20, -60, 30, 26, -59, 36)
		}
		if plants < 1 || plants > 90 {
			t.Errorf("%s: expected bounded moss vegetation, got %d blocks", addr, plants)
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
