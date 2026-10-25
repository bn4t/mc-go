package blackbox

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestEntParity covers the entity commands: rotate (player_rotation
// and player_look_at packets), ride (set_passengers and the rider's
// position sync), spreadplayers (random teleports + team/entity
// distance replies), and damage (invulnerability, type validation,
// success replies and the lethal death broadcast). Position packet
// bodies are masked to their length: vanilla's teleport ids and the
// spread targets are run-dependent.
func TestEntParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`summon minecraft:pig ~ ~ ~`,
		`summon minecraft:pig 8 -60 8`,
		`rotate`,
		`rotate @s`,
		`rotate @s 90`,
		`rotate @s 90 45`,
		`rotate @s -180 90`,
		`rotate @s 360 0`,
		`rotate @s x 0`,
		`rotate @e[type=minecraft:pig,limit=1] 10 20`,
		`rotate nosuchplayer 0 0`,
		`rotate @s 90 45 extra`,
		`rotate @e[type=minecraft:pig] 10 20`,
		`rotate @s facing @e[type=minecraft:pig,limit=1]`,
		`rotate @s facing 0 0 0`,
		`rotate @s facing minecraft:bogus`,
		`rotate @s facing entity @e[type=minecraft:pig,limit=1]`,
		`rotate @s facing entity @e[type=minecraft:pig,limit=1] eyes`,
		`rotate @s facing entity @e[type=minecraft:pig,limit=1] feet`,
		`rotate @s facing entity @e[type=minecraft:pig,limit=1] bogus`,
		`rotate @s facing entity nosuchplayer`,
		`rotate @s facing`,
		`rotate @s facing 0 0`,
		`ride`,
		`ride @s`,
		`ride @s dismount`,
		`ride @s mount`,
		`ride @s mount @s`,
		`ride @s mount @e[type=minecraft:pig,limit=1]`,
		`ride @s mount @e[type=minecraft:pig,limit=1]`,
		`ride @e[type=minecraft:pig,limit=1] mount @e[type=minecraft:pig,limit=1]`,
		`ride @s dismount`,
		`ride @s dismount`,
		`ride nosuchplayer mount @e[type=minecraft:pig,limit=1]`,
		`ride @s bogus`,
		`ride @e[type=minecraft:pig] mount @e[type=minecraft:pig,limit=1]`,
		`spreadplayers`,
		`spreadplayers 8`,
		`spreadplayers 8 -60`,
		`spreadplayers 8 -60 1`,
		`spreadplayers 8 -60 1 10`,
		`spreadplayers 8 -60 1 10 true`,
		`spreadplayers 8 -60 1 10 true @s`,
		`spreadplayers 8 -60 1 10 false @s`,
		`spreadplayers 8 -60 1 10 true @e[type=minecraft:pig]`,
		`spreadplayers 8 -60 1 10 false nosuchplayer`,
		`spreadplayers x -60 1 10 true @s`,
		`spreadplayers 8 -60 -1 10 true @s`,
		`spreadplayers 8 -60 1 0 true @s`,
		`spreadplayers 8 -60 1 10 bogus @s`,
		`spreadplayers 8 -60 1 10 100 true @s`,
		`spreadplayers 8 -60 1 10 true @s extra`,
		`spreadplayers ~ ~ 1 5 true @s`,
		`damage`,
		`damage @s`,
		`damage @s 5`,
		`damage @s 5 minecraft:generic`,
		`damage @s 5 minecraft:bogus`,
		`damage @s -1`,
		`damage @s x`,
		`damage nosuchplayer 5`,
		`damage @e[type=minecraft:pig] 1`,
		`damage @e[type=minecraft:pig,limit=1] 3`,
		`damage @s 5 minecraft:generic bogus`,
		`gamemode survival`,
		`damage @s 5`,
		`damage @s 5 minecraft:generic`,
		`damage @e[type=minecraft:pig,limit=1] 1 minecraft:mob_attack by @s`,
		`damage @e[type=minecraft:pig,limit=1] 1 minecraft:mob_attack by @e[type=minecraft:pig,limit=1] from @s`,
		`damage @e[type=minecraft:pig,limit=1] 1 minecraft:mob_attack at 8 -60 8`,
		`damage @e[type=minecraft:pig,limit=1] 1 minecraft:mob_attack by`,
		`damage @s 100`,
		`gamemode creative`,
		`damage @e[type=minecraft:pig,limit=1] 200 minecraft:generic_kill`,
	}

	relRe := regexp.MustCompile(`\{"": f\(-?[0-9.]+\)\}`)
	// player_look_at, player_position, player_rotation, set_passengers.
	// Bodies carry run-dependent positions and teleport ids, so the
	// pin is the packet id plus its length.
	keep := func(line string) bool {
		return strings.HasPrefix(line, "0x47:") ||
			strings.HasPrefix(line, "0x48:") ||
			strings.HasPrefix(line, "0x49:") ||
			strings.HasPrefix(line, "0x6b:")
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
					// "~" resolves against each server's own spawn
					// position, so the echoed center floats differ.
					if strings.Contains(cmd, "~") {
						tree = relRe.ReplaceAllString(tree, `{"": f(~)}`)
					}
					evs = append(evs, "chat "+maskUUID(tree))
				} else if keep(line) {
					i := strings.Index(line, ":")
					evs = append(evs, "pkt "+line[:i]+": len "+strconv.Itoa(len(line)-i-1))
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
