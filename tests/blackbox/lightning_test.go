package blackbox

// TestLightningStrikeParity puts a lightning rod down with a villager
// beside it and calls a bolt onto the rod — vanilla collects the
// entities in the strike box (±15 horizontal, y→y+21) and fires
// lightning_strike for every player whose distance predicate holds.
// adventure/lightning_rod_with_villager_no_fire ("Surge Protector")
// wants a villager bystander and a bolt with blocks_set_on_fire 0,
// which a rod strike satisfies.

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestLightningStrikeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Zapper"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Zapper")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "tp @s 4 -60 0")
		// Rod on a pillar: the bolt's scatter fires need a sturdy or
		// flammable base, and a rod on bare flat ground leaves fire on
		// the neighbors — blocks_set_on_fire 0 requires every scatter
		// cell to hang over air.
		sendCommand(t, pw, "fill 4 -60 -10 4 -50 -10 minecraft:stone")
		sendCommand(t, pw, "setblock 4 -49 -10 minecraft:lightning_rod")
		sendCommand(t, pw, "summon minecraft:villager 6 -60 -10 {NoAI:1b}")
		advCollect(t, c, pr, pw, 500*time.Millisecond, f)
		sendCommand(t, pw, "summon minecraft:lightning_bolt 4 -48 -10")
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:adventure/lightning_rod_with_villager_no_fire"]["lightning_rod_with_villager_no_fire"] {
		t.Fatalf("vanilla: surge protector not obtained: %v", want.prog["minecraft:adventure/lightning_rod_with_villager_no_fire"])
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("lightning_strike parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
