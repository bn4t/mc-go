package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestFluidOpposingCollisionFaces(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.world.Stop)
	d := s.dims[dimOverworld]
	state := func(name string, props map[string]string) world.Block { return mkBlockState(t, name, props) }
	bottom := state("stone_slab", map[string]string{"type": "bottom"})
	top := state("stone_slab", map[string]string{"type": "top"})
	trapEast := state("oak_trapdoor", map[string]string{"open": "true", "facing": "east"})
	trapWest := state("oak_trapdoor", map[string]string{"open": "true", "facing": "west"})
	stairsEast := state("oak_stairs", map[string]string{"facing": "east"})
	stairsWest := state("oak_stairs", map[string]string{"facing": "west"})
	water := fluidState(fluidWater, 0)
	type check struct {
		name     string
		from, to world.Block
		dir      world.Pos
		pass     bool
	}
	var cases []check
	for _, dir := range []world.Pos{{X: 1}, {X: -1}, {Z: 1}, {Z: -1}} {
		cases = append(cases, check{"complementary", bottom, top, dir, false}, check{"matching", bottom, bottom, dir, true})
	}
	cases = append(cases,
		check{"near trapdoor", water, trapEast, world.Pos{X: 1}, false},
		check{"far trapdoor", water, trapWest, world.Pos{X: 1}, true},
		check{"source trapdoor", trapWest, water, world.Pos{X: 1}, false},
		check{"near stairs", water, stairsWest, world.Pos{X: 1}, false},
		check{"far stairs", water, stairsEast, world.Pos{X: 1}, true},
		check{"bottom downward", bottom, world.Air, world.Pos{Y: -1}, false},
		check{"top downward", top, world.Air, world.Pos{Y: -1}, true},
		check{"bottom target", water, bottom, world.Pos{Y: -1}, true},
		check{"top target", water, top, world.Pos{Y: -1}, false},
		check{"pot", water, state("decorated_pot", nil), world.Pos{X: 1}, true},
		check{"ladder back", water, state("ladder", map[string]string{"facing": "north"}), world.Pos{Z: -1}, false},
		check{"ladder front", water, state("ladder", map[string]string{"facing": "north"}), world.Pos{Z: 1}, true},
		check{"scaffold empty-context underside", state("scaffolding", map[string]string{"bottom": "true"}), world.Air, world.Pos{Y: -1}, true},
		check{"scaffold top", water, state("scaffolding", nil), world.Pos{Y: -1}, false},
	)
	for i, tc := range cases {
		a := world.Pos{X: 0, Y: 160, Z: 0}
		b := world.Pos{X: tc.dir.X, Y: a.Y + tc.dir.Y, Z: tc.dir.Z}
		d.w.SetBlock(a, tc.from)
		d.w.SetBlock(b, tc.to)
		if got := s.fluidCanPassThroughWall(d, a, b); got != tc.pass {
			t.Errorf("case%d %s pass=%t want%t", i, tc.name, got, tc.pass)
		}
		if got := s.fluidCanPassThroughWall(d, b, a); got != tc.pass {
			t.Errorf("case%d %s reversed pass=%t want%t", i, tc.name, got, tc.pass)
		}
	}
}
