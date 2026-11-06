package server

import (
	"strings"
	"testing"

	"mc-go/internal/world"
)

// TestLootLocalPositionGrammar pins caret grammar and token indices at every
// /loot block-position site: spawn, insert, replace block, fish and mine.
func TestLootLocalPositionGrammar(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	rows := []struct {
		cmd   string
		key   string
		token int
		off   int
	}{
		{"loot spawn ^ ~ ^1 loot minecraft:blocks/dirt", "argument.pos.mixed", 2, 0},
		{"loot spawn ^NaN ^ ^ loot minecraft:blocks/dirt", "parsing.double.expected", 2, 1},
		{"loot spawn ^ ^ ^1e2 loot minecraft:blocks/dirt", "command.expected.separator", 4, 2},
		{"loot insert ^ ~ ^ loot minecraft:blocks/dirt", "argument.pos.mixed", 2, 0},
		{"loot insert ^NaN ^ ^ loot minecraft:blocks/dirt", "parsing.double.expected", 2, 1},
		{"loot insert ^ ^ ^1e2 loot minecraft:blocks/dirt", "command.expected.separator", 4, 2},
		{"loot replace block ^ ~ ^ container.0 loot minecraft:blocks/dirt", "argument.pos.mixed", 3, 0},
		{"loot replace block ^NaN ^ ^ container.0 loot minecraft:blocks/dirt", "parsing.double.expected", 3, 1},
		{"loot replace block ^ ^ ^1e2 container.0 loot minecraft:blocks/dirt", "command.expected.separator", 5, 2},
		{"loot give LootEditor fish minecraft:blocks/dirt ^ ~ ^", "argument.pos.mixed", 5, 0},
		{"loot give LootEditor fish minecraft:blocks/dirt ^NaN ^ ^", "parsing.double.expected", 5, 1},
		{"loot give LootEditor fish minecraft:blocks/dirt ^ ^ ^1e2", "command.expected.separator", 7, 2},
		{"loot give LootEditor mine ^ ~ ^", "argument.pos.mixed", 4, 0},
		{"loot give LootEditor mine ^NaN ^ ^", "parsing.double.expected", 4, 1},
		{"loot give LootEditor mine ^ ^ ^1e2", "command.expected.separator", 6, 2},
	}
	for _, row := range rows {
		got := s.runLoot("", strings.Fields(row.cmd)[1:])
		key := ""
		if len(got.comp.extra) > 0 {
			key = got.comp.extra[0].translate
		}
		if key != row.key || got.fail == nil || got.fail.token != row.token || got.fail.off != row.off {
			t.Errorf("%q = key %q token %+v, want %q at %d+%d", row.cmd, key, got.fail, row.key, row.token, row.off)
		}
	}
}

// TestLootLocalPositionSourceFrame pins direct and execute-rotated caret
// targets and sources. Container slot reads and the mine reply witness the
// resolved positions rather than only parser acceptance.
func TestLootLocalPositionSourceFrame(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	editor := placeSession(t, s, "LootEditor", 8.5, -60, 8.5)
	editor.sendCh = make(chan sendPkt, 4096)
	d := s.dims[dimOverworld]
	for _, cmd := range []string{
		"setblock 7 -60 8 chest",
		"setblock 8 -60 9 chest",
		"setblock 8 -60 11 chest",
		"setblock 12 -60 13 dirt",
	} {
		if r := s.runSetblock("LootEditor", strings.Fields(cmd)[1:]); r.fail != nil {
			t.Fatalf("setup %q: %+v", cmd, r)
		}
	}
	key := func(r reply) string {
		if len(r.comp.extra) > 0 {
			return r.comp.extra[0].translate
		}
		return r.comp.translate
	}

	if r := s.runLoot("LootEditor", strings.Fields("spawn ^4 ^ ^1 loot minecraft:blocks/dirt")); key(r) != "commands.drop.success.single" {
		t.Fatalf("local spawn: %+v", r)
	}
	if r := s.runLoot("LootEditor", strings.Fields("insert ^ ^ ^1 loot minecraft:blocks/dirt")); key(r) != "commands.drop.success.single" {
		t.Fatalf("local insert: %+v", r)
	}
	dirt, _ := world.ItemID("dirt")
	if st, _, ok := s.readItemBlock(d, world.Pos{X: 8, Y: -60, Z: 9}, 0); !ok || st.ID != dirt || st.Count != 1 {
		t.Fatalf("direct insert target %+v ok%v, want one dirt", st, ok)
	}

	if r := s.runExecute("LootEditor", strings.Fields("positioned 8.5 -60 8.5 rotated 90 0 run loot insert ^ ^ ^1 loot minecraft:blocks/dirt")); key(r) != "commands.drop.success.single" {
		t.Fatalf("rotated local insert: %+v", r)
	}
	if st, _, ok := s.readItemBlock(d, world.Pos{X: 7, Y: -60, Z: 8}, 0); !ok || st.ID != dirt || st.Count != 1 {
		t.Fatalf("rotated insert target %+v ok%v, want one dirt", st, ok)
	}

	if r := s.runLoot("LootEditor", strings.Fields("replace block ^ ^ ^3 container.0 loot minecraft:blocks/dirt")); key(r) != "commands.drop.success.single" {
		t.Fatalf("local replace: %+v", r)
	}
	if st, _, ok := s.readItemBlock(d, world.Pos{X: 8, Y: -60, Z: 11}, 0); !ok || st.ID != dirt || st.Count != 1 {
		t.Fatalf("replace target %+v ok%v, want one dirt", st, ok)
	}

	if r := s.runLoot("LootEditor", strings.Fields("spawn ^4 ^ ^1 fish minecraft:blocks/dirt ^4 ^ ^1")); key(r) != "commands.drop.success.single" {
		t.Fatalf("local fish: %+v", r)
	}
	if r := s.runLoot("LootEditor", strings.Fields("spawn ^4 ^ ^1 mine ^4 ^ ^5")); key(r) != "commands.drop.success.single_with_table" {
		t.Fatalf("local mine: %+v", r)
	} else if len(r.comp.with) != 3 {
		t.Fatalf("local mine args: %#v", r.comp.with)
	} else if table, ok := r.comp.with[2].(component); !ok || table.emptyKey != "minecraft:blocks/dirt" {
		t.Fatalf("local mine table: %#v", r.comp.with[2])
	}
}
