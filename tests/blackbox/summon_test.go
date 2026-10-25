package blackbox

// TestSummonedEntityParity builds an iron golem: four iron blocks in a
// T plus a carved pumpkin on top. Vanilla's BlockPattern consumes the
// five blocks and spawns the golem, granting adventure/summon_iron_golem.

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestSummonedEntityParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Golem"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Golem")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 23 -60 20 0 0")
		// Vanilla's iron-golem pattern is ~^~ / ### / ~#~: the cells
		// beside the pumpkin and beside the base must be air, so the T
		// floats one block above the ground.
		sendCommand(t, pw, "setblock 20 -60 20 minecraft:iron_block")
		sendCommand(t, pw, "fill 19 -59 20 21 -59 20 minecraft:iron_block")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:carved_pumpkin")
		advCollect(t, c, pr, pw, 600*time.Millisecond, f)
		// setblock the pumpkin: vanilla's pattern check runs on any
		// block write and grants to every player online, so the command
		// path is the parity-relevant one here.
		sendCommand(t, pw, "setblock 20 -58 20 minecraft:carved_pumpkin")
		advCollect(t, c, pr, pw, 2500*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:adventure/summon_iron_golem"]["summoned_golem"] {
		t.Fatalf("vanilla: golem summon not granted: %v", want.prog["minecraft:adventure/summon_iron_golem"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("summon_iron_golem parity: %d diffs\n%v", len(diffs), diffs)
	}
}
