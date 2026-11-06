//go:build blackbox

package blackbox

// TestLootLocalPositionParity pins caret block positions at every /loot
// position site: spawn and insert targets, replace block, fish and mine
// sources. Dirt's deterministic block table keeps the success components
// comparable; item and container probes witness the resolved targets, and the
// shared mixed/invalid cursors are compared with their full context trees.

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestLootLocalPositionParity(t *testing.T) {
	const name = "LootLocal"
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
		"loot spawn ^4 ^ ^1 loot minecraft:blocks/dirt",
		"loot insert ^ ^ ^1 loot minecraft:blocks/dirt",
		"execute positioned 8.5 -60 8.5 rotated 90 0 run loot insert ^ ^ ^1 loot minecraft:blocks/dirt",
		"loot replace block ^ ^ ^3 container.0 loot minecraft:blocks/dirt",
		"loot spawn ^4 ^ ^1 fish minecraft:blocks/dirt ^4 ^ ^1",
		"loot spawn ^4 ^ ^1 mine ^4 ^ ^5",
		"loot spawn 12 -60 9 loot minecraft:blocks/dirt",
		"loot spawn ^ ~ ^1 loot minecraft:blocks/dirt",
		"loot spawn ^NaN ^ ^ loot minecraft:blocks/dirt",
		"loot spawn ^ ^ ^1e2 loot minecraft:blocks/dirt",
		"loot insert ^ ~ ^ loot minecraft:blocks/dirt",
		"loot insert ^NaN ^ ^ loot minecraft:blocks/dirt",
		"loot insert ^ ^ ^1e2 loot minecraft:blocks/dirt",
		"loot replace block ^ ~ ^ container.0 loot minecraft:blocks/dirt",
		"loot replace block ^NaN ^ ^ container.0 loot minecraft:blocks/dirt",
		"loot replace block ^ ^ ^1e2 container.0 loot minecraft:blocks/dirt",
		"loot give @s fish minecraft:blocks/dirt ^ ~ ^",
		"loot give @s fish minecraft:blocks/dirt ^NaN ^ ^",
		"loot give @s fish minecraft:blocks/dirt ^ ^ ^1e2",
		"loot give @s mine ^ ~ ^",
		"loot give @s mine ^NaN ^ ^",
		"loot give @s mine ^ ^ ^1e2",
	}
	wantKeys := []string{
		"commands.drop.success.single", "commands.drop.success.single", "commands.drop.success.single",
		"commands.drop.success.single", "commands.drop.success.single", "commands.drop.success.single_with_table",
		"commands.drop.success.single",
		"argument.pos.mixed", "parsing.double.expected", "command.expected.separator",
		"argument.pos.mixed", "parsing.double.expected", "command.expected.separator",
		"argument.pos.mixed", "parsing.double.expected", "command.expected.separator",
		"argument.pos.mixed", "parsing.double.expected", "command.expected.separator",
		"argument.pos.mixed", "parsing.double.expected", "command.expected.separator",
	}
	if len(wantKeys) != len(probes) {
		t.Fatalf("fixture has %d keys for %d probes", len(wantKeys), len(probes))
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
			marker := fmt.Sprintf("LOOTLOCALEND%d", serial)
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
		command("setblock 7 -60 8 chest")
		command("setblock 8 -60 9 chest")
		command("setblock 8 -60 11 chest")
		command("setblock 12 -60 13 dirt")

		result.replies = append(result.replies, probe(probes[0]))
		condition("execute if entity @e[type=item,x=12,y=-60,z=9,dx=1,dy=1,dz=1] run say LOOTLOCAL_ITEM", "LOOTLOCAL_ITEM", "spawn_item")
		result.replies = append(result.replies, probe(probes[1]))
		condition("execute if data block 8 -60 9 Items[0] run say LOOTLOCAL_INSERT", "LOOTLOCAL_INSERT", "insert_direct")
		result.replies = append(result.replies, probe(probes[2]))
		condition("execute if data block 7 -60 8 Items[0] run say LOOTLOCAL_INSERT_ROTATED", "LOOTLOCAL_INSERT_ROTATED", "insert_rotated")
		result.replies = append(result.replies, probe(probes[3]))
		condition("execute if data block 8 -60 11 Items[0] run say LOOTLOCAL_REPLACE", "LOOTLOCAL_REPLACE", "replace_direct")
		for _, text := range probes[4:] {
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
		if !slices.Contains(want.replies[i].keys, wantKeys[i]) {
			t.Errorf("%q missing %s on vanilla: %v", text, wantKeys[i], want.replies[i].keys)
		}
		if !slices.Contains(got.replies[i].keys, wantKeys[i]) {
			t.Errorf("%q missing %s on mc-go: %v", text, wantKeys[i], got.replies[i].keys)
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
