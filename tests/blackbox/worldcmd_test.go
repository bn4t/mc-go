package blackbox

import (
	"regexp"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestWorldCmdParity covers the world-state commands: forceload's
// forced-chunk set, fillbiome's biome writes, datapack's builtin pack
// listing, help's page output, version's identity block, waypoint's
// locator-bar commands, and swing's animation broadcast.
func TestWorldCmdParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`summon minecraft:pig ~ ~ ~`,
		`forceload`,
		`forceload add`,
		`forceload add 0 0`,
		`forceload add 0 0 0 0`,
		`forceload add 0 0 17 17`,
		`forceload query`,
		`forceload query 0 0`,
		`forceload query 50 50`,
		`forceload remove 0 0`,
		`forceload remove 0 0 1 1`,
		`forceload query`,
		`forceload remove all`,
		`forceload query`,
		`forceload add x 0`,
		`forceload query x`,
		`forceload remove 5`,
		`forceload bogus`,
		`forceload add 0 0 0 0 extra`,
		`fillbiome`,
		`fillbiome 0 0 0`,
		`fillbiome 0 0 0 8 8 8`,
		`fillbiome 0 0 0 8 8 8 minecraft:plains`,
		`fillbiome 0 0 0 8 8 8 minecraft:desert`,
		`fillbiome 0 0 0 8 8 8 minecraft:bogus`,
		`fillbiome 0 0 0 8 8 8 plains`,
		`fillbiome ~ ~ ~ ~ ~ ~ minecraft:plains`,
		`fillbiome 0 0 0 8 8 8 minecraft:plains replace`,
		`fillbiome 0 0 0 8 8 8 minecraft:plains replace minecraft:plains`,
		`fillbiome 0 0 0 8 8 8 minecraft:plains replace minecraft:bogus`,
		`fillbiome 100000 0 0 100008 8 8 minecraft:plains`,
		`datapack`,
		`datapack list`,
		`datapack list vanilla`,
		`datapack list feature`,
		`datapack enable vanilla`,
		`datapack disable vanilla`,
		`datapack enable bogus`,
		`datapack disable bogus`,
		`datapack create`,
		`help`,
		`help clear`,
		`help bogus`,
		`help 2`,
		`help -1`,
		`version`,
		`version x`,
		`waypoint`,
		`waypoint list`,
		`waypoint modify`,
		`waypoint modify @s`,
		`waypoint modify @s color`,
		`waypoint modify @s color red`,
		`waypoint modify @s color bogus`,
		`waypoint modify @s style`,
		`waypoint modify @s style reset`,
		`waypoint modify @s style set minecraft:bogus`,
		`waypoint modify nosuchplayer color red`,
		`swing`,
		`swing @s`,
		`swing @s mainhand`,
		`swing @s offhand`,
		`swing @e[type=minecraft:pig,limit=1]`,
		`swing @e[type=minecraft:pig,limit=1] offhand`,
		`swing @s bogus`,
		`swing nosuchplayer`,
		`swing @e[type=minecraft:zombie,limit=1] mainhand`,
	}

	numRe := regexp.MustCompile(`f\(-?[0-9.e+]+\)|i\(-?[0-9]+\)|b\([0-9]+\)|"-?[0-9]+(?:\.[0-9]+)?"`)
	// The version stamp and the waypoint's tp-target coordinates differ
	// legitimately between implementations.
	buildRe := regexp.MustCompile(`commands.version.build_time", "with": \["[^"]*"\]`)
	tpRe := regexp.MustCompile(`run tp @s -?[0-9]+ -?[0-9]+ -?[0-9]+`)

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
					tree = numRe.ReplaceAllString(maskUUID(tree), "N")
					tree = buildRe.ReplaceAllString(tree, `commands.version.build_time", "with": [BT]`)
					tree = tpRe.ReplaceAllString(tree, "run tp @s X")
					evs = append(evs, "chat "+tree)
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
