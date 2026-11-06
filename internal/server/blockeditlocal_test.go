package server

import (
	"strings"
	"testing"

	"mc-go/internal/world"
)

// TestBlockEditLocalPositionGrammar pins the shared direct-command caret
// grammar and the token index each block-position argument reports.
func TestBlockEditLocalPositionGrammar(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	rows := []struct {
		cmd   string
		key   string
		token int
	}{
		{"setblock ^ ~ ^1 stone", "argument.pos.mixed", 1},
		{"setblock ^NaN ^ ^ stone", "parsing.double.expected", 0},
		{"setblock ^ ^ ^1e2 stone", "command.expected.separator", 2},
		{"fill ^ ~ ^ ^ ^ ^2 stone", "argument.pos.mixed", 1},
		{"fill ^ ^ ^ ^ ~ ^2 stone", "argument.pos.mixed", 4},
		{"clone ^ ^ ^ ^ ~ ^2 ^ ^ ^5", "argument.pos.mixed", 5},
		{"clone ^ ^ ^ ^ ^ ^2 ^+1 ^ ^5", "parsing.double.expected", 7},
	}
	for _, row := range rows {
		var got reply
		switch {
		case strings.HasPrefix(row.cmd, "setblock "):
			got = s.runSetblock("", strings.Fields(row.cmd)[1:])
		case strings.HasPrefix(row.cmd, "fill "):
			got = s.runFill("", strings.Fields(row.cmd)[1:])
		default:
			got = s.runClone("", strings.Fields(row.cmd)[1:])
		}
		key := ""
		if len(got.comp.extra) > 0 {
			key = got.comp.extra[0].translate
		}
		if key != row.key || got.fail == nil || got.fail.token != row.token {
			t.Errorf("%q = key %q token %+v, want %q at %d", row.cmd, key, got.fail, row.key, row.token)
		}
	}
}

// TestBlockEditLocalPositionSourceFrame pins a direct player's own view
// rotation, an execute-framed rotated source and the eyes anchor for the
// three direct block-editing commands.
func TestBlockEditLocalPositionSourceFrame(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	editor := placeSession(t, s, "Editor", 8.5, -60, 8.5)
	d := s.dims[dimOverworld]
	if r := s.runSetblock("Editor", []string{"^", "^", "^1", "gold_block"}); r.fail != nil {
		t.Fatalf("direct yaw0 setblock: %+v", r)
	}
	gold, _ := world.BlockState("gold_block")
	if got := d.w.GetBlock(world.Pos{X: 8, Y: -60, Z: 9}); got != gold {
		t.Fatalf("direct yaw0 block %v, want gold at 8,-60,9", got)
	}

	editor.setPos(8.5, -60, 8.5, 90, 0, true)
	if r := s.runSetblock("Editor", []string{"^", "^", "^1", "diamond_block"}); r.fail != nil {
		t.Fatalf("direct yaw90 setblock: %+v", r)
	}
	diamond, _ := world.BlockState("diamond_block")
	if got := d.w.GetBlock(world.Pos{X: 7, Y: -60, Z: 8}); got != diamond {
		t.Fatalf("direct yaw90 block %v, want diamond at 7,-60,8", got)
	}

	if r := s.runExecute("Editor", strings.Fields("positioned 8.5 -58.5 8.5 anchored eyes run setblock ^ ^ ^ emerald_block")); r.fail != nil {
		t.Fatalf("eyes-anchored setblock: %+v", r)
	}
	emerald, _ := world.BlockState("emerald_block")
	if got := d.w.GetBlock(world.Pos{X: 8, Y: -57, Z: 8}); got != emerald {
		t.Fatalf("eyes-anchored block %v, want emerald at 8,-57,8", got)
	}

	editor.setPos(8.5, -60, 8.5, 0, 0, true)
	if r := s.runFill("Editor", []string{"^4", "^", "^", "^4", "^", "^2", "stone"}); r.fail != nil {
		t.Fatalf("local fill: %+v", r)
	}
	stone, _ := world.BlockState("stone")
	for z := 8; z <= 10; z++ {
		if got := d.w.GetBlock(world.Pos{X: 12, Y: -60, Z: z}); got != stone {
			t.Fatalf("local fill z%d %v, want stone", z, got)
		}
	}

	d.w.SetBlock(world.Pos{X: 16, Y: -60, Z: 8}, stone)
	d.w.SetBlock(world.Pos{X: 16, Y: -60, Z: 9}, gold)
	d.w.SetBlock(world.Pos{X: 16, Y: -60, Z: 10}, emerald)
	if r := s.runClone("Editor", strings.Fields("from minecraft:overworld 16 -60 8 16 -60 10 16 -60 12")); r.fail != nil {
		t.Fatalf("absolute clone: %+v", r)
	}
	if got := d.w.GetBlock(world.Pos{X: 16, Y: -60, Z: 13}); got != gold {
		t.Fatalf("absolute clone middle %v, want gold", got)
	}
	if r := s.runExecute("Editor", strings.Fields("positioned 16.5 -60 8.5 rotated 0 0 run clone from minecraft:overworld ^ ^ ^ ^ ^ ^2 ^ ^ ^4")); r.fail != nil {
		t.Fatalf("local clone: %+v", r)
	}
	if got := d.w.GetBlock(world.Pos{X: 16, Y: -60, Z: 13}); got != gold {
		t.Fatalf("local clone middle %v, want gold", got)
	}
}
