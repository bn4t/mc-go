package blackbox

// TestWolfBegParity pins BegGoal on the wire: while a player within
// the goal's 8.0 lookDistance holds bone or a wolf_food item in
// either hand the wolf raises DATA_INTERESTED_ID (index 25 boolean)
// and looks at them — the dirty-pack 0x63 carries just that entry.
// Swapping the held item off the predicate drops the flag again, and
// a NoAI wolf never begs because the goal selector is NoAI-gated.
//
// Both wolves stand in a two-deep pit so the random stroll can't
// carry them out of the 8-block range and make the legs diverge.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestWolfBegParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	tag := fmt.Sprintf("bg%d", time.Now().UnixNano()%1_000_000)
	sel := func(tags string) string {
		return fmt.Sprintf("@e[type=minecraft:wolf,tag=%s,limit=1]", tags)
	}
	cmds := []string{
		// A two-deep pit in the flat floor: walls too high for the
		// wander-jump, so both wolves stay in the 8-block radius.
		"fill 14 -62 14 16 -60 16 air",
		// The player stands a block off the pit edge: even the far
		// corner of the pit stays inside BegGoal's 8-block sphere
		// (worst distance² ≈ 44 < 64), so the wolves' wander can't
		// carry them out of range mid-window.
		"tp @s 12.5 -59.5 12.5",
		"item replace entity @s weapon.mainhand with minecraft:bone",
		// AI wolf in the pit: the attach pack, then the beg flag's
		// dirty 0x63 lands in this same window. The variants are
		// pinned explicitly — a summon rolls both, and the legs'
		// independent rolls would diverge the packet compare.
		"summon minecraft:wolf 15.5 -59.5 15.5 {Tags:[\"" + tag + "a\"],variant:\"minecraft:pale\",sound_variant:\"minecraft:big\"}",
		// A quiet probe command buys the flag a second window.
		"data get entity " + sel(tag+"a") + " Invulnerable",
		// Swapping off the item clears the flag (idx20 → 0).
		"item replace entity @s weapon.mainhand with minecraft:stick",
		// Re-arming the predicate re-begs (idx20 → 1).
		"item replace entity @s weapon.mainhand with minecraft:bone",
		// A NoAI wolf in the same pit never begs.
		"summon minecraft:wolf 14.5 -59.5 14.5 {NoAI:1b,Tags:[\"" + tag + "b\"],variant:\"minecraft:pale\",sound_variant:\"minecraft:big\"}",
		"data get entity " + sel(tag+"b") + " Invulnerable",
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(1400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(800 * time.Millisecond)
		sink.take()
		var out [][]string
		for _, cmd := range cmds {
			sink.take()
			sendCommand(t, pw, cmd)
			time.Sleep(550 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if strings.HasPrefix(line, "0x63:") || strings.HasPrefix(line, "0x83:") {
					body := stripVarInt(line[5:])
					evs = append(evs, "pkt "+line[:4]+" "+maskUUID(fmt.Sprintf("%q", body)))
					continue
				}
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
		if fmt.Sprintf("%v", want[i]) != fmt.Sprintf("%v", got[i]) {
			t.Errorf("cmd %q:\n  vanilla %v\n  mc-go   %v", cmd, want[i], got[i])
		}
	}
}
