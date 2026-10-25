package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// utilEvents keeps chat replies plus the two clientbound packets the
// utility commands produce: Disguised Chat (say/me/msg) and Change
// Difficulty.
func utilEvents(lines []string) []string {
	keep := map[int64]bool{0xa: true, 0x21: true}
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

// TestUtilCommandParity pins /difficulty, /seed, /list, /whitelist,
// /banlist, /setidletimeout, /say, /me and /msg against the real
// server: replies, error cursors, the Change Difficulty broadcast, and
// disguised-chat packets.
func TestUtilCommandParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`difficulty`,
		`difficulty peaceful`,
		`difficulty easy`,
		`difficulty normal`,
		`difficulty hard`,
		`difficulty hard`,
		`difficulty bogushard`,
		`difficulty normal extra`,
		`difficulty`,
		`seed`,
		`list`,
		`list uuids`,
		`list xyz`,
		`say hello world`,
		`say`,
		`me waves hands`,
		`me`,
		`msg P hi there`,
		`tell P secret`,
		`w nosuchplayer hi`,
		`msg P`,
		`msg @e[type=minecraft:pig] hi`,
		`msg @a hi all`,
		`whitelist`,
		`whitelist on`,
		`whitelist off`,
		`whitelist on extra`,
		`whitelist list`,
		`banlist`,
		`banlist ips`,
		`setidletimeout 5`,
		`setidletimeout`,
		`experience`,
		`xp`,
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
			out = append(out, utilEvents(sink.take()))
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
