// SnifferEggBlock scheduled-tick parity vs the vanilla 26.2 server,
// measured off server-26.2.jar.
//
// Vanilla's sniffer egg does NOT random-tick — onPlace arms a
// scheduleTick at (boosted ? 12000 : 24000)/3 + nextInt(300) where
// boosted is #sniffer_egg_hatch_boost (moss_block) below, and every
// write re-arms through the same onPlace (the flag-2 bump writes
// included — the delay is a total split across three crack stages,
// not a per-stage delay). Boosted writes also levelEvent 3009 and
// every write gameEvents BLOCK_PLACE.
// tick: hatch < 2 → sniffer_egg.crack + hatch+1; hatch == 2 →
// sniffer_egg.hatch + destroyBlock (2001 + air) + one baby sniffer
// snapped to the egg center with wrapDegrees(nextFloat*360) yaw.
package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestSnifferEggHatchParity pins the staged hatch end to end: moss
// below halves every stage (4000 vs 8000), so after a 13000-tick
// sprint the boosted egg has already hatched while the unboosted one
// sits deterministically at hatch=1 — a real differential pin, not a
// wait-and-see. A second 12000-tick sprint covers the unboosted
// ~24900 max.
func TestSnifferEggHatchParity(t *testing.T) {
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
		// Boosted leg: moss_block below → 4000+jit per stage.
		`setblock 101 -62 101 minecraft:moss_block`,
		`setblock 101 -61 101 minecraft:sniffer_egg[hatch=0]`,
		// Unboosted leg: stone below → 8000+jit per stage.
		`setblock 104 -62 104 minecraft:stone`,
		`setblock 104 -61 104 minecraft:sniffer_egg[hatch=0]`,
		// Mature unboosted: one stage to hatch.
		`setblock 107 -62 107 minecraft:stone`,
		`setblock 107 -61 107 minecraft:sniffer_egg[hatch=2]`,
		// Mid boosted: two stages to hatch.
		`setblock 110 -62 110 minecraft:moss_block`,
		`setblock 110 -61 110 minecraft:sniffer_egg[hatch=1]`,
		`tp @s 105.5 -59 105.5`,
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZS-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZS-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZSK%d", i))
		marker := fmt.Sprintf("ZSK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZS-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZS-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	entProbe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if entity %s run say ZSE-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless entity %s run say ZSE-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZSEK%d", i))
		marker := fmt.Sprintf("ZSEK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZSE-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZSE-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	// ageProbe data-gets a baby's Age: vanilla hatches at -48000
	// (sniffer's measured spawn Age — Sniffer.class ldc #121) and
	// counts up — the assert is the open interval (-48000, 0). The
	// reply is a commands.data.entity.query NBT component whose last
	// `with` argument is the value.
	ageProbe := func(pw *protocol.Writer, sink *chatSink, sel string, i int) int {
		sendCommand(t, pw, fmt.Sprintf("data get entity %s Age", sel))
		sendCommand(t, pw, fmt.Sprintf("say ZSA%d", i))
		marker := fmt.Sprintf("ZSA%d", i)
		age := 0
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return age
				}
				if strings.HasPrefix(line, "0x79:") && strings.Contains(line, "commands.data.entity.query") {
					if v, ok := nbtWithTailInt(strings.TrimPrefix(line, "0x79:")); ok {
						age = v
					}
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return age
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
		out = append(out, probe(pw, sink, "101 -61 101 minecraft:sniffer_egg[hatch=0]", 0))
		out = append(out, probe(pw, sink, "104 -61 104 minecraft:sniffer_egg[hatch=0]", 1))
		out = append(out, probe(pw, sink, "107 -61 107 minecraft:sniffer_egg[hatch=2]", 2))
		out = append(out, probe(pw, sink, "110 -61 110 minecraft:sniffer_egg[hatch=1]", 3))

		// Sprint 13000 covers the boosted three-stage max (~12897):
		// boosted eggs hatch, the unboosted egg has run exactly one
		// stage — hatch=1 is deterministic here, not probabilistic.
		sendCommand(t, pw, "tick sprint 13000")
		deadline := time.Now().Add(150 * time.Second)
		hatched := false
		for time.Now().Before(deadline) && !hatched {
			time.Sleep(2 * time.Second)
			if probe(pw, sink, "101 -61 101 minecraft:air", 4) == "hit" {
				hatched = true
			}
		}
		if !hatched {
			t.Fatalf("boosted sniffer egg never hatched over 13000 ticks")
		}
		out = append(out, probe(pw, sink, "101 -61 101 minecraft:air", 5))
		out = append(out, probe(pw, sink, "104 -61 104 minecraft:sniffer_egg[hatch=1]", 6))
		out = append(out, probe(pw, sink, "107 -61 107 minecraft:air", 7))
		out = append(out, probe(pw, sink, "110 -61 110 minecraft:air", 8))

		// Sprint +12000 (25000 total) covers the unboosted
		// three-stage max (~24897).
		sendCommand(t, pw, "tick sprint 12000")
		deadline = time.Now().Add(150 * time.Second)
		done2 := false
		for time.Now().Before(deadline) && !done2 {
			time.Sleep(2 * time.Second)
			if probe(pw, sink, "104 -61 104 minecraft:air", 9) == "hit" {
				done2 = true
			}
		}
		if !done2 {
			t.Fatalf("unboosted sniffer egg never hatched over 25000 ticks")
		}
		out = append(out, probe(pw, sink, "104 -61 104 minecraft:air", 10))

		// Babies wander off their spawn cell during the sprint — pin
		// the global count (4 eggs → 4 sniffers) plus one baby's Age.
		out = append(out, entProbe(pw, sink, "@e[type=minecraft:sniffer]", 11))
		n := 0
		sendCommand(t, pw, "execute as @e[type=minecraft:sniffer] run say ZSN")
		sendCommand(t, pw, "say ZSNK")
		deadline3 := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline3) {
			for _, line := range sink.take() {
				if strings.Contains(line, "ZSNK") {
					goto aged
				}
				if strings.Contains(line, "ZSN") {
					n++
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
	aged:
		out = append(out, fmt.Sprintf("%d", n))
		age := ageProbe(pw, sink,
			"@e[type=minecraft:sniffer,limit=1,sort=nearest]", 15)
		return out, age
	}

	van, vanAge := run(targets.vanilla)
	mcg, mcgAge := run(targets.mcgo)
	names := []string{
		"fixture A egg hatch=0",
		"fixture B egg hatch=0",
		"fixture C egg hatch=2",
		"fixture D egg hatch=1",
		"boosted egg hatched to air",
		"unboosted egg at hatch=1 mid-sprint",
		"mature egg hatched to air",
		"mid egg hatched to air",
		"unboosted egg hatched to air",
		"sniffer present",
		"sniffer count",
	}
	for i, want := range []string{"hit", "hit", "hit", "hit", "hit", "hit", "hit", "hit", "hit", "hit", "4"} {
		t.Logf("%-36s vanilla=%s mcgo=%s", names[i], van[i], mcg[i])
		if van[i] != want || mcg[i] != want {
			t.Errorf("%s: vanilla=%s mcgo=%s, want %s", names[i], van[i], mcg[i], want)
		}
	}
	t.Logf("site A baby age vanilla=%d mcgo=%d", vanAge, mcgAge)
	for leg, a := range map[string]int{"vanilla": vanAge, "mc-go": mcgAge} {
		if a <= -48000 || a >= 0 {
			t.Errorf("%s baby age %d outside (-48000, 0)", leg, a)
		}
	}
}
