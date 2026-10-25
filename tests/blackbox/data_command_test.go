package blackbox

// TestDataCommandParity pins vanilla's /data block surface: get renders
// the target's save NBT as a pretty component (aqua keys, gold values,
// red type suffixes, HashMap-ordered entries) inside
// commands.data.block.query; merge/remove/modify reply
// commands.data.block.modified; a position with no block entity is
// commands.data.block.invalid and a missing path is
// arguments.nbtpath.nothing_found. A merge that touches a sign also
// emits block_entity_data from the tick-end flush; one that touches a
// chest emits none. A merge onto a list replaces it wholesale.
//
// Both servers run the same script; each command's observed events —
// chat replies rendered to canonical trees and raw block_entity_data
// bodies — must be identical.

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestDataCommandParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.take()
		sendCommand(t, pw, "setblock 8 -59 10 minecraft:chest")
		sendCommand(t, pw, "item replace block 8 -59 10 container.0 with minecraft:diamond 3")
		sendCommand(t, pw, "setblock 9 -59 10 minecraft:oak_sign")
		time.Sleep(300 * time.Millisecond)
		sink.take()
		var out [][]string
		for _, cmd := range []string{
			"data get block 8 -59 10",
			"data get block 8 -59 10 Items",
			"data get block 8 -59 10 Items[0].count",
			"data get block 8 -59 10 x",
			"data get block 9 -59 10 front_text.messages[0]",
			"data get block 9 -59 10 nosuchkey",
			"data get block 7 -59 10",
			"data merge block 9 -59 10 {is_waxed:1b}",
			"data merge block 9 -59 10 {front_text:{color:\"red\"}}",
			"data get block 9 -59 10 front_text",
			"data remove block 9 -59 10 front_text.color",
			"data get block 9 -59 10 front_text",
			"data remove block 9 -59 10 no.such[2]",
			"data modify block 8 -59 10 Items[0].count set value 5",
			"data get block 8 -59 10 Items[0].count",
			"data merge block 8 -59 10 {Items:[{id:\"minecraft:apple\",count:2,Slot:4b}]}",
			"data get block 8 -59 10 Items",
		} {
			sink.take()
			sendCommand(t, pw, cmd)
			time.Sleep(350 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if strings.HasPrefix(line, "0x6:") {
					evs = append(evs, "block_entity_data "+line[4:])
					continue
				}
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, tree)
				}
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if len(want) != len(got) {
		t.Fatalf("command reply groups: %d vs %d", len(want), len(got))
	}
	var sawBE, sawQuery, sawModified, sawError bool
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
			case strings.HasPrefix(e, "block_entity_data "):
				sawBE = true
			case strings.Contains(e, "commands.data.block.query"):
				sawQuery = true
			case strings.Contains(e, "commands.data.block.modified"):
				sawModified = true
			case strings.Contains(e, "nothing_found") || strings.Contains(e, "block.invalid"):
				sawError = true
			}
		}
	}
	if !sawQuery || !sawModified || !sawError || !sawBE {
		t.Fatalf("script did not exercise the surface: query=%v modified=%v error=%v be=%v",
			sawQuery, sawModified, sawError, sawBE)
	}
}
