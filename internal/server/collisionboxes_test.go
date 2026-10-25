package server

import (
	"math"
	"testing"

	"mc-go/internal/world"
)

func mkBlockState(t *testing.T, name string, kv map[string]string) world.Block {
	t.Helper()
	b, ok := world.BlockStateOf(name, kv)
	if !ok {
		t.Fatalf("BlockStateOf(%s,%v) not found", name, kv)
	}
	return b
}

// Collision-shape decomposition pins — vanilla's per-state collision
// boxes in cell-local units (fractions of a block).
func TestCollisionBoxesTable(t *testing.T) {
	t.Run("default full cube", func(t *testing.T) {
		if _, ok := collisionBoxesOf(mkBlockState(t, "stone", nil)); ok {
			t.Fatal("stone should fall back to the full-cube default")
		}
	})
	t.Run("bottom slab", func(t *testing.T) {
		got, ok := collisionBoxesOf(mkBlockState(t, "stone_slab", map[string]string{"type": "bottom"}))
		if !ok || len(got) != 1 || got[0][4] != 0.5 {
			t.Fatalf("bottom slab boxes=%v ok=%v, want [0..0.5] plate", got, ok)
		}
	})
	t.Run("top slab", func(t *testing.T) {
		got, ok := collisionBoxesOf(mkBlockState(t, "stone_slab", map[string]string{"type": "top"}))
		if !ok || len(got) != 1 || got[0][1] != 0.5 {
			t.Fatalf("top slab boxes=%v ok=%v, want [0.5..1] plate", got, ok)
		}
	})
	t.Run("double slab full", func(t *testing.T) {
		if _, ok := collisionBoxesOf(mkBlockState(t, "stone_slab", map[string]string{"type": "double"})); ok {
			t.Fatal("double slab should be the full cube")
		}
	})
	t.Run("snow layers", func(t *testing.T) {
		got, ok := collisionBoxesOf(mkBlockState(t, "snow", map[string]string{"layers": "4"}))
		if !ok || len(got) != 1 || got[0][4] != 0.5 {
			t.Fatalf("snow[4] boxes=%v ok=%v, want 0.5 top", got, ok)
		}
	})
	t.Run("open gate empty", func(t *testing.T) {
		got, ok := collisionBoxesOf(mkBlockState(t, "oak_fence_gate", map[string]string{"open": "true"}))
		if !ok || len(got) != 0 {
			t.Fatalf("open gate boxes=%v ok=%v, want empty", got, ok)
		}
	})
	t.Run("powder snow empty", func(t *testing.T) {
		got, ok := collisionBoxesOf(mkBlockState(t, "powder_snow", nil))
		if !ok || len(got) != 0 {
			t.Fatalf("powder_snow boxes=%v ok=%v, want empty", got, ok)
		}
	})
	t.Run("trapdoor closed bottom", func(t *testing.T) {
		got, ok := collisionBoxesOf(mkBlockState(t, "oak_trapdoor", map[string]string{"open": "false", "half": "bottom", "facing": "north"}))
		if !ok || len(got) != 1 || got[0][4] != 0.1875 {
			t.Fatalf("closed bottom trapdoor boxes=%v ok=%v, want 3/16 plate", got, ok)
		}
	})
	t.Run("trapdoor open plate", func(t *testing.T) {
		got, ok := collisionBoxesOf(mkBlockState(t, "oak_trapdoor", map[string]string{"open": "true", "facing": "south"}))
		if !ok || len(got) != 1 || got[0][2] != 0.8125 {
			t.Fatalf("open south trapdoor boxes=%v ok=%v, want plate at z>=0.8125", got, ok)
		}
	})
	t.Run("stairs straight", func(t *testing.T) {
		got, ok := collisionBoxesOf(mkBlockState(t, "oak_stairs", map[string]string{"facing": "north", "half": "bottom", "shape": "straight"}))
		if !ok || len(got) != 2 {
			t.Fatalf("straight stairs boxes=%v ok=%v, want plate+quarter row", got, ok)
		}
	})
	t.Run("door closed plate", func(t *testing.T) {
		got, ok := collisionBoxesOf(mkBlockState(t, "oak_door", map[string]string{"open": "false", "facing": "east", "half": "lower"}))
		if !ok || len(got) != 1 || got[0][0] != 0.8125 {
			t.Fatalf("closed east door boxes=%v ok=%v, want plate at x>=0.8125", got, ok)
		}
	})
}

