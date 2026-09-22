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

func TestAdminCmdParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P", "Q"),
	}, func(s *server.Server) {
		// save-all needs somewhere to write; without a world dir
		// mc-go honestly reports save.failed, which vanilla never
		// reaches — it always has a region dir.
		s.WorldDir = t.TempDir()
	})
	defer targets.cleanup()

	cmds := []string{
		`msg`,
		`msg Q`,
		`msg Q hello world`,
		`msg nosuchplayer hi`,
		`msg @e[type=minecraft:pig] hi`,
		`msg @s hi`,
		`tell Q hi`,
		`w Q hi`,
		`msg @a all hi`,
		`msg @e[type=minecraft:zombie] hi`,
		`teammsg`,
		`teammsg hi`,
		`tm hi`,
		`save-all`,
		`save-all flush`,
		`save-all x`,
		`save-all flush x`,
		`save-off`,
		`save-off`,
		`save-on`,
		`save-on`,
		`save-off x`,
		`ban-ip`,
		`ban-ip 1.2.3.4`,
		`ban-ip 5.6.7.8 reason words`,
		`ban-ip 5.6.7.8`,
		`ban-ip nosuchplayer`,
		`ban-ip 999.999.999.999`,
		`ban-ip 1.2.3`,
		`pardon-ip`,
		`pardon-ip 1.2.3.4`,
		`pardon-ip 1.2.3.4`,
		`pardon-ip nosuchplayer`,
		`pardon-ip Q`,
		`pardon-ip 9.9.9.9`,
		`ban-ip Q`,
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

		c2, pr2, pw2 := joinPlayQuiet(t, addr, "Q")
		defer c2.Close()
		_ = c2.SetDeadline(time.Time{})
		sendClientInformation(t, pw2)
		_ = pw2.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink2 := newChatSink(pr2, pw2)
		sink2.cap = 1 << 20

		time.Sleep(4 * time.Second)
		var out [][]string
		for i, cmd := range cmds {
			sink.take()
			sink2.take()
			t.Logf("send %d %q", i, cmd)
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			marker := fmt.Sprintf("ZMK%d", i)
			func() { defer func() { recover() }(); sendCommand(t, pw, "say "+marker) }()
			var evs []string
			deadline := time.Now().Add(3 * time.Second)
			gotP, gotQ := false, false
			quietUntil := time.Now()
			for {
				collect := func(who string, sn *chatSink, seen *bool) {
					for _, line := range sn.take() {
						if strings.Contains(line, marker) {
							*seen = true
							continue
						}
						if strings.Contains(line, "chat.type.advancement") {
							continue
						}
						if strings.HasPrefix(line, "0x21:") {
							i2 := strings.Index(line, ":")
							if root, err := nbt.ReadUnnamed(strings.NewReader(line[i2+1:])); err == nil {
								evs = append(evs, who+":dis "+numRe.ReplaceAllString(maskUUID(renderNBT(root, true)), "N"))
							}
							continue
						}
						if tree, ok := componentTree(line, true); ok {
							evs = append(evs, who+":chat "+numRe.ReplaceAllString(maskUUID(tree), "N"))
						} else if strings.HasPrefix(line, "0x79:") {
							i2 := strings.Index(line, ":")
							if root, err := nbt.ReadUnnamed(strings.NewReader(line[i2+1:])); err == nil {
								evs = append(evs, who+":chat "+numRe.ReplaceAllString(maskUUID(renderNBT(root, true)), "N"))
							}
						}
					}
				}
				before := len(evs)
				collect("P", sink, &gotP)
				collect("Q", sink2, &gotQ)
				if len(evs) != before {
					quietUntil = time.Now().Add(150 * time.Millisecond)
				}
				if time.Now().After(deadline) || (gotP && gotQ && time.Now().After(quietUntil)) {
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
