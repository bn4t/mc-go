package blackbox

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestMiscParity covers the stateless server-management commands:
// save-all/off/on, defaultgamemode, setidletimeout, seed, reload
// (which resends tags/recipes/book-settings), debug/perf/jfr
// profiling shells, trigger, random, spectate and transfer. The
// world seed, profiler timings, jfr filenames and dice rolls differ
// per run and are normalized out.
func TestMiscParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`summon minecraft:pig ~ ~ ~`,
		`save-all`,
		`save-all flush`,
		`save-all bogus`,
		`save-off`,
		`save-off`,
		`save-on`,
		`save-on`,
		`save-off extra`,
		`defaultgamemode`,
		`defaultgamemode survival`,
		`defaultgamemode creative`,
		`defaultgamemode bogus`,
		`defaultgamemode survival extra`,
		`setidletimeout`,
		`setidletimeout 30`,
		`setidletimeout 0`,
		`setidletimeout -1`,
		`setidletimeout x`,
		`setidletimeout 1 extra`,
		`seed`,
		`seed extra`,
		`reload`,
		`reload extra`,
		`publish`,
		`publish 25566`,
		`publish x`,
		`debug`,
		`debug stop`,
		`debug start`,
		`debug start`,
		`debug stop`,
		`debug report`,
		`perf`,
		`perf stop`,
		`perf start`,
		`perf start`,
		`perf stop`,
		`perf 10`,
		`jfr`,
		`jfr start`,
		`jfr start`,
		`jfr stop`,
		`jfr bogus`,
		`trigger`,
		`trigger score`,
		`trigger score add`,
		`trigger score add 1`,
		`trigger score bogus`,
		`random`,
		`random value`,
		`random value 1`,
		`random value 1..10`,
		`random roll 1..6`,
		`random value 10..1`,
		`random value 1..10 minecraft:overworld`,
		`random bogus`,
		`spectate`,
		`spectate @s`,
		`spectate @e[type=minecraft:pig,limit=1]`,
		`spectate nosuchplayer`,
		`transfer`,
		`transfer localhost`,
		`transfer localhost 25565`,
		`transfer localhost 25565 @s`,
		`transfer localhost 0 @s`,
		`transfer localhost 65536 @s`,
		`transfer localhost 25565 nosuchplayer`,
		`transfer localhost x @s`,
	}

	keep := func(line string) bool {
		return strings.HasPrefix(line, "0x81:") ||
			strings.HasPrefix(line, "0x85:") ||
			strings.HasPrefix(line, "0x86:") ||
			strings.HasPrefix(line, "0x4c:") ||
			strings.HasPrefix(line, "0x5d:")
	}
	jfrRe := regexp.MustCompile(`debug/server-\d{4}-\d{2}-\d{2}-\d{6}\.jfr`)
	numRe := regexp.MustCompile(`"(-?\d+\.?\d*)"|i\(-?\d+\)`)
	seedRe := regexp.MustCompile(`"(-?\d+)"`)
	randRe := regexp.MustCompile(`i\(-?\d+\)`)
	mask := func(line string) string {
		if strings.Contains(line, "commands.seed.success") {
			return seedRe.ReplaceAllString(line, `"S"`)
		}
		if strings.Contains(line, "commands.debug.stopped") ||
			strings.Contains(line, "commands.perf.stopped") {
			return numRe.ReplaceAllString(line, `N`)
		}
		if strings.Contains(line, "commands.jfr.stopped") {
			return jfrRe.ReplaceAllString(line, `debug/server-T.jfr`)
		}
		if strings.Contains(line, "commands.random.") {
			return randRe.ReplaceAllString(line, `i(N)`)
		}
		return line
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
			sendCommand(t, pw, cmd)
			time.Sleep(500 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if tree, ok := componentTree(line, true); ok {
					if strings.Contains(tree, "commands.perf.reportSaved") {
						continue
					}
					evs = append(evs, "chat "+maskUUID(mask(tree)))
				} else if keep(line) {
					// The tag/recipe bodies reload sends iterate the
					// registries in a nondeterministic order, so only
					// the id and length are pinned.
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
