package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestPlayerInactiveFireContactSettlement(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "FireSettle")
	s.simulationAction(func() {
		pe := s.entities.Spawn(&Entity{Type: mustEntityType("player"), Dim: dimOverworld})
		p.entityID.Store(pe.ID)
		p.setPos(.5, .1, .5, 0, 0, true)
		for _, c := range []struct {
			block        string
			before, want int32
		}{{"air", 0, -20}, {"air", -20, -20}, {"air", -7, -20}, {"air", -30, -20}, {"air", 73, 73}, {"water", 100, -20}, {"water", -7, -20}, {"powder_snow", 100, -20}, {"powder_snow", -20, -20}} {
			s.world.SetBlock(world.Pos{}, world.BlockStateMust(c.block))
			p.fireTicks = c.before
			health := p.health
			s.playerInsideBlocks(p)
			if p.fireTicks != c.want || p.health != health {
				t.Fatalf("%s fire%d -> %d health%g; want%d unchanged health", c.block, c.before, p.fireTicks, p.health, c.want)
			}
			if c.block == "powder_snow" {
				melted := s.world.GetBlock(world.Pos{}) == world.Air
				if melted != (c.before > 0) {
					t.Fatal("normalization changed snow melt predicate", c.before, melted)
				}
			}
		}
	})
}

func TestPlayerInactiveFirePreservesWarmup(t *testing.T) {
	for _, c := range []struct{ before, after, want int32 }{{-20, -19, -19}, {-1, 0, 0}, {0, 1, 1}, {100, 99, 99}, {100, 0, -20}, {0, 0, -20}, {-20, -20, -20}, {-30, -30, -20}} {
		if got := settledPlayerFire(c.before, c.after); got != c.want {
			t.Errorf("contact fire%d -> %d settled%d want%d", c.before, c.after, got, c.want)
		}
	}
}
