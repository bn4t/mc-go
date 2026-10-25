//go:build blackbox

package blackbox

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// gameruleRe rewrites the /help gamerule line with sorted rule names —
// vanilla iterates a HashMap, so the order varies between runs.
var gameruleRe = regexp.MustCompile(`/gamerule \((.*)\)`)

func normHelpLine(l string) string {
	m := gameruleRe.FindStringSubmatch(l)
	if m == nil {
		return l
	}
	parts := strings.Split(m[1], "|")
	sort.Strings(parts)
	return l[:strings.Index(l, "(")+1] + strings.Join(parts, "|") + ")"
}

// TestCommandLevelParity drives every registered command bare at op
// levels 0, 2 and 3 and diffs the replies — gated commands answer
// command.unknown.command with the name underlined, exactly like an
// unregistered name, and the level table is vanilla's measured map.
func TestCommandLevelParity(t *testing.T) {
	names := strings.Fields(`advancement attribute ban ban-ip banlist bossbar clear clone damage data datapack debug defaultgamemode deop dialog difficulty effect enchant execute experience fetchprofile fill fillbiome forceload function gamemode gamerule give help item jfr kick kill list locate loot me op pardon pardon-ip particle perf place playsound random recipe reload return ride rotate save-all save-off save-on say schedule scoreboard seed setblock setidletimeout setworldspawn spawnpoint spectate spreadplayers stop stopsound stopwatch summon swing tag team teammsg teleport tellraw test tick time title tm tp transfer trigger version waypoint weather whitelist worldborder xp`)
	// bare-with-extra-args cases plus gated-name help lookups
	extra := []string{"help tp", "help kick", "help nonexist"}
	for _, e := range extra {
		names = append(names, e)
	}
	numRe := regexp.MustCompile(`d\(-?[0-9.e+]+\)|f\(-?[0-9.e+]+\)|i\(-?[0-9]+\)|b\([0-9]+\)|l\(-?[0-9]+\)|"-?[0-9]+(?:\.[0-9]+)?"`)
	ops := func(level int) map[string]string {
		if level == 0 {
			return nil
		}
		return map[string]string{"ops.json": fmt.Sprintf(
			`[{"uuid":"60b291cb-b0b9-3db3-a56b-772fe6ffb2d2","name":"Q","level":%d,"bypassesPlayerLimit":false}]`, level)}
	}

	run := func(addr string) [][]string {
		var c net.Conn
		var pw *protocol.Writer
		var sink *chatSink
		rejoin := func() {
			if c != nil {
				c.Close()
			}
			var pr *protocol.Reader
			c, pr, pw = joinPlayQuiet(t, addr, "Q")
			_ = c.SetDeadline(time.Time{})
			sink = newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(2 * time.Second)
		}
		rejoin()
		defer c.Close()
		var out [][]string
		for i, cmd := range names {
			marker := fmt.Sprintf("ZMK%d", i)
			var evs []string
			// vanilla kicks chat bursts; retry until the marker lands
			for attempt := 0; attempt < 4; attempt++ {
				sink.take()
				var b protocol.Buffer
				b.String(cmd)
				if err := pw.WritePacket(server.IDPlayChatCommandSB, b.Bytes()); err != nil {
					rejoin()
					continue
				}
				time.Sleep(150 * time.Millisecond)
				b = protocol.Buffer{}
				b.String("me " + marker)
				if err := pw.WritePacket(server.IDPlayChatCommandSB, b.Bytes()); err != nil {
					rejoin()
					continue
				}
				evs = evs[:0]
				deadline := time.Now().Add(5 * time.Second)
				got := false
				quietUntil := time.Now()
				for {
					before := len(evs)
					for _, line := range sink.take() {
						if strings.Contains(line, marker) || strings.Contains(line, "chat.type.advancement") {
							got = true
							continue
						}
						var ev string
						if tree, ok := componentTree(line, true); ok {
							ev = tree
						} else if strings.HasPrefix(line, "0x79:") {
							i2 := strings.Index(line, ":")
							if root, err := nbt.ReadUnnamed(strings.NewReader(line[i2+1:])); err == nil {
								ev = renderNBT(root, true)
							}
						}
						if ev != "" {
							evs = append(evs, "chat "+normHelpLine(numRe.ReplaceAllString(maskUUID(ev), "N")))
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
				if got {
					break
				}
				rejoin()
			}
			out = append(out, evs)
		}
		return out
	}

	for _, level := range []int{0, 2, 3} {
		targets := requireBlackboxFiles(t, nil, ops(level), func(s *server.Server) {
			s.OpsRequired = true
			if level > 0 {
				s.SetOpLevel("Q", level)
			}
		})
		want := run(targets.vanilla)
		got := run(targets.mcgo)
		for i, cmd := range names {
			if len(want[i]) != len(got[i]) {
				t.Errorf("level %d cmd %q: vanilla %v\n  mc-go %v", level, cmd, want[i], got[i])
				continue
			}
			for j := range want[i] {
				if want[i][j] != got[i][j] {
					t.Errorf("level %d cmd %q event %d:\n  vanilla %s\n  mc-go   %s", level, cmd, j, want[i][j], got[i][j])
				}
			}
		}
		targets.cleanup()
	}
}