// clipSolidSegment honours per-cell collision boxes — a ray over a
// bottom slab passes while the same ray through a top slab clips.
func TestClipSolidSegmentShapes(t *testing.T) {
	s := New("127.0.0.1:0")
	d := s.dimensionByName("overworld")
	if d == nil {
		t.Skip("no overworld in test world")
	}
	for x := 6; x <= 10; x++ {
		for z := 8; z <= 12; z++ {
			d.w.SetBlock(world.Pos{X: x, Y: -61, Z: z}, mkBlockState(t, "stone", nil))
		}
	}
	d.w.SetBlock(world.Pos{X: 8, Y: -60, Z: 10}, mkBlockState(t, "stone_slab", map[string]string{"type": "bottom"}))
	// Ray across the slab at y=-59.4 — over the 0.5 top.
	if _, _, _, _, _, _, _, hit := s.clipSolidSegment(d, 8.5, -59.4, 8.5, 8.5, -59.4, 12.5); hit {
		t.Fatal("ray over a bottom slab should pass")
	}
	// Ray at y=-59.57 — inside the slab's solid half (top -59.5).
	bx, by, bz, _, _, _, _, hit := s.clipSolidSegment(d, 8.5, -59.57, 8.5, 8.5, -59.57, 12.5)
	if !hit || bx != 8 || by != -60 || bz != 10 {
		t.Fatalf("ray through the slab half: hit=%v cell=(%d,%d,%d)", hit, bx, by, bz)
	}
	// Top slab at the same cell flips both results.
	d.w.SetBlock(world.Pos{X: 8, Y: -60, Z: 10}, mkBlockState(t, "stone_slab", map[string]string{"type": "top"}))
	if _, _, _, _, _, _, _, hit := s.clipSolidSegment(d, 8.5, -59.6, 8.5, 8.5, -59.6, 12.5); hit {
		t.Fatal("ray under a top slab should pass")
	}
	if _, _, _, _, _, _, _, hit := s.clipSolidSegment(d, 8.5, -59.3, 8.5, 8.5, -59.3, 12.5); !hit {
		t.Fatal("ray through a top slab should clip")
	}
	// An open gate's empty shape never clips.
	d.w.SetBlock(world.Pos{X: 8, Y: -60, Z: 10}, mkBlockState(t, "oak_fence_gate", map[string]string{"open": "true"}))
	if _, _, _, _, _, _, _, hit := s.clipSolidSegment(d, 8.5, -59.4, 8.5, 8.5, -59.4, 12.5); hit {
		t.Fatal("ray through an open gate should pass")
	}
	// Snow[layers=2] tops at -59.75: a ray at -59.57 clears it, a
	// ray at -59.8 clips the column.
	d.w.SetBlock(world.Pos{X: 8, Y: -60, Z: 10}, mkBlockState(t, "snow", map[string]string{"layers": "2"}))
	if _, _, _, _, _, _, _, hit := s.clipSolidSegment(d, 8.5, -59.57, 8.5, 8.5, -59.57, 12.5); hit {
		t.Fatal("ray over snow[layers=2] should pass")
	}
	if _, _, _, _, _, _, _, hit := s.clipSolidSegment(d, 8.5, -59.8, 8.5, 8.5, -59.8, 12.5); !hit {
		t.Fatal("ray through snow[layers=2] should clip")
	}
	// A ray STARTING inside a box clips at the origin.
	d.w.SetBlock(world.Pos{X: 8, Y: -60, Z: 10}, mkBlockState(t, "stone", nil))
	_, _, _, _, hx, hy, hz, hit := s.clipSolidSegment(d, 8.5, -59.5, 10.5, 8.5, -59.5, 12.5)
	if !hit || math.Abs(hx-8.5) > 1e-9 || math.Abs(hy+59.5) > 1e-9 || math.Abs(hz-10.5) > 1e-9 {
		t.Fatalf("origin-inside ray: hit=%v h=(%v,%v,%v)", hit, hx, hy, hz)
	}
}
