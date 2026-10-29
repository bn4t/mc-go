package server

import "mc-go/internal/world"

// Structure blocks keep their power latch in the block entity. Only an edge
// triggers the selected action; changing the latch alone sends no update.
func (s *Server) structureBlockNeighborChanged(d *dimension, pos world.Pos, powered bool) {
	g := s.structureBlockAt(d, pos)
	if g == nil || nbtTruth(g.fields["powered"]) == powered {
		return
	}
	g.fields["powered"] = boolByte(powered)
	if !powered {
		return
	}
	name, _ := g.fields["name"].(string)
	switch g.fields["mode"] {
	case "SAVE":
		s.structureBlockSave(d, pos, g, false)
	case "LOAD":
		native := s.structureTemplateGet(name)
		if native == nil {
			return
		}
		author, _ := native["author"].(string)
		g.fields["author"] = author
		size := structureVector(native["size"])
		for i, key := range []string{"sizeX", "sizeY", "sizeZ"} {
			g.fields[key] = size[i]
		}
		s.structureTemplatePlace(d, pos, g, native)
	case "CORNER":
		delete(s.structureTemplates, name)
	}
}
