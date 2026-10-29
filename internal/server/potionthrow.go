package server

import (
	"math"
	"mc-go/internal/world"
)

// thrownPotionImpact separates the measured 26.2 splash and lingering arms.
// ThrowablePotionItem.use shoots at power .5 with a -20 degree offset;
// ThrownLingeringPotion leaves a radius-3, 600-tick cloud after a 10-tick wait.
// Evidence: javap (JDK 25) on the pinned 26.2 jar and
// TestPlayerPotionThrowParity's vanilla wire observations.
func (s *Server) thrownPotionImpact(e *Entity, name string, x, y, z float64) {
	if name != "lingering_potion" || e.splashPotion == "water" {
		s.potionSplash(e, x, y, z)
		return
	}
	contents := splashPotionContents[e.splashPotion]
	s.broadcastLevelEvent(e.Dim, potionLevelEvent(e.splashPotion), world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}, splashPotionColors[e.splashPotion])
	if len(contents) == 0 {
		return
	}
	cloudType, ok := EntityTypeID("area_effect_cloud")
	if !ok {
		return
	}
	effects := map[int32]mobEffectInst{}
	for _, eff := range contents {
		if id, ok := MobEffectID(eff.name); ok {
			expiry := int64(-1)
			if eff.dur > 0 {
				expiry = s.gameTime() + int64(eff.dur)
			}
			effects[id] = mobEffectInst{amplifier: eff.amp, expiry: expiry}
		}
	}
	cloud := s.entities.Spawn(&Entity{Type: cloudType, Dim: e.Dim, X: x, Y: y, Z: z,
		CloudPotionContents: e.splashPotion, CloudWait: 10, CloudDuration: 600, CloudRadius: 3, CloudRadiusPerTick: -3.0 / 600,
		CloudRadiusOnUse: -.5, CloudVictims: map[int32]int32{}, Effects: effects, CloudOwnerID: e.arrowShooter})
	s.broadcastEntitySpawn(nil, *cloud)
	s.broadcastEntityMetadata(cloud.ID, encodeCloudMeta(cloud))
}

// potionCloudTicks returns the immutable quarter-dose duration of a bottled
// effect. AreaEffectCloud builds each dose from PotionContents, not its age.
func potionCloudTicks(e *Entity, id int32, inst mobEffectInst, now int64) int32 {
	if e.CloudPotionContents != "" {
		for _, eff := range splashPotionContents[e.CloudPotionContents] {
			eid, _ := MobEffectID(eff.name)
			if eid == id {
				if eff.dur == 0 {
					return -1
				}
				return eff.dur / 4
			}
		}
	}
	if inst.expiry < 0 {
		return -1
	}
	return int32((inst.expiry - now) / 4)
}
