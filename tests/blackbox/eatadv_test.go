package blackbox

// TestConsumeItemParity eats one apple in survival: vanilla's
// consume_item trigger feeds husbandry/root's consumed_item and
// husbandry/balanced_diet's apple criteria; the flush diff covers the
// grant and the def fan-out the new visibility lights.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestConsumeItemParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Eat"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Eat")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "give @s minecraft:golden_apple")
		advCollect(t, c, pr, pw, 400*time.Millisecond, f)
		var ub protocol.Buffer
		ub.VarInt(0)
		ub.VarInt(0)
		ub.Float(0)
		ub.Float(0)
		_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		// Eating takes vanilla's 32 ticks; drain past it plus the
		// flush interval.
		advCollect(t, c, pr, pw, 3*time.Second, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:husbandry/balanced_diet"]["golden_apple"] {
		t.Fatalf("vanilla: balanced_diet/golden_apple not obtained: %v", want.prog["minecraft:husbandry/balanced_diet"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("consume_item parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
