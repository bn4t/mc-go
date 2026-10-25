package blackbox

// TestInventoryAdvancementParity gives the player items and diffs the
// advancement flush: story/mine_diamond's "diamond" criterion and the
// root's "crafting_table" are inventory_changed triggers on the game
// tree (the recipe tree already had this trigger wired). Giving both
// items at once exercises the OR-list and the def fan-out vanilla
// emits when story/root completes.

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestInventoryAdvancementParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("InvAdv"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "InvAdv")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "give @s minecraft:diamond")
		sendCommand(t, pw, "give @s minecraft:crafting_table")
		advCollect(t, c, pr, pw, 1500*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:story/mine_diamond"]["diamond"] {
		t.Fatalf("vanilla: mine_diamond/diamond not obtained: %v", want.prog["minecraft:story/mine_diamond"])
	}
	if !want.prog["minecraft:story/root"]["crafting_table"] {
		t.Fatalf("vanilla: story/root crafting_table not obtained: %v", want.prog["minecraft:story/root"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("inventory_changed parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
