package server

import (
	"bytes"
	"mc-go/internal/nbt"
)

// entityQueryTag renders the current level's entity through the same modeled
// native save view used by /data. F3+I requests saveWithoutId, so only the
// outer entity type identifier is omitted; nested passenger identifiers stay.
func (s *Server) entityQueryTag(sess *Session, id int32) ([]byte, bool) {
	e, ok := s.entities.GetPtr(id)
	if !ok || e.Dim != sess.dimName() {
		return nil, false
	}
	var compound map[string]any
	if player := s.sessionByEntityID(id); player != nil {
		compound = playerSaveMap(player, s.gameTime(), s.entities)
	} else {
		compound = entitySaveMap(e, s.gameTime(), s.entities).m
	}
	delete(compound, "id")
	var encoded bytes.Buffer
	if err := nbt.WriteUnnamed(&encoded, compound); err != nil {
		return nil, false
	}
	return encoded.Bytes(), true
}
