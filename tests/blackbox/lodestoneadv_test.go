package blackbox

// TestItemUsedOnBlockParity uses a compass on a lodestone:
// adventure/use_lodestone is item_used_on_block with a location_check
// on lodestone plus match_tool on compass — the trigger's simplest
// measured shape.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestItemUsedOnBlockParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Lod"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Lod")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 4 -60 2 0 0")
		sendCommand(t, pw, "setblock 4 -60 5 minecraft:lodestone")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:compass")
		advCollect(t, c, pr, pw, 600*time.Millisecond, f)
		var ub protocol.Buffer
		ub.VarInt(0)
		ub.Position(4, -60, 5)
		ub.VarInt(1)
		ub.Float(0.5)
		ub.Float(1.0)
		ub.Float(0.5)
		ub.Bool(false)
		ub.Bool(false)
		ub.VarInt(0)
		_ = pw.WritePacket(server.IDPlayUseItemOnSB, ub.Bytes())
		advCollect(t, c, pr, pw, 2*time.Second, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:adventure/use_lodestone"]["use_lodestone"] {
		t.Fatalf("vanilla: use_lodestone not obtained: %v", want.prog["minecraft:adventure/use_lodestone"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("item_used_on_block parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
