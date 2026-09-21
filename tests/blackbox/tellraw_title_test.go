//go:build blackbox

package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// tellrawTitleEvents keeps the lines tellraw/title exercises: system
// chat (command replies and tellraw payloads) plus the title packets.
// Everything else in the stream — entity spawns, scoreboard update
// packets, the periodic set_time — is timing noise both servers emit
// with different ordering and ids.
func tellrawTitleEvents(lines []string) []string {
	keep := map[int64]bool{0xe: true, 0x57: true, 0x70: true, 0x72: true, 0x73: true, 0x79: true}
	var evs []string
	for _, line := range lines {
		if tree, ok := componentTree(line, true); ok {
			evs = append(evs, "chat "+maskUUID(tree))
			continue
		}
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		id, err := strconv.ParseInt(line[:i], 0, 32)
		if err != nil || !keep[id] {
			continue
		}
		evs = append(evs, "pkt "+maskUUID(line))
	}
	return evs
}

// TestTellrawTitleParity pins /tellraw and /title against the real
// server: SNBT component parsing (bare strings, compounds, lists),
// server-side score/selector/translate resolution, title packets and
// timing arithmetic, and the full error grammar.
func TestTellrawTitleParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`tellraw @s {"text":"hi"}`,
		`tellraw @s {"text":"hi","color":"red","bold":true}`,
		`tellraw @a {"text":"all"}`,
		`tellraw @s ["a",{"text":"b","color":"blue"}]`,
		`tellraw nosuchplayer {"text":"x"}`,
		`tellraw @s notjson`,
		`tellraw @s {"text":"x"} extra`,
		`tellraw @s {"text":"hi","click_event":{"action":"run_command","command":"/help"}}`,
		`title @s title {"text":"Big"}`,
		`title @s subtitle {"text":"Sub"}`,
		`title @s actionbar {"text":"Act"}`,
		`title @s times 5 20 10`,
		`title @s times 5t 20t 10t`,
		`title @s times 1s 2s 1s`,
		`title @s times 0.5 1.5 0.5`,
		`title @s clear`,
		`title @s reset`,
		`title @s title badjson`,
		`title nosuchplayer title {"text":"x"}`,
		`title @s bogus {"text":"x"}`,
		`tellraw @s {bad`,
		`tellraw @s `,
		`tellraw @e[type=minecraft:pig] {"text":"x"}`,
		`summon minecraft:pig 2 -59 2 {NoAI:1b}`,
		`title @e times 1 2 3`,
		`title @e[type=!minecraft:pig] title {"text":"x"}`,
		`kill @e[type=minecraft:pig]`,
		`tellraw @s {"score":{"name":"P","objective":"z"}}`,
		`scoreboard objectives add zzobj2 dummy`,
		`scoreboard players set P zzobj2 5`,
		`tellraw @s {"score":{"name":"P","objective":"zzobj2"}}`,
		`tellraw @s {"selector":"@s"}`,
		`tellraw @s {"translate":"commands.kill.success.single","with":[{"selector":"@s"}]}`,
		`title @s times -1 -2 -3`,
		`title @s times a b c`,
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
			out = append(out, tellrawTitleEvents(sink.take()))
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
