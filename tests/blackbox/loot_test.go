package blackbox

// TestContainerLootParity opens a chest carrying vanilla LootTable NBT:
// player_generates_container_loot fires with the table id. Each of
// nether/loot_bastion's four tables is exercised in its own run —
// the advancement's requirement groups are OR-ed, so the first
// matching chest completes it and the remaining criteria can never
// be earned (vanilla unregisters a completed advancement's triggers).

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestContainerLootParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("LootA", "LootB", "LootC", "LootD"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	tables := []struct {
		crit   string
		ltable string
	}{
		{"loot_bastion_bridge", "minecraft:chests/bastion_bridge"},
		{"loot_bastion_hoglin_stable", "minecraft:chests/bastion_hoglin_stable"},
		{"loot_bastion_other", "minecraft:chests/bastion_other"},
		{"loot_bastion_treasure", "minecraft:chests/bastion_treasure"},
	}

	run := func(addr, ltable, name string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 21 -60 21 0 0")
		sendCommand(t, pw, "setblock 20 -60 20 minecraft:chest{LootTable:\""+ltable+"\"}")
		advCollect(t, c, pr, pw, 400*time.Millisecond, f)
		var ub protocol.Buffer
		ub.VarInt(0) // empty hand opens the chest
		ub.Position(int32(20), -60, int32(20))
		ub.VarInt(1)
		ub.Float(0.5)
		ub.Float(1.0)
		ub.Float(0.5)
		ub.Bool(false)
		ub.Bool(false)
		ub.VarInt(0)
		_ = pw.WritePacket(server.IDPlayUseItemOnSB, ub.Bytes())
		advCollect(t, c, pr, pw, 1500*time.Millisecond, f)
		return f
	}

	names := []string{"LootA", "LootB", "LootC", "LootD"}
	for i, tb := range tables {
		want := run(targets.vanilla, tb.ltable, names[i])
		got := run(targets.mcgo, tb.ltable, names[i])
		if !want.prog["minecraft:nether/loot_bastion"][tb.crit] {
			t.Fatalf("vanilla: %s not granted for %s: %v", tb.crit, tb.ltable, want.prog["minecraft:nether/loot_bastion"])
		}
		if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
			t.Fatalf("%s: %d diffs\n%v", tb.ltable, len(diffs), diffs)
		}
	}
}
