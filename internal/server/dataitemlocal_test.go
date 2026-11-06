package server

import (
	"strings"
	"testing"

	"mc-go/internal/world"
)

// TestDataItemLocalPositionGrammar pins the caret grammar and token index at
// every direct /data and /item block-position site.
func TestDataItemLocalPositionGrammar(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	rows := []struct {
		cmd   string
		key   string
		token int
		off   int
	}{
		{"data get block ^ ~ ^1 Items", "argument.pos.mixed", 3, 0},
		{"data get block ^NaN ^ ^ Items", "parsing.double.expected", 3, 1},
		{"data get block ^ ^ ^1e2 Items", "command.expected.separator", 5, 2},
		{"data modify storage local:test foo set from block ^ ~ ^ Items", "argument.pos.mixed", 8, 0},
		{"item replace block ^ ~ ^1 container.0 with minecraft:stone", "argument.pos.mixed", 3, 0},
		{"item replace block ^NaN ^ ^ container.0 with minecraft:stone", "parsing.double.expected", 3, 1},
		{"item replace block ^ ^ ^1e2 container.0 with minecraft:stone", "command.expected.separator", 5, 2},
		{"item replace block ^ ^ ^9 container.0 from block ^ ~ ^ container.0", "argument.pos.mixed", 9, 0},
	}
	for _, row := range rows {
		var got reply
		if strings.HasPrefix(row.cmd, "data ") {
			got = s.runData("", strings.Fields(row.cmd)[1:])
		} else {
			got = s.runItem("", strings.Fields(row.cmd)[1:])
		}
		key := ""
		if len(got.comp.extra) > 0 {
			key = got.comp.extra[0].translate
		}
		if key != row.key || got.fail == nil || got.fail.token != row.token || got.fail.off != row.off {
			t.Errorf("%q = key %q token %+v, want %q at %d+%d", row.cmd, key, got.fail, row.key, row.token, row.off)
		}
	}
}

// TestDataItemLocalPositionSourceFrame pins local block targets and the
// from-block source against a direct player and an execute-rotated frame.
func TestDataItemLocalPositionSourceFrame(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	placeSession(t, s, "DataEditor", 8.5, -60, 8.5)
	d := s.dims[dimOverworld]
	for _, cmd := range []string{
		`setblock 8 -60 9 chest{Items:[{Slot:0b,id:"minecraft:stone",count:2}]}`,
		`setblock 8 -60 11 chest{Items:[{Slot:0b,id:"minecraft:stone",count:2}]}`,
		`setblock 8 -60 13 chest`,
		"setblock 8 -60 15 oak_sign[rotation=0]",
		`setblock 8 -60 17 chest`,
		`setblock 8 -60 19 chest{Items:[{Slot:0b,id:"minecraft:gold_block",count:4}]}`,
		`setblock 8 -60 21 chest`,
		`setblock 8 -60 23 chest{Items:[{Slot:0b,id:"minecraft:gold_block",count:4}]}`,
	} {
		if r := s.runSetblock("DataEditor", strings.Fields(cmd)[1:]); r.fail != nil {
			t.Fatalf("setup %q: %+v", cmd, r)
		}
	}
	key := func(r reply) string {
		if len(r.comp.extra) > 0 {
			return r.comp.extra[0].translate
		}
		return r.comp.translate
	}
	if r := s.runData("DataEditor", strings.Fields("get block ^ ^ ^1 Items")); key(r) != "commands.data.block.query" {
		t.Fatalf("local data get: %+v", r)
	}
	if r := s.runData("DataEditor", strings.Fields(`merge block ^ ^ ^3 {Items:[{Slot:0b,id:"minecraft:diamond",count:3}]}`)); key(r) != "commands.data.block.modified" {
		t.Fatalf("local data merge: %+v", r)
	}
	diamond, _ := world.ItemID("diamond")
	if st, _, ok := s.readItemBlock(d, world.Pos{X: 8, Y: -60, Z: 11}, 0); !ok || st.ID != diamond || st.Count != 3 {
		t.Fatalf("data merge target %+v ok%v, want 3 diamonds", st, ok)
	}
	if r := s.runData("DataEditor", strings.Fields("modify block ^ ^ ^13 Items set from block ^ ^ ^15 Items")); key(r) != "commands.data.block.modified" {
		t.Fatalf("local data modify: %+v", r)
	}
	gold, _ := world.ItemID("gold_block")
	if st, _, ok := s.readItemBlock(d, world.Pos{X: 8, Y: -60, Z: 21}, 0); !ok || st.ID != gold || st.Count != 4 {
		t.Fatalf("data modify target %+v ok%v, want 4 gold blocks", st, ok)
	}
	if r := s.runItem("DataEditor", strings.Fields("replace block ^ ^ ^9 container.0 with minecraft:diamond 5")); key(r) != "commands.item.block.set.success" {
		t.Fatalf("local item replace: %+v", r)
	}
	if st, _, ok := s.readItemBlock(d, world.Pos{X: 8, Y: -60, Z: 17}, 0); !ok || st.ID != diamond || st.Count != 5 {
		t.Fatalf("item replace target %+v ok%v, want 5 diamonds", st, ok)
	}
	if r := s.runItem("DataEditor", strings.Fields("replace block ^ ^ ^9 container.1 from block ^ ^ ^11 container.0")); key(r) != "commands.item.block.set.success" {
		t.Fatalf("local item source: %+v", r)
	}
	if st, _, ok := s.readItemBlock(d, world.Pos{X: 8, Y: -60, Z: 17}, 1); !ok || st.ID != gold || st.Count != 4 {
		t.Fatalf("item source target %+v ok%v, want 4 gold blocks", st, ok)
	}
	if r := s.runExecute("DataEditor", strings.Fields("positioned 8.5 -60 10.5 rotated 180 0 run data get block ^ ^ ^1 Items")); key(r) != "commands.data.block.query" {
		t.Fatalf("rotated data get: %+v", r)
	}
}
