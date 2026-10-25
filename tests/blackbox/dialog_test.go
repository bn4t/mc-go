//go:build blackbox

package blackbox

// TestDialogPacketsParity pins /dialog on the wire: a show writes
// show_dialog carrying the dialog's Holder (registry index + 1 as one
// VarInt) ahead of the sendSuccess chat, a clear writes an empty
// clear_dialog ahead of its chat, and the parse/lookup errors stay
// chat-only. The dialog registry's entry order comes from the same
// captured registries packet the client already holds, so the holder
// bytes compare verbatim on both servers.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestDialogPacketsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		"dialog show @s minecraft:server_links",
		"dialog show @s minecraft:quick_actions",
		"dialog show @s minecraft:custom_options",
		"dialog show @s minecraft:no_such_dialog",
		"dialog show @e minecraft:server_links",
		"dialog clear @s",
		"dialog clear @e",
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		sink.take()
		var out [][]string
		for _, cmd := range cmds {
			sink.take()
			sendCommand(t, pw, cmd)
			time.Sleep(450 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				// show_dialog / clear_dialog bodies carry no volatile
				// fields — the holder VarInt compares verbatim.
				if strings.HasPrefix(line, "0x8c:") || strings.HasPrefix(line, "0x8b:") {
					evs = append(evs, "pkt "+line[:4]+" "+fmt.Sprintf("%q", line[5:]))
					continue
				}
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+maskUUID(tree))
				}
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, cmd := range cmds {
		if fmt.Sprintf("%v", want[i]) != fmt.Sprintf("%v", got[i]) {
			t.Errorf("cmd %q:\n  vanilla %v\n  mc-go   %v", cmd, want[i], got[i])
		}
	}
}
