package server

import (
	"math"
	"math/rand"

	"mc-go/internal/world"
)

// cureTickStep advances zombie-villager conversion. Vanilla's
// ZombieVillager.tick decrements villagerConversionTime by
// getConversionProgress each tick while isConverting; at zero the
// zombie is replaced by a villager (finishConversion) and the player
// who started the cure earns cured_zombie_villager.
func (s *Server) cureTickStep() {
	zvType := mustEntityType("zombie_villager")
	s.entities.mu.RLock()
	var zvs []*Entity
	for _, e := range s.entities.byID {
		if e.Type == zvType && e.Converting && !e.Dying {
			zvs = append(zvs, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range zvs {
		e.ConversionTime -= int32(cureProgress(s, e))
		if e.ConversionTime <= 0 {
			s.finishCure(e)
		}
	}
}

// cureProgress mirrors getConversionProgress: 1 per tick, plus a 1%
// roll that scans the 9x9x9 box around the zombie for beds (each found
// bed, capped at 14, adds 1 with 30% probability).
func cureProgress(s *Server, e *Entity) int {
	progress := 1
	if rand.Float32() >= 0.01 {
		return progress
	}
	w := s.dimWorld(e.Dim)
	if w == nil {
		return progress
	}
	bx, by, bz := int(math.Floor(e.X)), int(math.Floor(e.Y)), int(math.Floor(e.Z))
	found := 0
	for x := bx - 4; x <= bx+4 && found < 14; x++ {
		for y := by - 4; y <= by+4 && found < 14; y++ {
			for z := bz - 4; z <= bz+4 && found < 14; z++ {
				if isBedBlock(w.GetBlock(world.Pos{X: x, Y: y, Z: z})) {
					found++
					if rand.Float32() < 0.3 {
						progress++
					}
				}
			}
		}
	}
	return progress
}

// finishCure replaces the zombie villager with a villager (new entity
// id, same position/villager data/offers), fires the advancement
// trigger for the curing player, and plays the cure level event.
func (s *Server) finishCure(e *Entity) {
	s.entities.Remove(e.ID)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})

	v := s.entities.Spawn(&Entity{
		Type:       mustEntityType("villager"),
		Dim:        e.Dim,
		X:          e.X,
		Y:          e.Y,
		Z:          e.Z,
		Yaw:        e.Yaw,
		Pitch:      e.Pitch,
		Living:     true,
		NoAI:       e.NoAI,
		Profession: e.Profession,
		Offers:     e.Offers,
		Baby:       e.Baby,
	})
	s.broadcastEntitySpawn(nil, *v)

	if e.HasConvStarter {
		if sess := s.sessionByUUID(e.ConversionStarter); sess != nil {
			s.fireCuredZombieVillager(sess)
		}
	}
	if !e.Silent {
		s.broadcastLevelEvent(e.Dim, 1027, world.Pos{
			X: int(math.Floor(e.X)),
			Y: int(math.Floor(e.Y)),
			Z: int(math.Floor(e.Z)),
		}, 0)
	}
}
