package world

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLevelSeedPresence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   any
		want    int64
		present bool
	}{{"zero", int64(0), 0, true}, {"one", int64(1), 1, true}, {"wrong-type", int32(0), 0, false}, {"absent", nil, 0, false}, {"missing-key", "missing-key", 0, false}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := WriteLevel(LevelData{Dir: dir}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "data", "minecraft", "world_gen_settings.dat")
			if tc.value == nil {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				payload := map[string]any{"seed": tc.value}
				if tc.name == "missing-key" {
					delete(payload, "seed")
				}
				if err := writeGzipNBT(path, "", savedData(payload)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ReadLevel(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got.Seed != tc.want || got.HasSeed != tc.present {
				t.Fatalf("seed=%d present=%t", got.Seed, got.HasSeed)
			}
		})
	}
}
