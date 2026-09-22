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

// TestSnowMeltParity drives SnowLayerBlock.randomTick — block light
// strictly above 11 melts a snow layer (dropping snowballs); snow at
// light 11 holds, and snow_block never ticks. Torch is 14: taxicab
// distance 2 reads 12 (melts), distance 3 reads 11 (holds) — one
// notch stricter than ice.
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
		`gamerule random_tick_speed 0`,
		`time set noon`,
		`fill 44 -60 16 72 -60 30 minecraft:stone`,
		`fill 44 -59 16 72 -57 30 minecraft:air`,
		// A: snow layer two cells from a torch — light 12, melts.
		`setblock 60 -59 20 minecraft:snow[layers=4]`,
		`setblock 60 -59 22 minecraft:torch`,
		// B: threshold — distance 3 reads 11, holds.
		`setblock 52 -59 20 minecraft:snow[layers=4]`,
		`setblock 52 -59 23 minecraft:torch`,
		// C: snow_block is a different block — never ticks.
		`setblock 64 -59 20 minecraft:snow_block`,
		`setblock 64 -59 22 minecraft:torch`,
		// D: dark snow far from emitters holds.
		`setblock 46 -59 20 minecraft:snow[layers=4]`,
		`tp @s 56.5 -58 23.5`,
	}
	preProbes := []struct{ name, spec string }{
		{"lit snow sits", "60 -59 20 minecraft:snow"},
		{"threshold snow sits", "52 -59 20 minecraft:snow"},
		{"snow block sits", "64 -59 20 minecraft:snow_block"},
		{"dark snow sits", "46 -59 20 minecraft:snow"},
	}
	postProbes := []struct{ name, spec string }{
		{"lit snow melted", "60 -59 20 minecraft:air"},
		{"threshold snow holds", "52 -59 20 minecraft:snow"},
		{"snow block holds", "64 -59 20 minecraft:snow_block"},
		{"dark snow holds", "46 -59 20 minecraft:snow"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZS-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZS-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZSK%d", i))
		marker := fmt.Sprintf("ZSK%d", i)
		hit := "none"
		deadline := time.Now().Add(8 * time.Second)
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

// TestRedstoneOreUnlitParity drives RedStoneOreBlock.randomTick — a
// lit ore settles back to lit=false.
func TestRedstoneOreUnlitParity(t *testing.T) {
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
		`setblock 50 -59 20 minecraft:redstone_ore[lit=true]`,
		`setblock 54 -59 20 minecraft:deepslate_redstone_ore[lit=true]`,
		`setblock 58 -59 20 minecraft:redstone_ore`,
		`tp @s 54.5 -58 23.5`,
	}
	preProbes := []struct{ name, spec string }{
		{"ore lit", "50 -59 20 minecraft:redstone_ore[lit=true]"},
		{"deepslate lit", "54 -59 20 minecraft:deepslate_redstone_ore[lit=true]"},
	}
	postProbes := []struct{ name, spec string }{
		{"ore unlit", "50 -59 20 minecraft:redstone_ore[lit=false]"},
		{"deepslate unlit", "54 -59 20 minecraft:deepslate_redstone_ore[lit=false]"},
		{"control stays unlit", "58 -59 20 minecraft:redstone_ore[lit=false]"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZO-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZO-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZOK%d", i))
		marker := fmt.Sprintf("ZOK%d", i)
		hit := "none"
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZO-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZO-miss-%d", i)) {
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
