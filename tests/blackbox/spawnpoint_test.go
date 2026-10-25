package blackbox

import (
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// spawnEvents keeps chat replies plus Set Default Spawn Position;
// everything else in the stream is unrelated tick noise.
func spawnEvents(lines []string) []string {
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
		if err != nil || id != 0x61 {
			continue
		}
		evs = append(evs, "pkt "+maskUUID(line))
	}
	sort.Strings(evs)
	return evs
}

// TestSpawnCommandParity pins /spawnpoint and /setworldspawn against
// the real server: relative/absent positions, the yaw+pitch rotation
// pair, every parse error with its cursor, and the default-spawn
// broadcast packet.
func TestSpawnCommandParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`tp @s 8.5 -60 8.5`,
		`spawnpoint`,
		`spawnpoint @s`,
		`spawnpoint @s 10 -60 20`,
		`spawnpoint @s 10 -60 20 90`,
		`spawnpoint @s 10 -60 20 -45.5`,
		`spawnpoint @s ~ ~ ~`,
		`spawnpoint @s ~1 ~-2 ~3 180`,
		`spawnpoint nosuchplayer 1 2 3`,
		`spawnpoint @e[type=minecraft:pig] 1 2 3`,
		`spawnpoint @a 5 -60 5`,
		`spawnpoint @s 999999999999 0 0`,
		`spawnpoint @s 1 2`,
		`spawnpoint @s 1 2 3 4 5`,
		`spawnpoint @s 1 x 3`,
		`spawnpoint @s 0 -1000 0`,
		`spawnpoint @s 0 1000 0`,
		`spawnpoint @s 0 0 0 360`,
		`spawnpoint @s 0 0 0 -270`,
		`setworldspawn`,
		`setworldspawn 8 -60 8`,
		`setworldspawn 8 -60 8 45`,
		`setworldspawn 8 -60 8 -90.5`,
		`setworldspawn ~ ~ ~`,
		`setworldspawn 8 -60 8 45 extra`,
		`setworldspawn x 0 0`,
		`setworldspawn 0 -1000 0`,
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
			out = append(out, spawnEvents(sink.take()))
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
