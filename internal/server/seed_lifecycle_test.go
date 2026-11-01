package server

import (
	"os"
	"path/filepath"
	"testing"

	"mc-go/internal/world"
)

func TestSeedLifecycle(t *testing.T) {
	t.Run("new-default", func(t *testing.T) {
		s := New("")
		defer s.Stop()
		if s.WorldSeed != 1 || s.worldSeed() != 1 {
			t.Fatalf("default seed=%d accessor=%d", s.WorldSeed, s.worldSeed())
		}
		s.WorldSeed = 0
		if s.worldSeed() != 0 {
			t.Fatal("explicit zero substituted")
		}
	})
	t.Run("default-normal-terrain", func(t *testing.T) {
		s := New("")
		defer s.Stop()
		s.LevelType = LevelTypeNormal
		if err := s.LoadWorld(); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{dimOverworld, dimNether, dimEnd} {
			d := s.dims[name]
			if d.terrain == nil || d.terrain.g.RS.Seed != 1 {
				t.Fatalf("%s default terrain is not seed 1", name)
			}
		}
	})

	for _, tc := range []struct {
		name                    string
		saved, configured, want int64
		missing                 bool
	}{{"saved-zero", 0, 1, 0, false}, {"saved-one", 1, 0, 1, false}, {"missing", 0, 23, 23, true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := world.WriteLevel(world.LevelData{Dir: dir, Seed: tc.saved, Generator: LevelTypeNormal}); err != nil {
				t.Fatal(err)
			}
			if tc.missing {
				if err := os.Remove(filepath.Join(dir, "data", "minecraft", "world_gen_settings.dat")); err != nil {
					t.Fatal(err)
				}
			}
			s := New("")
			defer s.Stop()
			s.WorldDir = dir
			s.WorldSeed = tc.configured
			s.LevelType = LevelTypeNormal
			if err := s.LoadWorld(); err != nil {
				t.Fatal(err)
			}
			if s.WorldSeed != tc.want || s.worldSeed() != tc.want {
				t.Fatalf("seed=%d accessor=%d want=%d", s.WorldSeed, s.worldSeed(), tc.want)
			}
			for _, name := range []string{dimOverworld, dimNether, dimEnd} {
				d := s.dims[name]
				if d.terrain == nil || d.terrain.g.RS.Seed != tc.want {
					t.Fatalf("%s terrain seed not %d", name, tc.want)
				}
			}
		})
	}
}
