package world

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestFillBoxPersistenceAcrossChunkBoundaries(t *testing.T) {
	w := New(nil, 8)
	t.Cleanup(w.Stop)
	coords := []int{-33, -32, -17, -16, -1, 0, 15, 16, 31, 32}
	states := []Block{Air, Stone, Dirt, Grass}
	want := make(map[Pos]Block)
	for zi, z := range coords {
		for xi, x := range coords {
			for yi, y := range []int{-64, -61, 0, 15, 16} {
				p := Pos{X: x, Y: y, Z: z}
				state := states[(xi+zi+yi)%len(states)]
				w.SetBlock(p, state)
				want[p] = state
			}
		}
	}
	path := filepath.Join(t.TempDir(), "world.json")
	if err := w.Save(path); err != nil {
		t.Fatal(err)
	}
	// Persistence carries block facts, not ownership buckets. Loading into a
	// different worker count must preserve every explicit override, including air.
	loaded := New(nil, 3)
	t.Cleanup(loaded.Stop)
	if err := loaded.Load(path); err != nil {
		t.Fatal(err)
	}
	if got := loaded.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatal("loading with a different worker count changed block overrides")
	}
	min, max := Pos{-34, -65, -34}, Pos{33, 17, 33}
	dx, dy, dz := max.X-min.X+1, max.Y-min.Y+1, max.Z-min.Z+1
	for _, candidate := range []*World{w, loaded} {
		out := make([]Block, dx*dy*dz)
		candidate.FillBox(min, max, out)
		for y := min.Y; y <= max.Y; y++ {
			for z := min.Z; z <= max.Z; z++ {
				for x := min.X; x <= max.X; x++ {
					p := Pos{X: x, Y: y, Z: z}
					expected, ok := want[p]
					if !ok {
						expected = flat(p)
					}
					index := ((y-min.Y)*dz+(z-min.Z))*dx + x - min.X
					if out[index] != expected {
						t.Fatalf("(%d,%d,%d): FillBox=%d, want %d", x, y, z, out[index], expected)
					}
				}
			}
		}
	}
}
