//go:build blackbox

package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestInvParity covers /enchant and /clear: the enchantment resource
// registry, level bounds and cap, the itemless and incompatible edges
// (the latter embedding the item's literal display name), and clear's
// count mode (maxCount 0 counts without removing), the player-only
// selector, and the item id validation.
func TestInvParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`summon minecraft:pig ~ ~ ~`,
		`enchant @s minecraft:sharpness`,
		`give @s minecraft:diamond_sword`,
		`enchant`,
		`enchant @s`,
		`enchant @s minecraft:sharpness`,
		`enchant @s minecraft:sharpness 1`,
		`enchant @s minecraft:sharpness 6`,
		`enchant @s minecraft:bogus 1`,
		`enchant @s minecraft:sharpness x`,
		`enchant @s minecraft:sharpness -1`,
		`enchant nosuchplayer minecraft:sharpness 1`,
		`enchant @s minecraft:protection 4`,
		`enchant @s minecraft:fire_aspect 2`,
		`enchant @e[type=minecraft:pig,limit=1] minecraft:sharpness 1`,
		`enchant @e[type=minecraft:zombie,limit=1] minecraft:sharpness 1`,
		`enchant @s minecraft:sharpness 1 extra`,
		`give @s minecraft:diamond_sword`,
		`clear @e[type=minecraft:pig] minecraft:dirt`,
		`clear nosuchplayer`,
		`clear @s minecraft:bogus`,
		`clear @s minecraft:diamond_sword x`,
		`clear @s minecraft:diamond_sword -1`,
		`clear @s minecraft:diamond_sword 0`,
		`clear @s minecraft:diamond_sword 1`,
		`clear @s minecraft:dirt`,
		`clear @s`,
		`clear`,
		`clear @s minecraft:diamond_sword 1 extra`,
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		var out [][]string
		for i, cmd := range cmds {
			sink.take()
			t.Logf("send %d %q", i, cmd)
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			time.Sleep(500 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+maskUUID(tree))
				}
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
