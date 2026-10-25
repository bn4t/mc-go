//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestIceMeltParity drives IceBlock.randomTick — block light above
// 11 (light-dampening 0) melts ice into a water source; everything
// dimmer, including open sky, holds. Probes pin the 11/12 threshold:
// torch (14) at taxicab distance 2 delivers 12 and melts, at 4
// delivers 10 and holds.
func TestIceMeltParity(t *testing.T) {
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
		`time set noon`,
		`fill 44 -60 16 72 -60 30 minecraft:stone`,
		`fill 44 -59 16 72 -57 30 minecraft:air`,
		// A: ice two cells from a torch — light 12, melts. The melt
		// water pops the torch and spreads to a stone dam.
		`setblock 60 -59 20 minecraft:ice`,
		`setblock 60 -59 22 minecraft:torch`,
		`setblock 60 -59 24 minecraft:stone`,
		`setblock 60 -59 26 minecraft:ice`,
		// B: dark ice far from any emitter holds; a twin at distance
		// 2 of its own torch melts behind a dam.
		`setblock 52 -59 20 minecraft:ice`,
		`setblock 52 -59 22 minecraft:stone`,
		`setblock 52 -59 24 minecraft:ice`,
		`setblock 52 -59 26 minecraft:torch`,
		`tp @s 56.5 -58 23.5`,
	}
	preProbes := []struct{ name, spec string }{
		{"lit ice sits", "60 -59 20 minecraft:ice"},
		{"threshold ice sits", "60 -59 26 minecraft:ice"},
		{"dark ice sits", "52 -59 20 minecraft:ice"},
		{"lit ice b sits", "52 -59 24 minecraft:ice"},
	}
	postProbes := []struct{ name, spec string }{
		{"lit ice melted to water", "60 -59 20 minecraft:water"},
		{"threshold ice holds", "60 -59 26 minecraft:ice"},
		{"dark ice holds", "52 -59 20 minecraft:ice"},
		{"lit ice b melted", "52 -59 24 minecraft:water"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZI-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZI-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZIK%d", i))
		marker := fmt.Sprintf("ZIK%d", i)
		hit := "none"
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZI-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZI-miss-%d", i)) {
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
		sendCommand(t, pw, `say ZSYNC`)
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			found := false
			for _, line := range sink.take() {
				if strings.Contains(line, "ZSYNC") {
					found = true
				}
			}
			if found {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(6 * time.Second)

		var out []string
		for i, p := range preProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		sendCommand(t, pw, `gamerule random_tick_speed 4096`)
		time.Sleep(30 * time.Second)
		sendCommand(t, pw, `gamerule random_tick_speed 0`)
		time.Sleep(500 * time.Millisecond)
		for i, p := range postProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(preProbes, postProbes...)
	for i, p := range all {
		t.Logf("%-30s vanilla=%s mcgo=%s", p.name, vanilla[i], mcgo[i])
		if vanilla[i] == "hit" && mcgo[i] != "hit" {
			t.Errorf("probe %q: vanilla hit, mc-go %s", p.name, mcgo[i])
		}
		if mcgo[i] == "hit" && vanilla[i] != "hit" {
			t.Errorf("probe %q: mc-go hit, vanilla %s", p.name, vanilla[i])
		}
	}
}
