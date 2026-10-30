package server

import (
	"bytes"
	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// blockQueryTag renders saveWithoutMetadata rather than the smaller update
// tag used to refresh a client. Storage coordinates and packing markers are
// excluded only at the outer level; saved item/component data stays intact.
func (s *Server) blockQueryTag(d *dimension, pos world.Pos) []byte {
	saved, ok := s.blockEntitySaveMap(d, pos)
	if !ok {
		return nil
	}
	fields := make(map[string]any, len(saved))
	for key, value := range saved {
		switch key {
		case "id", "x", "y", "z", "keepPacked":
			continue
		}
		fields[key] = value
	}
	var encoded bytes.Buffer
	if err := nbt.WriteUnnamed(&encoded, fields); err != nil {
		return nil
	}
	return encoded.Bytes()
}
