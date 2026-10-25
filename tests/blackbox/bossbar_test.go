package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestBossbarParity pins /bossbar against the real server: create/
// list/get/set/remove replies with their hoverable bar labels, the
// boss_event packet stream for member viewers, and the unchanged/
// unknown/parse failures with their cursors.
func TestBossbarParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`bossbar list`,
		`bossbar add test:bar "Test Bar"`,
		`bossbar add test:bar2 {"text":"Other","color":"gold"}`,
		`bossbar add test:bar`,
		`bossbar add test:bar "Dup"`,
		`bossbar add badname {"text":"x"}`,
		`bossbar list`,
		`bossbar get test:bar`,
		`bossbar get test:bar max`,
		`bossbar get test:bar value`,
		`bossbar get test:bar players`,
		`bossbar get test:bar visible`,
		`bossbar get test:bar color`,
		`bossbar get test:bar bogus`,
		`bossbar get nosuch:bar max`,
		`bossbar set test:bar max 50`,
		`bossbar set test:bar value 25`,
		`bossbar set test:bar value 999`,
		`bossbar set test:bar value -5`,
		`bossbar set test:bar value x`,
		`bossbar set test:bar color blue`,
		`bossbar set test:bar color purple`,
		`bossbar set test:bar color bogus`,
		`bossbar set test:bar style notched_6`,
		`bossbar set test:bar style bogus`,
		`bossbar set test:bar visible true`,
		`bossbar set test:bar visible false`,
		`bossbar set test:bar visible true`,
		`bossbar set test:bar players @s`,
		`bossbar set test:bar players`,
		`bossbar set test:bar players nosuchplayer`,
		`bossbar set test:bar name {"text":"Renamed"}`,
		`bossbar set test:bar name`,
		`bossbar set test:bar bogus 5`,
		`bossbar set nosuch:bar max 5`,
		`bossbar remove nosuch:bar`,
		`bossbar remove test:bar2`,
		`bossbar list`,
		`bossbar`,
		`bossbar remove test:bar`,
		`bossbar remove minecraft:badname`,
		`bossbar list`,
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
					continue
				}
				i := strings.Index(line, ":")
				if i < 0 {
					continue
				}
				id, err := strconv.ParseInt(line[:i], 0, 32)
				if err != nil || id != 0x09 {
					continue
				}
				evs = append(evs, "pkt 0x9:"+maskBarUUID(line[i+1:]))
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

// maskBarUUID normalizes the 16-byte bar uuid leading a boss_event
// body so per-run uuids compare equal.
func maskBarUUID(body string) string {
	if len(body) < 16 {
		return body
	}
	return "<uuid>" + body[16:]
}
