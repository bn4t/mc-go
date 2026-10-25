package blackbox

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
)

func TestOpsAdminParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P", "Q"),
	}, func(s *server.Server) {
		// Vanilla's ops.json grants P and Q level 4; seed mc-go's
		// table the same way so /op and /deop see the state.
		s.SetOpLevel("P", 4)
		s.SetOpLevel("Q", 4)
	})
	defer targets.cleanup()

	cmds := []string{
		`ban`,
		`ban Q`,
		`ban Q`,
		`ban zznoone`,
		`ban zznoone2 bad behavior here`,
		`pardon`,
		`pardon Q`,
		`pardon Q`,
		`pardon zznoone`,
		`kick`,
		`kick zznoone`,
		`op`,
		`op P`,
		`whitelist`,
		`whitelist on`,
		`whitelist on`,
		`whitelist off`,
		`whitelist off`,
		`whitelist on`,
		`whitelist list`,
		`whitelist add Q`,
		`whitelist add zznoone`,
		`whitelist add Q`,
		`whitelist list`,
		`whitelist remove zznoone`,
		`whitelist remove Q`,
		`whitelist remove zznoone`,
		`whitelist reload`,
		`deop Q`,
		`deop Q`,
		`deop zznoone`,
		`op zznoone`,
		`list`,
		`list uuids`,
		`list x`,
		`seed`,
		`help`,
		`help 2`,
		`help ban`,
		`help nosuchcommand`,
		`me waves happily`,
		`me`,
		`kick Q`,
		`kick Q bye reason`,
		`kick zznoone`,
	}

	numRe := regexp.MustCompile(`d\(-?[0-9.e+]+\)|f\(-?[0-9.e+]+\)|i\(-?[0-9]+\)|b\([0-9]+\)|l\(-?[0-9]+\)|"-?[0-9]+(?:\.[0-9]+)?"`)
	// /help's plain-text command usages enumerate /gamerule rules in
	// map order — random on both servers. Sort the (a|b|c) groups so
	// the listing compares on membership.
	altRe := regexp.MustCompile(`\(([^()]*)\)`)
	wlRe := regexp.MustCompile(`commands\.whitelist\.list", "with": \[\{"": N\}, \{"": "([^"]*)"\}\]`)
	norm := func(ev string) string {
		ev = altRe.ReplaceAllStringFunc(ev, func(g string) string {
			parts := strings.Split(g[1:len(g)-1], "|")
			sort.Strings(parts)
			return "(" + strings.Join(parts, "|") + ")"
		})
		// whitelist.list joins the name table in iteration order —
		// hash order on both servers, so sort the joined names.
		return wlRe.ReplaceAllStringFunc(ev, func(g string) string {
			m := wlRe.FindStringSubmatch(g)
			parts := strings.Split(m[1], ", ")
			sort.Strings(parts)
			return strings.Replace(g, m[1], strings.Join(parts, ", "), 1)
		})
	}

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
								evs = append(evs, who+":dis "+norm(numRe.ReplaceAllString(maskUUID(renderNBT(root, true)), "N")))
							}
							continue
						}
						if tree, ok := componentTree(line, true); ok {
							evs = append(evs, who+":chat "+norm(numRe.ReplaceAllString(maskUUID(tree), "N")))
						} else if strings.HasPrefix(line, "0x79:") {
							i2 := strings.Index(line, ":")
							if root, err := nbt.ReadUnnamed(strings.NewReader(line[i2+1:])); err == nil {
								evs = append(evs, who+":chat "+norm(numRe.ReplaceAllString(maskUUID(renderNBT(root, true)), "N")))
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
	byWho := func(evs []string) (p, q []string) {
		for _, e := range evs {
			if strings.HasPrefix(e, "Q:") {
				q = append(q, e[2:])
			} else {
				p = append(p, e[2:])
			}
		}
		return p, q
	}
	for i, cmd := range cmds {
		// Order between two connections is delivery racy; per-connection
		// order is the comparable part.
		wantP, wantQ := byWho(want[i])
		gotP, gotQ := byWho(got[i])
		for side, pair := range map[string][2][]string{"P": {wantP, gotP}, "Q": {wantQ, gotQ}} {
			we, ge := pair[0], pair[1]
			if len(we) != len(ge) {
				t.Errorf("cmd %q %s: vanilla %v\n  mc-go %v", cmd, side, we, ge)
				continue
			}
			for j := range we {
				if we[j] != ge[j] {
					t.Errorf("cmd %q %s event %d:\n  vanilla %s\n  mc-go   %s", cmd, side, j, we[j], ge[j])
				}
			}
		}
	}
}
