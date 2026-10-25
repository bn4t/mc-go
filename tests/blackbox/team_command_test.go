package blackbox

// TestTeamCommandParity pins vanilla's /team family and /teammsg on
// the wire: SetPlayerTeam create/update/remove/member packets, the
// component replies (square-bracketed team names, join/leave/member
// lists, option successes, the admin echo to other ops as
// chat.type.admin), and the team_msg_command_incoming/outgoing
// disguised-chat pair. Player selector resolution, team-formatted
// sender names, and /team join's remove-then-add packet pair on a
// re-join are all byte-compared.
//
// Every command's observed events — raw team/disguised-chat bodies and
// canonical component trees for system chat — must match between
// vanilla and mc-go on both players' connections.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestTeamCommandParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P", "Q"),
	}, nil)
	defer targets.cleanup()

	// script runs a command list against one server and returns, per
	// command, each recipient's observed packets in order.
	run := func(addr string, cmds []string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)

		c2, pr2, pw2 := joinPlayBlackbox(t, addr, "Q")
		defer c2.Close()
		_ = c2.SetDeadline(time.Time{})
		_ = pw2.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink2 := newChatSink(pr2, pw2)
		time.Sleep(400 * time.Millisecond)

		var out [][]string
		for _, cmd := range cmds {
			sink.take()
			sink2.take()
			sendCommand(t, pw, cmd)
			time.Sleep(400 * time.Millisecond)
			var evs []string
			for _, pair := range []struct {
				name string
				sn   *chatSink
			}{{"P", sink}, {"Q", sink2}} {
				for _, line := range pair.sn.take() {
					i := strings.Index(line, ":")
					if i < 0 {
						continue
					}
					// 0x6d set_player_team and 0x21 disguised_chat: compare
					// the whole body — bindings and team info fields
					// included.
					if strings.HasPrefix(line, "0x6d:") || strings.HasPrefix(line, "0x21:") {
						evs = append(evs, fmt.Sprintf("%s pkt%s", pair.name, line[:i]))
						evs = append(evs, "  body "+line[i+1:])
						continue
					}
					if tree, ok := componentTree(line, true); ok {
						evs = append(evs, pair.name+" chat "+tree)
					}
				}
			}
			out = append(out, evs)
		}
		return out
	}

	script := []string{
		"team add red",
		"team add blue \"Blue Crew\"",
		"team add red",
		"team list",
		"team join red P",
		"team join red Q",
		"team join red",
		"team list red",
		"teammsg red says hi",
		"team modify red color gold",
		"team modify red color gold",
		"team modify red color reset",
		"team modify red color badcolor",
		"team modify red prefix \"[R]\"",
		"team modify red displayName \"Red Team\"",
		"team modify red displayName \"Red Team\"",
		"team modify red friendlyFire false",
		"team modify red friendlyFire false",
		"team modify red friendlyFire maybe",
		"team modify red seeFriendlyInvisibles true",
		"team modify red nametagVisibility hideForOtherTeams",
		"team modify red nametagVisibility hideForOtherTeams",
		"team modify red nameTagVisibility never",
		"team modify red deathMessageVisibility never",
		"team modify red collisionRule pushOwnTeam",
		"team modify red collisionRule pushOwnTeam",
		"team modify red collisionRule bogus",
		"team modify red badoption x",
		"team leave Q",
		"team empty red",
		"team empty red",
		"team remove red",
		"team list",
		"team join nosuch P",
		"teammsg hi all",
		"team remove nosuch",
		"team modify nosuch color gold",
		"team list nosuch",
		"team add solo",
		"team list solo",
		"team join solo P",
		"teammsg hi all",
		"team leave P",
		"team leave P",
	}

	want := run(targets.vanilla, script)
	got := run(targets.mcgo, script)
	if len(want) != len(got) {
		t.Fatalf("command groups: %d vs %d", len(want), len(got))
	}
	var sawTeam, sawDC, sawAdmin, sawError bool
	for i := range want {
		if len(want[i]) != len(got[i]) {
			t.Fatalf("command %q: vanilla=%v\nmc-go=%v", script[i], want[i], got[i])
		}
		for j := range want[i] {
			if want[i][j] != got[i][j] {
				t.Fatalf("command %q event %d:\nvanilla %s\nmc-go    %s",
					script[i], j, want[i][j], got[i][j])
			}
		}
		for _, e := range want[i] {
			switch {
			case strings.Contains(e, "pkt0x6d"):
				sawTeam = true
			case strings.Contains(e, "pkt0x21"):
				sawDC = true
			case strings.Contains(e, "chat.type.admin"):
				sawAdmin = true
			case strings.Contains(e, "notFound") || strings.Contains(e, "noteam"):
				sawError = true
			}
		}
	}
	if !sawTeam || !sawDC || !sawAdmin || !sawError {
		t.Fatalf("script did not exercise the surface: team=%v dc=%v admin=%v err=%v",
			sawTeam, sawDC, sawAdmin, sawError)
	}
}
