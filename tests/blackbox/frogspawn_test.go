// FrogspawnBlock scheduled-tick parity vs the vanilla 26.2 server,
// measured off server-26.2.jar.
//
// Vanilla's frogspawn does NOT random-tick — onPlace arms a
// scheduleTick at getFrogspawnHatchDelay = random.nextInt(3600,
// 12000), and the due tick re-checks canSurvive (water fluid or
// #supports_frogspawn below, own cell fluid-free): fail →
// destroyBlock (2001 + air), pass → hatchFrogspawn = destroyBlock(no
// drops) + block.frogspawn.hatch (BLOCKS 1.0/1.0) + nextInt(2,6)
// tadpoles snapped to (x+clamp(nextDouble,0.2,0.8), y-0.5,
// z+clamp(...)), yaw nextInt(1,361), persistenceRequired.
package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestFrogspawnHatchParity pins the scheduled hatch end to end: an
// egg over water hatches into 2-5 persistent tadpoles inside the
// water below, while an egg over stone pops with nothing spawned.
// A 13000-tick sprint covers the full 3600-12000 hatch-delay range
// on both legs regardless of each side's roll.
func TestFrogspawnHatchParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"simulation-distance": "3",
	}, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) {
		s.SimulationDistance = 3
	})
	defer targets.cleanup()

	setup := []string{
		`gamerule random_tick_speed 0`,
		// Stone floor keeps the water pad put; the water pad at -62
		// floats the egg at -61, whose tadpoles spawn at -61.5 inside
		// the water. The stone egg sits on dry stone 14 cells out —
		// water spread (max 7) can't reach it.
		`fill 98 -63 98 118 -63 118 minecraft:stone`,
		`fill 99 -62 99 104 -62 104 minecraft:water`,
		`setblock 101 -61 101 minecraft:frogspawn`,
		`setblock 116 -62 116 minecraft:frogspawn`,
		`tp @s 105.5 -59 105.5`,
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZP-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZP-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZPK%d", i))
		marker := fmt.Sprintf("ZPK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZP-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZP-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	entProbe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if entity %s run say ZQ-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless entity %s run say ZQ-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZQK%d", i))
		marker := fmt.Sprintf("ZQK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZQ-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZQ-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	// countTadpoles says one marker per matching tadpole; the roll is
	// nextInt(2,6) so counts are per-leg — each must sit in [2,5].
	countTadpoles := func(pw *protocol.Writer, sink *chatSink, sel string, i int) int {
		sendCommand(t, pw, fmt.Sprintf("execute as %s run say ZTC-%d", sel, i))
		sendCommand(t, pw, fmt.Sprintf("say ZCK%d", i))
		marker := fmt.Sprintf("ZCK%d", i)
		n := -1
		cnt := 0
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					n = cnt
					goto done
				}
				if strings.Contains(line, fmt.Sprintf("ZTC-%d", i)) {
					cnt++
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
	done:
		return n
	}

	run := func(addr string) ([]string, int) {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(4 * time.Second)
		sink.take()
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)

		var out []string
		out = append(out, probe(pw, sink, "101 -61 101 minecraft:frogspawn", 0))
		out = append(out, probe(pw, sink, "116 -62 116 minecraft:frogspawn", 1))

		// Cover the whole 3600-12000 hatch-delay range — the due tick
		// hatches the water egg and pops the stone one.
		sendCommand(t, pw, "tick sprint 13000")
		deadline := time.Now().Add(150 * time.Second)
		hatched := false
		for time.Now().Before(deadline) && !hatched {
			time.Sleep(2 * time.Second)
			if probe(pw, sink, "101 -61 101 minecraft:air", 2) == "hit" {
				hatched = true
			}
		}
		out = append(out, probe(pw, sink, "101 -61 101 minecraft:air", 3))
		out = append(out, probe(pw, sink, "116 -62 116 minecraft:air", 4))

		// Tadpoles inside the water below the egg site: nextInt(2,6)
		// of them, all persistenceRequired=1b, none near the popped
		// stone egg. The pad spans 6x6 so the radius covers a
		// tadpole that swam into a corner before the sprint ended.
		tadSel := "@e[type=minecraft:tadpole,distance=..6,x=101,y=-61.5,z=101]"
		out = append(out, entProbe(pw, sink, tadSel, 5))
		out = append(out, entProbe(pw, sink,
			"@e[type=minecraft:tadpole,distance=..6,x=101,y=-61.5,z=101,nbt={PersistenceRequired:1b}]", 6))
		out = append(out, entProbe(pw, sink,
			"@e[type=minecraft:tadpole,distance=..4,x=116,y=-61.5,z=116]", 7))
		return out, countTadpoles(pw, sink, tadSel, 8)
	}

	van, vanN := run(targets.vanilla)
	mcg, mcgN := run(targets.mcgo)
	names := []string{
		"fixture A egg present",
		"fixture B egg present",
		"water egg hatched to air",
		"stone egg popped to air",
		"tadpoles near water egg",
		"persistent tadpoles",
		"no tadpoles near stone egg",
	}
	for i, want := range []string{"hit", "hit", "hit", "hit", "hit", "hit", "miss"} {
		t.Logf("%-28s vanilla=%s mcgo=%s", names[i], van[i], mcg[i])
		if van[i] != want || mcg[i] != want {
			t.Errorf("%s: vanilla=%s mcgo=%s, want %s", names[i], van[i], mcg[i], want)
		}
	}
	t.Logf("tadpole count vanilla=%d mcgo=%d", vanN, mcgN)
	for leg, n := range map[string]int{"vanilla": vanN, "mc-go": mcgN} {
		if n < 2 || n > 5 {
			t.Errorf("%s tadpole count %d outside [2,5]", leg, n)
		}
	}
}
