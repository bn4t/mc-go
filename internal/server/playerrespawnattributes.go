package server

// resetPlayerBodyAttributes retains the old body's raw bases on ordinary
// death, but starts the new body without its modifiers or active effects.
// Retained equipment is materialized again by the normal equipment scan.
func (s *Server) resetPlayerBodyAttributes(sess *Session) float32 {
	sess.walkingSpeed = 0
	sess.walkingSpeedSet = false
	sess.effMu.Lock()
	sess.effects = nil
	sess.effMu.Unlock()
	sess.absorb = 0
	sess.levitationStart = nil
	sess.levitationStartTick = 0
	sess.raidOmenPos = nil
	maxHealth := playerAttrBases["max_health"]
	if player, ok := s.entities.GetPtr(sess.entityID.Load()); ok {
		// Copied instances belong to a new body and must be projected again,
		// including base-only instances whose value did not change.
		for name := range player.AttrTouched {
			player.markAttrDirty(name)
		}
		for name := range player.AttrBase {
			player.markAttrDirty(name)
		}
		for name := range player.AttrMods {
			player.markAttrDirty(name)
		}
		player.AttrMods = nil
		player.playerEquipmentAttrsInit = false
		player.playerArmorAttrsInit = false
		if base, present := player.AttrBase["max_health"]; present {
			maxHealth = base
		}
	}
	return float32(sanitizeAttributeValue("max_health", maxHealth))
}
