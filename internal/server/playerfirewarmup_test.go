package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestPlayerFireBlockWarmupAndDamage(t *testing.T) {
	for _, c := range []struct {
		block  string
		damage float32
	}{{"fire", 1}, {"soul_fire", 2}} {
		t.Run(c.block, func(t *testing.T) {
			s := simulationTestServer(t)
			p := survivalSession(t, s, "FireWarmup")
			s.simulationAction(func() {
				s.difficulty = DifficultyNormal
				record := s.entities.Spawn(&Entity{Type: mustEntityType("player"), Dim: dimOverworld})
				p.entityID.Store(record.ID)
				p.setPos(.5, .1, .5, 0, 0, true)
				pe, _ := s.entities.GetPtr(record.ID)
				pe.TicksFrozen = 30
				p.fireTicks = -20
				s.world.SetBlock(world.Pos{}, world.BlockStateMust(c.block))
				s.playerInsideBlocks(p)
				if p.fireTicks != -19 || pe.TicksFrozen != 0 || p.health != 20-c.damage {
					t.Fatalf("first contact fire%d frozen%d health%g; want-19/0/%g", p.fireTicks, pe.TicksFrozen, p.health, 20-c.damage)
				}
				for n := 2; n <= 20; n++ {
					s.playerInsideBlocks(p)
					want := int32(n - 20)
					if n == 20 {
						want = 160
					}
					if p.fireTicks != want {
						t.Fatalf("contact%d fire%d want%d", n, p.fireTicks, want)
					}
				}
				p.fireTicks = 300
				for n := 0; n < 32; n++ {
					before := p.fireTicks
					s.playerInsideBlocks(p)
					if delta := p.fireTicks - before; delta < 1 || delta > 2 {
						t.Fatalf("positive contact delta%d, want[1,2]", delta)
					}
				}
			})
		})
	}
}
