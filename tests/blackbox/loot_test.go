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
	itemRe := regexp.MustCompile(`"id": "minecraft:[^"]+"|"item\.minecraft\.[^"]+"`)

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
}
