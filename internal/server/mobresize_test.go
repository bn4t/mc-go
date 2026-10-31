package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestMobResizeGuardFacts(t *testing.T) {
	old := [2]float64{.6, 1.8}
	for _, tc := range []struct {
		name                   string
		next                   [2]float64
		first, noPhysics, want bool
	}{
		{"admitted", [2]float64{1.2, 3.6}, false, false, true},
		{"firstTick", [2]float64{1.2, 3.6}, true, false, false},
		{"noPhysics", [2]float64{1.2, 3.6}, false, true, false},
		{"tooLarge", [2]float64{5, 3.6}, false, false, false},
		{"shrink", [2]float64{.3, .9}, false, false, false},
		{"widthOnly", [2]float64{1.2, 1.8}, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mobResizeCanAdjust(old, tc.next, tc.first, tc.noPhysics); got != tc.want {
				t.Fatalf("got%t want%t", got, tc.want)
			}
		})
	}
}

func TestMobResizeSolidCenters(t *testing.T) {
	old := [2]float64{1.396484375, float64(float32(1.6))}
	next := [2]float64{float64(float32(old[0]) * 2), float64(float32(old[1]) * 2)}
	for _, tc := range []struct {
		name           string
		floor, ceiling bool
		y              float64
	}{
		{"air", false, false, 161.19999998807907},
		{"floor", true, false, 162},
		{"ceiling", false, true, 160.79999995231628},
		{"sealed", true, true, 162.000001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := world.NewGen(nil, 1, func(world.Pos) world.Block { return world.Air })
			t.Cleanup(w.Stop)
			for x := 3; x <= 14; x++ {
				for z := -5; z <= 5; z++ {
					if tc.floor {
						w.SetBlock(world.Pos{X: x, Y: 161, Z: z}, world.Stone)
					}
					if tc.ceiling {
						w.SetBlock(world.Pos{X: x, Y: 164, Z: z}, world.Stone)
					}
				}
			}
			p, ok := mobResizePosition(w, [3]float64{8.5, 162, .5}, old, next)
			if !ok || p[0] != 8.5 || p[2] != .5 || math.Abs(p[1]-tc.y) > 1e-8 {
				t.Fatalf("position%v success%t wantY%.17g", p, ok, tc.y)
			}
		})
	}
}

func TestMobResizeCachedDimensionScale(t *testing.T) {
	e := &Entity{Type: mustEntityType("horse"), Living: true, AttrBase: map[string]float64{"scale": 2}}
	base := entityBBDims(e)
	if base != entityBaseBBDims(e) {
		t.Fatal("unconsumed attribute changed cached box")
	}
	e.mobCollisionScale = 2
	if got := entityBBDims(e); got != [2]float64{float64(float32(base[0]) * 2), float64(float32(base[1]) * 2)} {
		t.Fatalf("scaled box%v", got)
	}
	e.mobCollisionScale = .5
	if got := entityBBDims(e); got[1] != float64(float32(base[1])*.5) {
		t.Fatalf("shrunken box%v", got)
	}
}

func TestMobResizeRefreshDoesNotAdvanceTracking(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	e := s.entities.Spawn(&Entity{Type: mustEntityType("horse"), Living: true, TickCount: 1, SyncCount: 7, X: 8.5, Y: 162, Z: .5, AttrBase: map[string]float64{"scale": 2}})
	e.firstTick = false
	s.refreshMobCollisionScale(e)
	if e.SyncCount != 7 {
		t.Fatalf("resize advanced tracker cadence to%d", e.SyncCount)
	}
	if e.mobCollisionScale != 2 || e.PosDirty || !e.NeedsSync || math.Abs(e.Y-161.19999998807907) > 1e-8 {
		t.Fatalf("scale/position not consumed: scale%g dirty%t y%.17g", e.mobCollisionScale, e.PosDirty, e.Y)
	}
	s.mobSyncLivingFrame(e)
	if e.SyncCount != 8 {
		t.Fatalf("one frame advanced tracker to%d want8", e.SyncCount)
	}

}
