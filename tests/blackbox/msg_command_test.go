package blackbox

// TestMsgSelectorParity pins vanilla's MessageArgument.resolve: a
// selector embedded in /msg, /say or /me text expands server-side into
// the matched players' display-name components — click suggest_command,
// show_entity hover, insertion — comma-joined in gray when several
// match, an empty literal when none do. A message without selectors
// stays a bare literal, and literal chunks around resolved selectors
// land in extra — as bare strings when every element is plain, as
// {"": text} compounds when the list is compound-typed.
//
// Each command's disguised_chat content component and every system
// chat reply, for both the sender and the recipient, must be identical
// across servers.

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
)

func TestMsgSelectorParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P", "Q"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) [][]string {
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
		for _, cmd := range []string{
			"msg Q hi @a bye",
			"msg Q hi @s",
			"say all @e[type=player]!",
			"me waves @p",
			"msg Q nomatch @e[type=zombie] end",
			"msg Q plain",
			"msg @a broadcast @s",
		} {
			sink.take()
			sink2.take()
			sendCommand(t, pw, cmd)
			time.Sleep(400 * time.Millisecond)
			var evs []string
			for _, pair := range [][2]any{{"P", sink}, {"Q", sink2}} {
				who, sn := pair[0].(string), pair[1].(*chatSink)
				for _, line := range sn.take() {
					i := strings.Index(line, ":")
					if i < 0 {
						continue
					}
					// 0x21 disguised_chat: the first field is the message
					// content component.
					if strings.HasPrefix(line, "0x21:") {
						v, err := nbt.ReadUnnamed(strings.NewReader(line[i+1:]))
						if err != nil {
							t.Fatalf("disguised chat content did not decode: %v", err)
						}
						evs = append(evs, who+":"+renderNBT(v, true))
						continue
					}
					if tree, ok := componentTree(line, true); ok {
						evs = append(evs, who+":"+tree)
					}
				}
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if len(want) != len(got) {
		t.Fatalf("command groups: %d vs %d", len(want), len(got))
	}
	var sawExpanded, sawEmpty, sawPlain bool
	for i := range want {
		if len(want[i]) != len(got[i]) {
			t.Fatalf("command %d: vanilla=%v\nmc-go=%v", i, want[i], got[i])
		}
		for j := range want[i] {
			if want[i][j] != got[i][j] {
				t.Fatalf("command %d event %d:\nvanilla %s\nmc-go    %s",
					i, j, want[i][j], got[i][j])
			}
		}
		for _, e := range want[i] {
			switch {
			case strings.Contains(e, "show_entity"):
				sawExpanded = true
			case strings.Contains(e, `"nomatch "`):
				sawEmpty = true
			case e == `P:"plain"` || e == `Q:"plain"`:
				sawPlain = true
			}
		}
	}
	if !sawExpanded || !sawEmpty || !sawPlain {
		t.Fatalf("script did not exercise the surface: expanded=%v empty=%v plain=%v",
			sawExpanded, sawEmpty, sawPlain)
	}
}
