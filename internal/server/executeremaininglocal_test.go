package server

import (
	"strings"
	"testing"

	"mc-go/internal/world"
)

// TestExecuteRemainingLocalPositionGrammar pins the shared source-frame
// grammar for the consumers added after if block/biome/loaded: a mixed triple
// is rejected at the offending token, and the local number scanner keeps Go's
// exponent, NaN and leading-plus extensions out.
func TestExecuteRemainingLocalPositionGrammar(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	rows := []struct {
		cmd   string
		key   string
		token int
		off   int
	}{
		{"positioned ^ ~ ^1 run setblock ~ ~ ~ stone", "argument.pos.mixed", 2, 0},
		{"positioned ^NaN ^ ^ run setblock ~ ~ ~ stone", "parsing.double.expected", 2, 1},
		{"positioned ^ ^ ^1e2 run setblock ~ ~ ~ stone", "command.expected.separator", 4, 2},
		{"facing ^ ~ ^1 run setblock ~ ~ ~ stone", "argument.pos.mixed", 2, 0},
		{"if blocks ^ ~ ^ ^ ^ ^2 8 -58 12 all run say X", "argument.pos.mixed", 3, 0},
		{"if items block ^ ~ ^ container.0 stone run say X", "argument.pos.mixed", 4, 0},
		{"if data block ^ ~ ^ Items run say X", "argument.pos.mixed", 4, 0},
		{"store result block ^ ~ ^ foo int 1 run data get storage z:z a", "argument.pos.mixed", 4, 0},
	}
	for _, row := range rows {
		got := s.runExecute("", strings.Fields(row.cmd))
		key := ""
		if len(got.comp.extra) > 0 {
			key = got.comp.extra[0].translate
		}
		if key != row.key || got.fail == nil || got.fail.token != row.token || got.fail.off != row.off {
			t.Errorf("%q = key %q token %+v, want %q at %d+%d", row.cmd, key, got.fail, row.key, row.token, row.off)
		}
	}
}

// TestExecuteLocalPositionedPlanAndFallback pins both dispatch paths: the
// plan compiler resolves positioned locally, and a chain that falls back to
// the legacy tail (here via facing) resolves the same way.
func TestExecuteLocalPositionedPlanAndFallback(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	placeSession(t, s, "LocalEye", 8.5, -60, 8.5)
	d := s.dims[dimOverworld]
	redstone, _ := world.BlockState("redstone_block")
	s.runExecute("", strings.Fields("positioned 8.5 -58.5 8.5 rotated 90 0 positioned ^1 ^2 ^3 run setblock ~ ~ ~ redstone_block"))
	if got := d.w.GetBlock(world.Pos{X: 5, Y: -57, Z: 9}); got != redstone {
		t.Fatalf("plan positioned local got %v, want redstone at 5,-57,9", got)
	}
	// facing is outside the plan grammar, so this chain uses the legacy tail
	// and still resolves the eyes-anchored local positioned argument.
	gold, _ := world.BlockState("gold_block")
	s.runExecute("LocalEye", strings.Fields("positioned 8.5 -58.5 8.5 anchored eyes facing 8.5 -58.5 9.5 positioned ^ ^ ^ run setblock ~ ~ ~ gold_block"))
	if got := d.w.GetBlock(world.Pos{X: 8, Y: -57, Z: 8}); got != gold {
		t.Fatalf("fallback positioned local got %v, want gold at 8,-57,8", got)
	}
}
