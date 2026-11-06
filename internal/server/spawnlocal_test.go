package server

import (
	"strings"
	"testing"
)

// TestSpawnLocalPositionGrammar pins the caret grammar and native cursor
// offsets for /spawnpoint (after an explicit target) and /setworldspawn.
func TestSpawnLocalPositionGrammar(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	placeSession(t, s, "SpawnEditor", 8.5, -60, 8.5)
	rows := []struct {
		cmd   string
		key   string
		token int
		off   int
	}{
		{"spawnpoint @s ^ ~ ^1", "argument.pos.mixed", 2, 0},
		{"spawnpoint @s ^NaN ^ ^", "parsing.double.expected", 2, 1},
		{"spawnpoint @s ^ ^ ^1e2", "command.expected.separator", 4, 2},
		{"setworldspawn ^ ~ ^", "argument.pos.mixed", 1, 0},
		{"setworldspawn ^NaN ^ ^", "parsing.double.expected", 1, 1},
		{"setworldspawn ^ ^ ^1e2", "command.expected.separator", 3, 2},
	}
	for _, row := range rows {
		args := strings.Fields(row.cmd)[1:]
		var got reply
		if strings.HasPrefix(row.cmd, "spawnpoint ") {
			got = s.runSpawnpoint("SpawnEditor", args)
		} else {
			got = s.runSetWorldSpawn("SpawnEditor", args)
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

// TestSpawnLocalPositionSourceFrame pins local spawn positions from a direct
// player and an execute-rotated frame.
func TestSpawnLocalPositionSourceFrame(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	editor := placeSession(t, s, "SpawnEditor", 8.5, -60, 8.5)
	if r := s.runSpawnpoint("SpawnEditor", []string{"@s", "^", "^", "^1"}); r.fail != nil {
		t.Fatalf("local spawnpoint: %+v", r)
	}
	editor.posMu.Lock()
	got := editor.spawnPos
	set := editor.hasSpawnPos
	editor.posMu.Unlock()
	if !set || got != [3]int{8, -60, 9} {
		t.Fatalf("spawnpoint local = %v set%v, want 8,-60,9", got, set)
	}
	if r := s.runSetWorldSpawn("SpawnEditor", []string{"^", "^", "^1", "45", "0"}); r.fail != nil {
		t.Fatalf("local setworldspawn: %+v", r)
	}
	x, y, z := s.WorldSpawn()
	if x != 8.5 || y != -60 || z != 9.5 {
		t.Fatalf("world spawn = %v %v %v, want 8.5,-60,9.5", x, y, z)
	}
	s.spawnMu.Lock()
	yaw, pitch := s.spawnYaw, s.spawnPitch
	s.spawnMu.Unlock()
	if yaw != 45 || pitch != 0 {
		t.Fatalf("world spawn angles = %v %v, want 45 0", yaw, pitch)
	}
	// An execute frame's rotation drives the same local resolution.
	if r := s.runExecute("SpawnEditor", strings.Fields("positioned 8.5 -60 10.5 rotated 180 0 run spawnpoint @s ^ ^ ^1")); r.fail != nil {
		t.Fatalf("execute-frame spawnpoint: %+v", r)
	}
	editor.posMu.Lock()
	got = editor.spawnPos
	editor.posMu.Unlock()
	if got != [3]int{8, -60, 9} {
		t.Fatalf("execute-frame spawnpoint = %v, want 8,-60,9", got)
	}
}
