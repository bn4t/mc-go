//go:build blackbox

package blackbox

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
)

func TestEntCmdParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`summon minecraft:pig ~ ~ ~`,
		`damage`,
		`damage @s`,
		`damage @s 5`,
		`damage @e[type=minecraft:pig] 5`,
		`damage @e[type=minecraft:pig] 5 minecraft:generic`,
		`damage @e[type=minecraft:pig] 5 minecraft:bogus`,
		`damage @e[type=minecraft:pig] -1`,
		`damage @e[type=minecraft:pig] 5 minecraft:generic at 0 -60 0`,
		`damage @e[type=minecraft:pig] 5 minecraft:generic at 0 -60`,
		`damage @e[type=minecraft:pig] 5 minecraft:generic by @s`,
		`damage @e[type=minecraft:pig] 5 minecraft:generic by @s from @s`,
		`damage @e[type=minecraft:pig] 5 minecraft:generic by`,
		`damage zznoone 5`,
		`damage @e[type=minecraft:zombie] 5`,
		`damage @s 0`,
		`damage @s 5 minecraft:generic extra`,
		`ride`,
		`ride @s`,
		`ride @s bogus`,
		`ride @s dismount`,
		`ride @s mount @e[type=minecraft:pig,limit=1]`,
		`ride @s mount @e[type=minecraft:pig,limit=1]`,
		`ride @s mount @s`,
		`ride @e[type=minecraft:pig,limit=1] mount @s`,
		`ride zznoone dismount`,
		`ride @s dismount`,
		`ride @s dismount`,
		`rotate`,
		`rotate @s`,
		`rotate @s 90`,
		`rotate @s 90 0`,
		`rotate @s 90.5 -45.25`,
		`rotate @s abc 0`,
		`rotate @s facing`,
		`rotate @s facing @s`,
		`rotate @s facing @e[type=minecraft:pig,limit=1]`,
		`rotate @s facing 0 -60 0`,
		`rotate @s facing 0 -60`,
		`rotate zznoone 90 0`,
		`rotate @s 90 0 extra`,
		`spreadplayers`,
		`spreadplayers 0`,
		`spreadplayers 0 0`,
		`spreadplayers 0 0 1`,
		`spreadplayers 0 0 1 10`,
		`spreadplayers 0 0 1 10 true`,
		`spreadplayers 0 0 1 10 true @s`,
		`spreadplayers 0 0 1 10 -64 false @s`,
		`spreadplayers 0 0 1 10 999 false @s`,
		`spreadplayers 0 0 0 10 false @s`,
		`spreadplayers 0 0 1 10 false zznoone`,
		`spreadplayers 0 0 1 10 under 50 false @s`,
		`spreadplayers 0 0 1 10 50 under`,
		`spreadplayers 0 0 1 10 50 under false @s`,
		`test`,
		`test bogus`,
		`test run`,
		`test run foo`,
		`test runall`,
		`test runclosest`,
		`test runclosest x`,
		`test runthese`,
		`test runthat`,
		`test runmultiple`,
		`test runmultiple foo`,
		`test runmultiple 2`,
		`test runfailed`,
		`test pos`,
		`test pos x`,
		`test clear`,
		`test clearall`,
		`test clearthese`,
		`test clearthat`,
		`test reset`,
		`test resetclosest`,
		`test resetthat`,
		`test resetthese`,
		`test locate`,
		`test locate foo`,
		`test create`,
		`test create foo`,
		`test create foo 4`,
		`test create foo 4 4`,
		`test create foo 4 4 4`,
		`test verify`,
		`test verify foo`,
		`test debug`,
		`test watch`,
		`test export`,
		`test exportclosest`,
		`test exportthat`,
		`test exportthese`,
		`test stop`,
		`test help`,
	}

	numRe := regexp.MustCompile(`d\(-?[0-9.e+]+\)|f\(-?[0-9.e+]+\)|i\(-?[0-9]+\)|b\([0-9]+\)|l\(-?[0-9]+\)|"-?[0-9]+(?:\.[0-9]+)?"`)

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(4 * time.Second)
		var out [][]string
		for i, cmd := range cmds {
			sink.take()
			t.Logf("send %d %q", i, cmd)
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			marker := fmt.Sprintf("ZMK%d", i)
			func() { defer func() { recover() }(); sendCommand(t, pw, "say "+marker) }()
			var evs []string
			deadline := time.Now().Add(4 * time.Second)
			got := false
			quietUntil := time.Now()
			for {
				before := len(evs)
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						got = true
						continue
					}
					if strings.Contains(line, "chat.type.advancement") {
						continue
					}
					if tree, ok := componentTree(line, true); ok {
						evs = append(evs, "chat "+numRe.ReplaceAllString(maskUUID(tree), "N"))
					} else if strings.HasPrefix(line, "0x79:") {
						i2 := strings.Index(line, ":")
						if root, err := nbt.ReadUnnamed(strings.NewReader(line[i2+1:])); err == nil {
							evs = append(evs, "chat "+numRe.ReplaceAllString(maskUUID(renderNBT(root, true)), "N"))
						}
					}
				}
				if len(evs) != before {
					quietUntil = time.Now().Add(150 * time.Millisecond)
				}
				if time.Now().After(deadline) || (got && time.Now().After(quietUntil)) {
					break
				}
				time.Sleep(30 * time.Millisecond)
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
