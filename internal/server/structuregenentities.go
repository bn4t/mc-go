package server

import "mc-go/internal/worldgen"

// structureGenEntityStep admits generated structure residents independently
// of the natural-spawn rule. End crystals stay queued for the existing End
// fight phase, preserving that feature's lifecycle.
func (s *Server) structureGenEntityStep() {
	for _, d := range s.dims {
		terrain := d.terrain
		if terrain == nil {
			continue
		}
		terrain.freshMu.Lock()
		var residents []worldgen.GenEntity
		keep := terrain.genEntities[:0]
		for _, entity := range terrain.genEntities {
			if entity.Structure == "swamp_hut" {
				residents = append(residents, entity)
			} else {
				keep = append(keep, entity)
			}
		}
		terrain.genEntities = keep
		terrain.freshMu.Unlock()
		if len(residents) > 0 {
			s.spawnGenEntities(d, residents)
		}
	}
}

// spawnHutResident applies the native resident properties before pairing.
// Constructor/finalization random values use the existing mob mechanisms;
// these independent entity draws do not consume decoration random.
func (s *Server) spawnHutResident(d *dimension, g worldgen.GenEntity) *Entity {
	if g.Type != "witch" && g.Type != "cat" {
		return nil
	}
	entity, ok := s.summonMobRaw(d, g.Type, g.X, g.Y, g.Z, false, false)
	if !ok {
		return nil
	}
	entity.Yaw, entity.Pitch = g.Yaw, 0
	entity.HeadYaw, entity.LookYaw = g.Yaw, g.Yaw
	// Finalize once below; summon initialization would install a separate
	// summon-path modifier before the permanent resident finalization.
	entity.PersistenceRequired = true
	entity.SoundVariant = rollSoundVariant(g.Type)
	s.mobDefaultEquip(entity)
	if g.Type == "cat" {
		entity.VariantName = "all_black"
	} else {
		entity.CanJoinRaid = true
	}
	if !entity.Dormant {
		s.broadcastFreshMob(entity)
	}
	return entity
}
