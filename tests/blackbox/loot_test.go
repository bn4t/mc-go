//go:build blackbox

package blackbox

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
)

func TestLootParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`loot`,
		`loot give`,
		`loot give @s`,
		`loot give @s fish minecraft:gameplay/fishing 0 -60 0`,
		`loot give @s fish minecraft:gameplay/fishing 0 -60 0 minecraft:fishing_rod`,
		`loot give @s fish minecraft:gameplay/fishing 0 -60 0 mainhand`,
		`loot give @s fish minecraft:gameplay/treasure 0 -60 0`,
		`loot give @s fish minecraft:bogus 0 -60 0`,
		`loot give @s fish minecraft:gameplay/fishing 2000 -60 0`,
		`loot give @s loot minecraft:chests/simple_dungeon`,
		`loot give @s loot minecraft:bogus`,
		`loot give @s kill @s`,
		`loot give @s kill @e[type=minecraft:pig,limit=1]`,
		`loot give @s kill nosuchplayer`,
		`loot give @e[type=minecraft:pig,limit=1] fish minecraft:gameplay/fishing 0 -60 0`,
		`loot give nosuchplayer fish minecraft:gameplay/fishing 0 -60 0`,
		`loot give @e[type=minecraft:item] fish minecraft:gameplay/fishing 0 -60 0`,
		`loot spawn 40 -60 0 fish minecraft:gameplay/fishing 0 -60 0`,
		`loot spawn 40 -60 0 loot minecraft:chests/simple_dungeon`,
		`loot spawn 40 -60 0 kill @s`,
		`loot spawn 2000 -60 0 fish minecraft:gameplay/fishing 0 -60 0`,
		`loot insert 0 -60 0 fish minecraft:gameplay/fishing 0 -60 0`,
		`loot insert 2000 -60 0 fish minecraft:gameplay/fishing 0 -60 0`,
		`loot replace entity @s weapon.mainhand fish minecraft:gameplay/fishing 0 -60 0`,
		`loot replace entity @s weapon.mainhand 2 fish minecraft:gameplay/fishing 0 -60 0`,
		`loot replace entity @s bogus.slot fish minecraft:gameplay/fishing 0 -60 0`,
		`loot replace entity @s weapon.mainhand fish minecraft:gameplay/fishing 0 -60 0 extra`,
		`loot replace block 0 -60 0 container.0 fish minecraft:gameplay/fishing 0 -60 0`,
		`loot replace block 0 -60 0 container.27 fish minecraft:gameplay/fishing 0 -60 0`,
		`loot replace block 2000 -60 0 container.0 fish minecraft:gameplay/fishing 0 -60 0`,
		`loot replace block 0 -60 0 bogus.slot fish minecraft:gameplay/fishing 0 -60 0`,
		`loot bogus @s fish minecraft:gameplay/fishing 0 -60 0`,
		`loot give @s mine 0 -60 0`,
		`loot give @s mine 0 -60 0 minecraft:diamond_pickaxe`,
		`loot give @s mine 0 -60 0 mainhand`,
		`loot give @s mine 2000 -60 0`,
		`loot give @s bogus minecraft:gameplay/fishing`,
	}

	numRe := regexp.MustCompile(`d\(-?[0-9.e+]+\)|f\(-?[0-9.e+]+\)|i\(-?[0-9]+\)|b\([0-9]+\)|l\(-?[0-9]+\)|"-?[0-9]+(?:\.[0-9]+)?"`)
	// Loot draws are RNG: the produced item's identity can't be
	// compared, only the reply's shape. Mask the item id inside
	// show_item hovers and the item.minecraft.* name.
	itemRe := regexp.MustCompile(`"id": "minecraft:[^"]+"|"(?:item|block|entity)\.minecraft\.[^"]+"`)

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(4 * time.Second)
		_ = sink // see below
		var out [][]string
		for i, cmd := range cmds {
			sink.take()
			t.Logf("send %d %q", i, cmd)
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			marker := fmt.Sprintf("ZMK%d", i)
			func() { defer func() { recover() }(); sendCommand(t, pw, "say "+marker) }()
			var evs []string
			deadline := time.Now().Add(3 * time.Second)
			for {
				gotMarker := false
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						gotMarker = true
						continue
					}
					// Giving a random stack can trip an
					// inventory-changed advancement announce; the
					// draw is RNG, so drop those events.
					if strings.Contains(line, "chat.type.advancement") {
						continue
					}
					if tree, ok := componentTree(line, true); ok {
						evs = append(evs, "chat "+numRe.ReplaceAllString(itemRe.ReplaceAllString(maskUUID(tree), "I"), "N"))
					} else if strings.HasPrefix(line, "0x79:") {
						i := strings.Index(line, ":")
						if root, err := nbt.ReadUnnamed(strings.NewReader(line[i+1:])); err == nil {
							evs = append(evs, "chat "+numRe.ReplaceAllString(itemRe.ReplaceAllString(maskUUID(renderNBT(root, true)), "I"), "N"))
						}
					}
				}
				if gotMarker || time.Now().After(deadline) {
					break
				}
				time.Sleep(30 * time.Millisecond)
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, cmd := range cmds {
		if len(want[i]) != len(got[i]) {
			t.Errorf("cmd %q: vanilla %v\n  mc-go %v", cmd, want[i], got[i])
			continue
		}
		for j := range want[i] {
			if want[i][j] != got[i][j] {
				t.Errorf("cmd %q event %d:\n  vanilla %s\n  mc-go   %s", cmd, j, want[i][j], got[i][j])
			}
		}
	}

	// Deterministic mine probes: loot the drops into a fresh chest and
	// read the Items SNBT back — the item identity is what pins the
	// condition/tool logic, not the drop.success chat shape. Each
	// probe i owns chest (20,-60,i) so an empty loot write reads as
	// an empty Items, never a stale stack. Count is masked for tables
	// with RNG counts (wheat seeds).
	countRe := regexp.MustCompile(`count: [0-9]+`)
	probes := []struct{ setup, cmd string }{
		// No tool arg → source's (empty) main hand. Vanilla's mine
		// path applies NO requires-tool gate: bare-handed stone still
		// drops cobblestone.
		{"setblock 30 -60 0 minecraft:stone", "loot replace block 20 -60 0 container.0 mine 30 -60 0"},
		{"", "data get block 20 -60 0 Items"},
		{"setblock 30 -60 2 minecraft:glass", "loot replace block 20 -60 1 container.0 mine 30 -60 2 minecraft:diamond_pickaxe"},
		{"", "data get block 20 -60 1 Items"},
		{"", "loot replace block 20 -60 2 container.0 mine 30 -60 2 minecraft:diamond_pickaxe[minecraft:enchantments={\"minecraft:silk_touch\":1}]"},
		{"", "data get block 20 -60 2 Items"},
		{"setblock 30 -60 4 minecraft:acacia_leaves", "loot replace block 20 -60 3 container.0 mine 30 -60 4 minecraft:shears"},
		{"", "data get block 20 -60 3 Items"},
		// block_state_property gates the lower-half drop.
		{"setblock 30 -60 6 minecraft:oak_door[half=lower]", "loot replace block 20 -60 4 container.0 mine 30 -60 6"},
		{"", "data get block 20 -60 4 Items"},
		{"setblock 30 -60 8 minecraft:oak_door[half=upper]", "loot replace block 20 -60 5 container.0 mine 30 -60 8"},
		{"", "data get block 20 -60 5 Items"},
		{"setblock 30 -60 10 minecraft:wheat[age=7]", "loot replace block 20 -60 6 container.0 mine 30 -60 10"},
		{"", "data get block 20 -60 6 Items"},
		{"setblock 30 -60 12 minecraft:wheat[age=0]", "loot replace block 20 -60 7 container.0 mine 30 -60 12"},
		{"", "data get block 20 -60 7 Items"},
		// A wooden pickaxe on iron_ore still yields raw_iron — the
		// incorrect_for_*_tool deny only applies to real breaking.
		{"setblock 30 -60 14 minecraft:iron_ore", "loot replace block 20 -60 8 container.0 mine 30 -60 14 minecraft:wooden_pickaxe"},
		{"", "data get block 20 -60 8 Items"},
		{"setblock 30 -60 16 minecraft:grass_block", "loot replace block 20 -60 9 container.0 mine 30 -60 16 minecraft:dirt"},
		{"", "data get block 20 -60 9 Items"},
		// silk touch picks the first alternatives child.
		{"", "loot replace block 20 -60 10 container.0 mine 30 -60 16 minecraft:dirt[minecraft:enchantments={\"minecraft:silk_touch\":1}]"},
		{"", "data get block 20 -60 10 Items"},
		// [count] semantics: 4 into a 2-stack table — stack-size
		// override shows [4,4], roll-count shows ~8 stacks.
		{"", "loot replace block 20 -60 11 container.0 4 mine 30 -60 10"},
		{"", "data get block 20 -60 11 Items"},
		// Pin replace count on entities: wheat yields 2 stacks/roll —
		// rolls model gives 8 stacks, slots model fills equipment.
		{"", "loot replace entity @s weapon.mainhand 4 mine 30 -60 10"},
		{"", "data get entity @s SelectedItem"},
		{"", "data get entity @s equipment"},
	}

	run2 := func(addr string) [][]string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		time.Sleep(4 * time.Second)
		sink.take()
		var out [][]string
		for i, p := range probes {
			sink.take()
			if p.setup != "" {
				func() { defer func() { recover() }(); sendCommand(t, pw, p.setup) }()
				sink.take()
			}
			if strings.HasPrefix(p.cmd, "loot ") {
				// A fresh chest per probe: setblock on a new
				// position always takes, unlike resetting in place
				// (vanilla answers an unchanged block with
				// setblock.failed and keeps the old contents).
				func() {
					defer func() { recover() }()
					sendCommand(t, pw, fmt.Sprintf("setblock 20 -60 %d minecraft:chest", i/2))
				}()
				sink.take()
			}
			func() { defer func() { recover() }(); sendCommand(t, pw, p.cmd) }()
			marker := fmt.Sprintf("ZMP%d", i)
			func() { defer func() { recover() }(); sendCommand(t, pw, "say "+marker) }()
			var evs []string
			deadline := time.Now().Add(3 * time.Second)
			for {
				gotMarker := false
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						gotMarker = true
						continue
					}
					if strings.Contains(line, "chat.type.advancement") {
						continue
					}
					if tree, ok := componentTree(line, true); ok {
						evs = append(evs, "chat "+countRe.ReplaceAllString(numRe.ReplaceAllString(maskUUID(tree), "N"), "count: N"))
					} else if strings.HasPrefix(line, "0x79:") {
						j := strings.Index(line, ":")
						if root, err := nbt.ReadUnnamed(strings.NewReader(line[j+1:])); err == nil {
							evs = append(evs, "chat "+countRe.ReplaceAllString(numRe.ReplaceAllString(maskUUID(renderNBT(root, true)), "N"), "count: N"))
						}
					}
				}
				if gotMarker || time.Now().After(deadline) {
					break
				}
				time.Sleep(30 * time.Millisecond)
			}
			out = append(out, evs)
		}
		return out
	}

	want2 := run2(targets.vanilla)
	got2 := run2(targets.mcgo)
	for i, p := range probes {
		if len(want2[i]) != len(got2[i]) {
			t.Errorf("probe %q/%q: vanilla %v\n  mc-go %v", p.setup, p.cmd, want2[i], got2[i])
			continue
		}
		for j := range want2[i] {
			if want2[i][j] != got2[i][j] {
				t.Errorf("probe %q/%q event %d:\n  vanilla %s\n  mc-go   %s", p.setup, p.cmd, j, want2[i][j], got2[i][j])
			}
		}
	}
}
