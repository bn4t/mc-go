package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestPlayerPowderVerticalContactCounts(t *testing.T) {
	for _, c := range []struct {
		name            string
		y, gravity      float64
		below, levitate bool
		want            int32
	}{
		{"floor", 0, .08, false, false, 1},
		{"new-lower-row", 1, .08, true, false, 2},
		{"lower-air", 1, .08, false, false, 1},
		{"fractional", .5, .08, true, false, 1},
		{"small-clipped-gap", .01, .08, true, false, 1},
		{"zero-gravity", 1, 0, true, false, 1},
		{"levitation", 1, .08, true, true, 1},
		{"new-upper-row", .199, .08, true, true, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := simulationTestServer(t)
			p := survivalSession(t, s, "PowderCounts")
			s.simulationAction(func() {
				record := s.entities.Spawn(&Entity{Type: mustEntityType("player"), Dim: dimOverworld})
				p.entityID.Store(record.ID)
				p.setPos(.5, c.y, .5, 0, 0, false)
				pe, _ := s.entities.GetPtr(record.ID)
				pe.AttrBase = map[string]float64{"gravity": c.gravity}
				if c.levitate {
					p.effects = map[int32]mobEffectInst{effectLevitation: {expiry: 100}}
				}
				s.world.SetBlock(world.Pos{Y: -1}, world.BlockStateMust("stone"))
				for y := 0; y <= 3; y++ {
					s.world.SetBlock(world.Pos{Y: y}, world.BlockStateMust("powder_snow"))
				}
				if !c.below && c.y >= 1 {
					s.world.SetBlock(world.Pos{}, world.Air)
				}
				for frame := int32(1); frame <= 5; frame++ {
					s.playerInsideBlocks(p)
					s.playerPowderTick(p, pe)
					s.entityInsideStep()
					if pe.TicksFrozen != c.want*frame {
						t.Fatalf("frame%d frozen%d want%d", frame, pe.TicksFrozen, c.want*frame)
					}
				}
			})
		})
	}
}
