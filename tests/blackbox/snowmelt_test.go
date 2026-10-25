// SnowLayerBlock.randomTick parity vs the vanilla 26.2 server,
// measured off server-26.2.jar.
//
// Vanilla: getBrightness(LightLayer.BLOCK, pos) > 11 →
// dropResources(state, level, pos) + removeBlock(pos, false) — a
// silent air write; destroyBlock's 2001 effect is NOT emitted.
// dropResources yields nothing here: every pool in
// blocks/snow.json is entity:this-gated and the bare block context
// binds no entity (measured: no item entity at a melted cell).
// There is no dampening term (unlike frosted ice's
// 11 - age - dampening); layers=8 is the opaque cube whose stored
// light never exceeds 11, so a full layer never melts this way.
// Snow formation on precipitation is unmeasured — no weather
// system yet.
package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestSnowMeltParity pins the light gate at the 11/12 edge (torch
// at taxicab 2 melts, at 3 holds), the hold-in-dark case, and the
// no-drop semantic (no item entity at either melt site).
func TestSnowMeltParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"simulation-distance": "3",
	}, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) {
		s.SimulationDistance = 3
	})
	defer targets.cleanup()

	setup := []string{
		// A: layers=2 adjacent torch — arrival 14.
		`setblock 101 -62 101 minecraft:dirt`,
		`setblock 101 -61 101 minecraft:snow[layers=2]`,
		`setblock 100 -61 101 minecraft:torch`,
		// B: layers=5 in the dark — holds.
		`setblock 105 -62 105 minecraft:dirt`,
		`setblock 105 -61 105 minecraft:snow[layers=5]`,
		// C: layers=2 torch at taxicab 2 — arrival 13.
		`setblock 109 -62 109 minecraft:dirt`,
		`setblock 109 -61 109 minecraft:snow[layers=2]`,
		`setblock 107 -61 109 minecraft:torch`,
		// D: layers=3 adjacent torch — arrival 14.
		`setblock 113 -62 113 minecraft:dirt`,
		`setblock 113 -61 113 minecraft:snow[layers=3]`,
		`setblock 112 -61 113 minecraft:torch`,
		// E: layers=3 adjacent glowstone — arrival 15.
		`setblock 117 -62 117 minecraft:dirt`,
		`setblock 117 -61 117 minecraft:snow[layers=3]`,
		`setblock 116 -61 117 minecraft:glowstone`,
		// F: layers=8 adjacent glowstone — arrival 15.
		`setblock 121 -62 121 minecraft:dirt`,
		`setblock 121 -61 121 minecraft:snow[layers=8]`,
		`setblock 120 -61 121 minecraft:glowstone`,
		// Sampler frozen until fixtures are in place.
		`gamerule random_tick_speed 0`,
		`tp @s 110.5 -50 110.5`,
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZW-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZW-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZWK%d", i))
		marker := fmt.Sprintf("ZWK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZW-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZW-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	entProbe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if entity %s run say ZWE-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless entity %s run say ZWE-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZWEK%d", i))
		marker := fmt.Sprintf("ZWEK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZWE-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZWE-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	run := func(addr string) []string {
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
		out = append(out, probe(pw, sink, "101 -61 101 minecraft:snow[layers=2]", 0))
		out = append(out, probe(pw, sink, "105 -61 105 minecraft:snow[layers=5]", 1))
		out = append(out, probe(pw, sink, "109 -61 109 minecraft:snow[layers=2]", 2))
		out = append(out, probe(pw, sink, "113 -61 113 minecraft:snow[layers=3]", 3))
		out = append(out, probe(pw, sink, "117 -61 117 minecraft:snow[layers=3]", 4))
		out = append(out, probe(pw, sink, "121 -61 121 minecraft:snow[layers=8]", 5))

		// Unlock the sampler at 4096 — every tickable cell is hit
		// ~once per game tick, so real time converges fast on both
		// legs (vanilla's sprint does not drive random ticks).
		sendCommand(t, pw, `gamerule random_tick_speed 4096`)
		time.Sleep(20 * time.Second)
		sendCommand(t, pw, `gamerule random_tick_speed 0`)
		time.Sleep(500 * time.Millisecond)
		sink.take()

		out = append(out, probe(pw, sink, "101 -61 101 minecraft:air", 6))
		out = append(out, probe(pw, sink, "105 -61 105 minecraft:snow[layers=5]", 7))
		out = append(out, probe(pw, sink, "109 -61 109 minecraft:snow[layers=2]", 8))
		out = append(out, probe(pw, sink, "113 -61 113 minecraft:air", 9))
		out = append(out, probe(pw, sink, "117 -61 117 minecraft:air", 10))
		out = append(out, probe(pw, sink, "121 -61 121 minecraft:snow[layers=8]", 11))

		// dropResources yields NOTHING on melt — every pool in
		// blocks/snow.json is entity:this-gated and the bare block
		// context binds no entity — so the melt sites stay empty.
		out = append(out, entProbe(pw, sink,
			`@e[type=minecraft:item,distance=..3,x=101.5,y=-60.5,z=101.5]`, 12))
		out = append(out, entProbe(pw, sink,
			`@e[type=minecraft:item,distance=..3,x=117.5,y=-60.5,z=117.5]`, 13))
		return out
	}

	van := run(targets.vanilla)
	mcg := run(targets.mcgo)
	names := []string{
		"fixture A snow layers=2",
		"fixture B snow layers=5",
		"fixture C snow layers=2",
		"fixture D snow layers=3",
		"fixture E snow layers=3",
		"fixture F snow layers=8",
		"A: layers=2, arrival 14",
		"B: layers=5 dark",
		"C: layers=2, arrival 13",
		"D: layers=3, arrival 14 (air)",
		"E: layers=3, arrival 15 (glowstone)",
		"F: layers=8, arrival 15 (glowstone)",
		"no drop at melt site A",
		"no drop at melt site E",
	}
	for i, want := range []string{"hit", "hit", "hit", "hit", "hit", "hit",
		"hit", "hit", "hit", "hit", "hit", "hit", "miss", "miss"} {
		t.Logf("%-30s vanilla=%s mcgo=%s", names[i], van[i], mcg[i])
		if van[i] != want || mcg[i] != want {
			t.Errorf("%s: vanilla=%s mcgo=%s, want %s", names[i], van[i], mcg[i], want)
		}
	}
}
