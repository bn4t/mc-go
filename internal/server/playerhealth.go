package server

// playerMaxHealth reads canonical bases and modifiers, including active
// Health Boost and explicit edits of its modifier, then narrows like the
// living entity's float maximum-health getter.
func (s *Server) playerMaxHealth(player *playerRecord) float32 {
	base := playerAttrBases["max_health"]
	var mods map[string]attrMod
	if s != nil && s.entities != nil && player != nil {
		if e, ok := s.entities.Get(player.entityID.Load()); ok {
			if value, present := e.AttrBase["max_health"]; present {
				base = value
			}
			mods = e.AttrMods["max_health"]
		}
	}
	return float32(sanitizeAttributeValue("max_health", attrValue(base, mods)))
}

// healPlayer follows living healing: a dead body is not revived, and the
// float sum is clamped against its current effective maximum.
func (sess *Session) healPlayer(amount float32) {
	if sess.health > 0 {
		sess.health = max(float32(0), min(sess.health+amount, sess.server.playerMaxHealth(&sess.playerRecord)))
		sess.healthDirty.Store(true)
	}
}

// A lower maximum takes effect during living attribute refresh. Health is
// survival state owned by Java sessions; presence-only avatars have no such
// simulation and are deliberately skipped here.
func (s *Server) playerHealthAttributeTick() {
	for _, player := range s.activePlayersOwned() {
		sess := player.java
		if sess == nil || !sess.gameplayTickReady() {
			continue
		}
		if limit := s.playerMaxHealth(player); sess.health > limit {
			sess.health = limit
			sess.healthDirty.Store(true)
		}
	}
}
