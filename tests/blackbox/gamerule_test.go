package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestGameRuleParity pins /gamerule against the real server: query and
// set replies, bool/int value validation with per-error cursors,
// prefixed names, and send_command_feedback's suppression of every
// command reply including the set's own.
func TestGameRuleParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`gamerule`,
		`gamerule keep_inventory`,
		`gamerule random_tick_speed`,
		`gamerule bogusrule`,
		`gamerule minecraft:keep_inventory`,
		`gamerule keep_inventory true`,
		`gamerule keep_inventory`,
		`gamerule keep_inventory false`,
		`gamerule keep_inventory 5`,
		`gamerule keep_inventory true extra`,
		`gamerule random_tick_speed 10`,
		`gamerule random_tick_speed x`,
		`gamerule random_tick_speed -1`,
		`gamerule random_tick_speed 99999999999`,
		`gamerule random_tick_speed true`,
		`gamerule send_command_feedback false`,
		`gamerule keep_inventory`,
		`gamerule send_command_feedback true`,
		`gamerule locator_bar false`,
		`gamerule locator_bar`,
		`gamerule locator_bar true`,
		`gamerule respawn_radius 0`,
		`gamerule players_sleeping_percentage`,
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
		for _, cmd := range cmds {
			sink.take()
			sendCommand(t, pw, cmd)
			time.Sleep(450 * time.Millisecond)
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
