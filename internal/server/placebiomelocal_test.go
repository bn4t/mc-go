package server

import (
	"strings"
	"testing"

	"mc-go/internal/world"
)

// TestPlaceFillBiomeLocalPositionGrammar pins the caret grammar and token
// index for /place structure, /place feature and both /fillbiome corners.
func TestPlaceFillBiomeLocalPositionGrammar(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	rows := []struct {
		cmd   string
		key   string
		token int
		off   int
	}{
		{"place structure swamp_hut ^ ~ ^1", "argument.pos.mixed", 3, 0},
		{"place structure swamp_hut ^NaN ^ ^", "parsing.double.expected", 3, 1},
		{"place structure swamp_hut ^ ^ ^1e2", "command.expected.separator", 5, 2},
		{"place feature minecraft:oak ^ ~ ^1", "argument.pos.mixed", 3, 0},
		{"place feature minecraft:oak ^NaN ^ ^", "parsing.double.expected", 3, 1},
		{"place feature minecraft:oak ^ ^ ^1e2", "command.expected.separator", 5, 2},
		{"fillbiome ^ ~ ^ ^ ^ ^1 minecraft:plains", "argument.pos.mixed", 1, 0},
		{"fillbiome ^ ^ ^ ^ ~ ^1 minecraft:plains", "argument.pos.mixed", 4, 0},
		{"fillbiome ^NaN ^ ^ ^ ^1 minecraft:plains", "parsing.double.expected", 1, 1},
		{"fillbiome ^ ^ ^ ^ ^ ^1e2 minecraft:plains", "command.expected.separator", 6, 2},
	}
	for _, row := range rows {
		args := strings.Fields(row.cmd)[1:]
		var got reply
		if strings.HasPrefix(row.cmd, "place ") {
			got = s.runPlace("", args)
		} else {
			got = s.runFillBiome("", args)
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

// TestPlaceFillBiomeLocalPositionSourceFrame pins direct and execute-rotated
// caret positions for /place structure and /fillbiome. The fill reply carries
// the normalized quart box, and the biome reads witness the actual writes.
func TestPlaceFillBiomeLocalPositionSourceFrame(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	editor := placeSession(t, s, "PlaceBiomeEditor", 8.5, -60, 8.5)
	// Structure placement emits a block-update burst; the default scripted
	// session's 64-packet mailbox would disconnect before the next command.
	editor.sendCh = make(chan sendPkt, 4096)
	d := s.dims[dimOverworld]

	checkPlace := func(r reply, name string, x, y, z int32) {
		t.Helper()
		if r.fail != nil || r.comp.translate != "commands.place.structure.success" || len(r.comp.with) != 4 {
			t.Fatalf("place %s: %+v", name, r)
		}
		pos := [4]any{r.comp.with[0], r.comp.with[1], r.comp.with[2], r.comp.with[3]}
		want := [4]any{nestedCompound{key: "", value: "minecraft:" + name}, nestedCompound{key: "", value: x}, nestedCompound{key: "", value: y}, nestedCompound{key: "", value: z}}
		if pos != want {
			t.Fatalf("place %s args = %#v, want %#v", name, pos, want)
		}
	}
	checkPlace(s.runPlace("PlaceBiomeEditor", []string{"structure", "swamp_hut", "^", "^", "^1"}), "swamp_hut", 8, -60, 9)
	if r := s.runExecute("PlaceBiomeEditor", strings.Fields("positioned 8.5 -60 8.5 rotated 90 0 run place structure swamp_hut ^ ^ ^1")); r.fail != nil {
		t.Fatalf("rotated place: %+v", r)
	} else if r.comp.translate != "commands.place.structure.success" || len(r.comp.with) != 4 {
		t.Fatalf("rotated place reply: %+v", r)
	} else if x := r.comp.with[1].(nestedCompound).value; x != int32(7) {
		t.Fatalf("rotated place x = %v, want 7", x)
	}

	checkFill := func(r reply, count, x0, y0, z0, x1, y1, z1 int32) {
		t.Helper()
		if r.fail != nil || r.comp.translate != "commands.fillbiome.success.count" || len(r.comp.with) != 7 {
			t.Fatalf("fillbiome: %+v", r)
		}
		want := []any{count, x0, y0, z0, x1, y1, z1}
		for i := range want {
			if r.comp.with[i] != want[i] {
				t.Fatalf("fillbiome args = %#v, want %#v", r.comp.with, want)
			}
		}
	}
	checkFill(s.runFillBiome("PlaceBiomeEditor", strings.Fields("^ ^ ^ ^ ^ ^1 minecraft:plains")), 1, 8, -60, 8, 8, -60, 8)
	if got := s.biomeAt(d, world.Pos{X: 8, Y: -60, Z: 9}); got != "minecraft:plains" {
		t.Fatalf("direct local biome = %q, want plains", got)
	}
	if r := s.runExecute("PlaceBiomeEditor", strings.Fields("positioned 8.5 -60 8.5 rotated 90 0 run fillbiome ^ ^ ^ ^ ^ ^1 minecraft:desert")); r.fail != nil {
		t.Fatalf("rotated fillbiome: %+v", r)
	} else if r.comp.translate != "commands.fillbiome.success.count" || len(r.comp.with) != 7 {
		t.Fatalf("rotated fillbiome reply: %+v", r)
	} else {
		want := []any{int32(2), int32(4), int32(-60), int32(8), int32(8), int32(-60), int32(8)}
		for i := range want {
			if r.comp.with[i] != want[i] {
				t.Fatalf("rotated fillbiome args = %#v, want %#v", r.comp.with, want)
			}
		}
	}
	if got := s.biomeAt(d, world.Pos{X: 7, Y: -60, Z: 8}); got != "minecraft:desert" {
		t.Fatalf("rotated local biome = %q, want desert", got)
	}
	if r := s.runFillBiome("PlaceBiomeEditor", strings.Fields("^ ^ ^ ^ ^ ^-5 minecraft:plains")); r.fail != nil {
		t.Fatalf("reversed local fillbiome: %+v", r)
	} else if r.comp.translate != "commands.fillbiome.success.count" || len(r.comp.with) != 7 {
		t.Fatalf("reversed local fillbiome reply: %+v", r)
	} else {
		want := []any{int32(3), int32(8), int32(-60), int32(0), int32(8), int32(-60), int32(8)}
		for i := range want {
			if r.comp.with[i] != want[i] {
				t.Fatalf("reversed local fillbiome args = %#v, want %#v", r.comp.with, want)
			}
		}
	}
	if got := s.biomeAt(d, world.Pos{X: 8, Y: -60, Z: 3}); got != "minecraft:plains" {
		t.Fatalf("reversed local biome = %q, want plains", got)
	}
}
