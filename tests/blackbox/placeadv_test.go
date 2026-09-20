package blackbox

// TestPlacedBlockParity plants wheat seeds on tilled farmland:
// husbandry/plant_seed's "wheat" criterion is placed_block with a
// location predicate on minecraft:wheat — the simplest shape of the
// trigger's 15 criteria.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestPlacedBlockParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Place"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Place")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 4 -60 2 0 0")
		sendCommand(t, pw, "fill 2 -61 4 6 -61 8 minecraft:farmland")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:wheat_seeds")
		advCollect(t, c, pr, pw, 600*time.Millisecond, f)
		// Use Item On: click the top face of farmland 4,-61,6.
		var ub protocol.Buffer
		ub.VarInt(0)           // hand
		ub.Position(4, -61, 6) // clicked block
		ub.VarInt(1)           // +Y face
		ub.Float(0.5)          // cursor
		ub.Float(1.0)
		ub.Float(0.5)
		ub.Bool(false) // inside block
		ub.Bool(false) // hit miss flag (26.2 tail)
		ub.VarInt(0)   // sequence
		_ = pw.WritePacket(server.IDPlayUseItemOnSB, ub.Bytes())
		advCollect(t, c, pr, pw, 2*time.Second, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:husbandry/plant_seed"]["wheat"] {
		t.Fatalf("vanilla: plant_seed/wheat not obtained: %v", want.prog["minecraft:husbandry/plant_seed"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("placed_block parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
