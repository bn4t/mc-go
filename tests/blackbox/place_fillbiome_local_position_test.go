//go:build blackbox

package blackbox

// TestPlaceFillBiomeLocalPositionParity pins caret block positions for
// /place (structure and feature) and /fillbiome. Success replies carry the
// resolved block coordinates, so the whole component tree is compared across
// servers; direct and execute-rotated sources, a local/absolute corner mix,
// unaligned and reversed quart boxes, physical biome and feature placement,
// and the shared mixed/invalid error cursors are covered.

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestPlaceFillBiomeLocalPositionParity(t *testing.T) {
	const name = "PlaceBiomeLocal"
	targets := requireBlackboxFiles(t,
		map[string]string{"view-distance": "2", "simulation-distance": "2"},
		map[string]string{"ops.json": opsJSONFor(name)},
		func(s *server.Server) {
			s.ViewDistance = 2
			s.SimulationDistance = 2
			s.WorldSeed = 1
		})
	defer targets.cleanup()

	probes := []string{
		"place structure swamp_hut ^ ^ ^1",
		"execute positioned 8.5 -60 8.5 rotated 90 0 run place structure swamp_hut ^ ^ ^1",
		"place feature minecraft:oak ^ ^ ^1",
		"fillbiome ^ ^ ^ ^ ^ ^1 minecraft:plains",
		"execute positioned 8.5 -60 8.5 rotated 90 0 run fillbiome ^ ^ ^ ^ ^ ^1 minecraft:desert",
		"fillbiome ^ ^ ^ 8 -60 9 minecraft:plains",
		"fillbiome 8 -60 8 ^ ^ ^1 minecraft:desert",
		"fillbiome 7 -60 8 8 -60 8 minecraft:plains",
		"fillbiome 7 -61 8 8 -59 8 minecraft:desert",
		"fillbiome 8 -60 8 7 -60 8 minecraft:plains",
		"fillbiome ^ ^ ^ ^ ^ ^-1 minecraft:desert",
		"place structure swamp_hut ^ ~ ^1",
		"place structure swamp_hut ^NaN ^ ^",
		"place structure swamp_hut ^ ^ ^1e2",
		"place feature minecraft:oak ^ ~ ^1",
		"place feature minecraft:oak ^NaN ^ ^",
		"place feature minecraft:oak ^ ^ ^1e2",
		"fillbiome ^ ~ ^ ^ ^ ^1 minecraft:plains",
		"fillbiome ^ ^ ^ ^ ~ ^1 minecraft:plains",
		"fillbiome ^NaN ^ ^ ^ ^1 minecraft:plains",
		"fillbiome ^ ^ ^ ^ ^ ^1e2 minecraft:plains",
	}

	type probeResult struct {
		trees []string
		keys  []string
	}
	type capture struct {
		replies    []probeResult
		conditions map[string]bool
	}
	run := func(addr string) capture {
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkResolvedTP(pr, pw)
		serial := 0
		command := func(text string) ([]string, string) {
			serial++
			marker := fmt.Sprintf("PLACEBIOMELOCALEND%d", serial)
			sendCommand(t, pw, text)
			sendCommand(t, pw, "say "+marker)
			return waitForExactSayBB(t, sink, marker, 30*time.Second), marker
		}
		probe := func(text string) probeResult {
			lines, marker := command(text)
			var out probeResult
			for _, line := range lines {
				if structureSaidBB([]string{line}, marker) {
					continue
				}
				if tree, ok := componentTree(line, true); ok {
					out.trees = append(out.trees, tree)
				}
				if key, _, ok := componentArgs(line); ok {
					out.keys = append(out.keys, key)
				}
			}
			return out
		}
		result := capture{conditions: map[string]bool{}}
		condition := func(text, say, label string) {
			lines, _ := command(text)
			result.conditions[label] = structureSaidBB(lines, say)
		}

		command("tick freeze")
		command("gamemode spectator @s")
		command("forceload add -32 -32 47 31")
		command("tp @s 8.5 -60 8.5 0 0")

		result.replies = append(result.replies, probe(probes[0]))
		result.replies = append(result.replies, probe(probes[1]))
		command("tp @s 24.5 -60 24.5 0 0")
		result.replies = append(result.replies, probe(probes[2]))
		for y := -60; y <= -58; y++ {
			say := fmt.Sprintf("PLACEBIOMELOCAL_TREE_%d", y)
			condition(fmt.Sprintf("execute if block 24 %d 25 oak_log run say %s", y, say), say, fmt.Sprintf("tree_%d", y))
		}
		command("tp @s 8.5 -60 8.5 0 0")
		result.replies = append(result.replies, probe(probes[3]))
		condition("execute if biome 8 -60 9 minecraft:plains run say PLACEBIOMELOCAL_BIOME_DIRECT", "PLACEBIOMELOCAL_BIOME_DIRECT", "biome_direct")
		result.replies = append(result.replies, probe(probes[4]))
		condition("execute if biome 7 -60 8 minecraft:desert run say PLACEBIOMELOCAL_BIOME_ROTATED", "PLACEBIOMELOCAL_BIOME_ROTATED", "biome_rotated")
		result.replies = append(result.replies, probe(probes[5]))
		condition("execute if biome 8 -60 9 minecraft:plains run say PLACEBIOMELOCAL_BIOME_MIXED_FROM", "PLACEBIOMELOCAL_BIOME_MIXED_FROM", "biome_mixed_from")
		result.replies = append(result.replies, probe(probes[6]))
		condition("execute if biome 8 -60 9 minecraft:desert run say PLACEBIOMELOCAL_BIOME_MIXED_TO", "PLACEBIOMELOCAL_BIOME_MIXED_TO", "biome_mixed_to")
		for _, text := range probes[7:] {
			result.replies = append(result.replies, probe(text))
		}
		return result
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, text := range probes {
		if !slices.Equal(want.replies[i].trees, got.replies[i].trees) {
			t.Errorf("%q:\n  vanilla %v\n  mc-go   %v", text, want.replies[i].trees, got.replies[i].trees)
		}
		if !slices.Equal(want.replies[i].keys, got.replies[i].keys) {
			t.Errorf("%q keys:\n  vanilla %v\n  mc-go   %v", text, want.replies[i].keys, got.replies[i].keys)
		}
	}
	wantKeys := []string{
		"commands.place.structure.success", "commands.place.structure.success", "commands.place.feature.success",
		"commands.fillbiome.success.count", "commands.fillbiome.success.count", "commands.fillbiome.success.count",
		"commands.fillbiome.success.count", "commands.fillbiome.success.count", "commands.fillbiome.success.count",
		"commands.fillbiome.success.count", "commands.fillbiome.success.count",
		"argument.pos.mixed", "parsing.double.expected", "command.expected.separator",
		"argument.pos.mixed", "parsing.double.expected", "command.expected.separator",
		"argument.pos.mixed", "argument.pos.mixed", "parsing.double.expected", "command.expected.separator",
	}
	if len(wantKeys) != len(probes) {
		t.Fatalf("fixture has %d keys for %d probes", len(wantKeys), len(probes))
	}
	for i, key := range wantKeys {
		if !slices.Contains(want.replies[i].keys, key) {
			t.Errorf("%q missing %s on vanilla: %v", probes[i], key, want.replies[i].keys)
		}
		if !slices.Contains(got.replies[i].keys, key) {
			t.Errorf("%q missing %s on mc-go: %v", probes[i], key, got.replies[i].keys)
		}
	}
	for label, wantOK := range want.conditions {
		if got.conditions[label] != wantOK {
			t.Errorf("condition %s = %v on mc-go, want %v from vanilla", label, got.conditions[label], wantOK)
		}
		if !wantOK {
			t.Errorf("condition %s failed on vanilla", label)
		}
	}
}
