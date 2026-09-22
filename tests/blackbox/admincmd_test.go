//go:build blackbox

package blackbox

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
)

// TestAdmin2Parity covers the misc admin/debug commands: stopwatch,
// return, schedule, function, fetchprofile, attribute, spawn_armor_trims,
// serverpack, unpublish, raid, dialog, chase, locate, and place.
func TestAdmin2Parity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`summon minecraft:pig ~ ~ ~`,
		`stopwatch`,
		`stopwatch create`,
		`stopwatch create sw`,
		`stopwatch create sw`,
		`stopwatch query sw`,
		`stopwatch restart sw`,
		`stopwatch query sw`,
		`stopwatch query nosuch`,
		`stopwatch remove sw`,
		`stopwatch query sw`,
		`stopwatch create a:b`,
		`stopwatch create "a b"`,
		`stopwatch bogus`,
		`return`,
		`return 5`,
		`return fail`,
		`return run`,
		`return run clear @s`,
		`return run bogus`,
		`return x`,
		`return 5 extra`,
		`schedule`,
		`schedule function`,
		`schedule function foo`,
		`schedule function foo 5`,
		`schedule function foo 5 append`,
		`schedule function foo 5 replace`,
		`schedule function foo 5 bogus`,
		`schedule function bar 5`,
		`schedule clear`,
		`schedule clear foo`,
		`schedule clear bar`,
		`schedule function foo -1`,
		`schedule function minecraft:x 5`,
		`schedule bogus`,
		`function`,
		`function foo`,
		`function minecraft:x`,
		`function foo {"a":1}`,
		`function foo with entity @s`,
		`function foo bar`,
		`fetchprofile`,
		`fetchprofile name`,
		`fetchprofile name P`,
		`fetchprofile id`,
		`fetchprofile id 550e8400-e29b-41d4-a716-446655440000`,
		`fetchprofile entity`,
		`fetchprofile entity @s`,
		`fetchprofile entity @e[type=minecraft:pig,limit=1]`,
		`fetchprofile entity nosuchplayer`,
		`fetchprofile bogus`,
		`attribute`,
		`attribute @s`,
		`attribute @s minecraft:movement_speed`,
		`attribute @s minecraft:movement_speed get`,
		`attribute @s minecraft:movement_speed get 2`,
		`attribute nosuchplayer minecraft:bogus get`,
		`attribute @e[type=minecraft:pig,limit=1] minecraft:attack_damage get`,
		`attribute @e[type=minecraft:pig,limit=1] minecraft:max_health get`,
		`attribute @s minecraft:movement_speed base`,
		`attribute @s minecraft:movement_speed base get`,
		`attribute @s minecraft:movement_speed base get 2`,
		`attribute @s minecraft:movement_speed base set 0.5`,
		`attribute @s minecraft:movement_speed base get`,
		`attribute @s minecraft:movement_speed base set x`,
		`attribute @s minecraft:movement_speed base reset`,
		`attribute @s minecraft:movement_speed modifier`,
		`attribute @s minecraft:movement_speed modifier add minecraft:test 0.1 add_value`,
		`attribute @s minecraft:movement_speed modifier add minecraft:test 0.1 add_value`,
		`attribute @s minecraft:movement_speed modifier value get minecraft:test`,
		`attribute @s minecraft:movement_speed modifier value get minecraft:test 3`,
		`attribute @s minecraft:movement_speed modifier remove minecraft:test`,
		`attribute @s minecraft:movement_speed modifier remove minecraft:test`,
		`attribute @s minecraft:bogus get`,
		`attribute nosuchplayer minecraft:movement_speed get`,
		`attribute @e[type=minecraft:pig,limit=1] minecraft:movement_speed get`,
		`attribute @s minecraft:movement_speed get extra`,
		`spawn_armor_trims`,
		`spawn_armor_trims foo`,
		`serverpack`,
		`serverpack push`,
		`serverpack push http://x`,
		`serverpack pop`,
		`serverpack bogus`,
		`unpublish`,
		`unpublish x`,
		`raid`,
		`raid check`,
		`raid start`,
		`raid stop`,
		`raid sound`,
		`raid glow`,
		`raid spawnleader`,
		`raid spawnleader pillager`,
		`raid setomen`,
		`raid setomen 1`,
		`raid bogus`,
		`dialog`,
		`dialog show`,
		`dialog show @s`,
		`dialog show @s minecraft:pause`,
		`dialog show @s minecraft:quick_actions`,
		`dialog show @s minecraft:bogus`,
		`dialog clear nosuchplayer`,
		`dialog clear @s`,
		`dialog clear`,
		`dialog show nosuchplayer minecraft:pause`,
		`dialog show @e[type=minecraft:pig] minecraft:pause`,
		`dialog bogus`,
		`chase`,
		`chase stop`,
		`chase localhost`,
		`chase localhost 9999`,
		`chase lead`,
		`chase follow`,
		`chase bogus`,
		`locate`,
		`locate structure`,
		`locate structure minecraft:bogus`,
		`locate structure minecraft:village_plains`,
		`locate biome minecraft:plains`,
		`locate biome minecraft:deep_dark`,
		`locate biome minecraft:the_void`,
		`locate biome minecraft:bogus`,
		`locate poi`,
		`locate poi minecraft:bogus`,
		`locate poi minecraft:nether_portal`,
		`locate bogus`,
		`place`,
		`place feature`,
		`place feature minecraft:bogus`,
		`place feature minecraft:oak`,
		`place feature minecraft:oak ~ ~ ~`,
		`place template`,
		`place template foo`,
		`place jigsaw x y`,
		`place structure minecraft:bogus`,
		`place bogus`,
	}

	numRe := regexp.MustCompile(`d\(-?[0-9.e+]+\)|f\(-?[0-9.e+]+\)|i\(-?[0-9]+\)|b\([0-9]+\)|l\(-?[0-9]+\)|"-?[0-9]+(?:\.[0-9]+)?"`)
	tpRe := regexp.MustCompile(`/tp @s [^"]+`)

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
			time.Sleep(600 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+tpRe.ReplaceAllString(numRe.ReplaceAllString(maskUUID(tree), "N"), "/tp @s X"))
				} else if strings.HasPrefix(line, "0x79:") {
					i := strings.Index(line, ":")
					if root, err := nbt.ReadUnnamed(strings.NewReader(line[i+1:])); err == nil {
						evs = append(evs, "chat "+tpRe.ReplaceAllString(numRe.ReplaceAllString(maskUUID(renderNBT(root, true)), "N"), "/tp @s X"))
					}
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
