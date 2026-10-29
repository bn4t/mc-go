package blackbox

import (
	"fmt"
	"testing"
	"time"
)

// A rising dispenser signal schedules its action four game ticks away.
// A constrained water source spreads after five ticks and schedules its
// next spread five ticks later: this also checks deadlines created in a tick.
// Query the world age until every requested step has completed; wall time
// and the early /tick step acknowledgement are not completion barriers.
func TestScheduledDispenserBoundaryParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()
	run := func(addr string) map[string]bool {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		time.Sleep(2 * time.Second)
		sendCommand(t, pw, "tp @s 20 -55 25")
		time.Sleep(2 * time.Second)
		for _, cmd := range []string{
			"tick freeze",
			"fill 19 -60 19 23 -57 21 minecraft:air",
			"setblock 20 -60 20 minecraft:dispenser[facing=east]",
			"item replace block 20 -60 20 container.0 with minecraft:stone 2",
			"setblock 19 -60 20 minecraft:redstone_block",
			"fill 39 -61 19 45 -60 21 minecraft:stone",
			"fill 40 -60 20 44 -60 20 minecraft:air",
			"setblock 40 -60 20 minecraft:water",
		} {
			sendCommand(t, pw, cmd)
		}
		waitDrained(t, pw, sink)
		before := gameTimeOf(t, pw, sink)
		probe := func(spec, key string) bool {
			sink.take()
			marker := "SCHED_" + key + "_PASS"
			sendCommand(t, pw, "execute if "+spec+" run say "+marker)
			sendCommand(t, pw, "say SCHED_DONE")
			lines := waitForMarker(t, sink, "SCHED_DONE", 10*time.Second)
			return sawMarker(lines, marker)
		}
		out := map[string]bool{"dispenser0": probe("items block 20 -60 20 container.0 minecraft:stone[count=2]", "dispenser0")}
		at := int64(0)
		for _, n := range []int64{3, 1, 1, 4, 1} {
			sendCommand(t, pw, fmt.Sprintf("tick step %d", n))
			at += n
			deadline := time.Now().Add(20 * time.Second)
			for gameTimeOf(t, pw, sink) < before+at {
				if time.Now().After(deadline) {
					t.Fatal("stepped game time never reached the requested boundary")
				}
			}
			count := 1
			if at < 4 {
				count = 2
			}
			key := fmt.Sprintf("dispenser%d", at)
			out[key] = probe(fmt.Sprintf("items block 20 -60 20 container.0 minecraft:stone[count=%d]", count), key)
			key = fmt.Sprintf("water1_%d", at)
			state := "air"
			if at >= 5 {
				state = "water[level=1]"
			}
			out[key] = probe("block 41 -60 20 minecraft:"+state, key)
			key = fmt.Sprintf("water2_%d", at)
			state = "air"
			if at >= 10 {
				state = "water[level=2]"
			}
			out[key] = probe("block 42 -60 20 minecraft:"+state, key)
		}
		return out
	}
	want, got := run(targets.vanilla), run(targets.mcgo)
	for key, v := range want {
		t.Logf("%s: vanilla=%v mc-go=%v", key, v, got[key])
		if !v || !got[key] {
			t.Errorf("%s: vanilla=%v mc-go=%v", key, v, got[key])
		}
	}
}
