package world

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLevelRoundTrip pins mc-go's own reading of the files it writes,
// which is what a server restarted on its own world directory does.
// The cross-server half of this - that the real 26.2 server reads these
// same files - is the blackbox suite's, which boots the jar on a
// directory mc-go saved.
func TestLevelRoundTrip(t *testing.T) {
	// The directory does not exist yet: saving a world for the first
	// time is the ordinary case, and the real server creates the
	// directory then too.
	dir := filepath.Join(t.TempDir(), "nested", "world")
	want := LevelData{
		Dir:                dir,
		LevelName:          "parity",
		GameType:           1,
		Difficulty:         "normal",
		Seed:               1,
		GenerateStructures: true,
		GameTime:           4242,
		DayTime:            6000,
		Spawn:              [3]int{0, -60, 0},
		GameRules: map[string]string{
			"minecraft:advance_time":                     "false",
			"minecraft:max_block_modifications":          "32768",
			"minecraft:fire_spread_radius_around_player": "128",
		},
	}
	if err := WriteLevel(want); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, f := range []string{"level.dat", "data/minecraft/world_gen_settings.dat", "data/minecraft/world_clocks.dat", "data/minecraft/game_rules.dat"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("the real server reads %s unconditionally: %v", f, err)
		}
	}

	got, err := ReadLevel(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.LevelName != want.LevelName {
		t.Errorf("level name = %q, want %q", got.LevelName, want.LevelName)
	}
	if got.Seed != want.Seed {
		t.Errorf("seed = %d, want %d", got.Seed, want.Seed)
	}
	if got.GameTime != want.GameTime {
		t.Errorf("game time = %d, want %d", got.GameTime, want.GameTime)
	}
	if got.DayTime != want.DayTime {
		t.Errorf("day time = %d, want %d", got.DayTime, want.DayTime)
	}
	if got.Spawn != want.Spawn {
		t.Errorf("spawn = %v, want %v", got.Spawn, want.Spawn)
	}
	if got.Difficulty != want.Difficulty {
		t.Errorf("difficulty = %q, want %q", got.Difficulty, want.Difficulty)
	}
	// The rules come back as the server states them even though the
	// file holds them as typed NBT: a byte for the boolean rules, an
	// int for the counted ones.
	for name, v := range want.GameRules {
		if got.GameRules[name] != v {
			t.Errorf("game rule %s = %q, want %q", name, got.GameRules[name], v)
		}
	}
}
