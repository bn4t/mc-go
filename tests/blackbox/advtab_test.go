package blackbox

// TestSelectAdvancementsTabParity covers
// ServerGamePacketListenerImpl.handleSeenAdvancements +
// PlayerAdvancements.setSelectedTab, measured from 26.2 bytecode:
// OPENED_TAB resolves the id through ServerAdvancementManager — an
// unknown id is ignored; a resolved holder goes to setSelectedTab
// which keeps it only when value().isRoot() && display().isPresent(),
// and emits select_advancements_tab (0x55, nullable Identifier) only
// when the stored selection actually changed. CLOSED_SCREEN leaves
// lastSelectedTab alone.

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// selAdvLines parses captured 0x55 payloads into "sel <id>" / "sel
// null" strings.
func selAdvLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if !strings.HasPrefix(line, "0x55:") {
			continue
		}
		r := protocol.NewPacketReader([]byte(line[5:]))
		ok, err := r.Bool()
		if err != nil || !ok {
			out = append(out, "sel null")
			continue
		}
		id, _ := r.String()
		out = append(out, "sel "+id)
	}
	return out
}

// sendSeenAdv writes one seen_advancements serverbound packet: VarInt
// action (0 = OPENED_TAB, 1 = CLOSED_SCREEN) + Identifier only for
// OPENED_TAB.
func sendSeenAdv(t *testing.T, pw *protocol.Writer, action int32, tab string) {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(action)
	if action == 0 {
		b.String(tab)
	}
	if err := pw.WritePacket(0x32, b.Bytes()); err != nil {
		t.Fatalf("seen_advancements write: %v", err)
	}
}

func TestSelectAdvancementsTabParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Advt"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		conn, pr, pw := joinPlayBlackbox(t, addr, "Advt")
		sink := newChatSinkTP(pr, pw)
		defer conn.Close()
		time.Sleep(400 * time.Millisecond)
		sink.take()

		var got []string
		probe := func(action int32, tab string) {
			sendSeenAdv(t, pw, action, tab)
			time.Sleep(400 * time.Millisecond)
			got = append(got, selAdvLines(sink.take())...)
		}

		probe(0, "minecraft:adventure/root") // select
		probe(0, "minecraft:adventure/root") // no change -> silent
		probe(0, "minecraft:recipes/root")   // root w/o display -> clears
		probe(0, "minecraft:bogus/tab")      // unknown id -> ignored
		probe(1, "")                         // close keeps lastSelectedTab
		probe(0, "minecraft:story/root")     // select story
		probe(1, "")                         // close, selection stays
		probe(0, "minecraft:story/root")     // same -> silent
		return got
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if len(want) == 0 {
		t.Fatalf("vanilla emitted no select_advancements_tab at all")
	}
	if len(got) == 0 {
		t.Fatalf("mc-go emitted no select_advancements_tab at all")
	}
	if len(want) != len(got) {
		t.Fatalf("select_advancements_tab count: vanilla %d vs mc-go %d\nvanilla=%v\nmc-go=%v",
			len(want), len(got), want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("select_advancements_tab[%d]: vanilla %q vs mc-go %q\nvanilla=%v\nmc-go=%v",
				i, want[i], got[i], want, got)
		}
	}
}
