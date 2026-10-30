package server

// mobInvulnerableTo handles the saved Entity flag for the ordinary server
// damage path. Creative bypass belongs to the responsible damage source,
// independently of which player would receive kill credit.
func (s *Server) mobInvulnerableTo(e *Entity, kind string, info *damageInfo) bool {
	if !e.Living || !e.Invulnerable || damageTypeBypassesInvulnerability(kind) {
		return false
	}
	var cause int32
	if info != nil {
		cause = info.cause
		if cause == 0 {
			cause = info.direct
		}
	}
	player := s.sessionByEntityID(cause)
	return player == nil || player.mode() != GameModeCreative
}
