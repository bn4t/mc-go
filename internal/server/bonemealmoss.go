package server

import "mc-go/internal/world"

// bonemealMossPatch uses the official moss_patch_bonemeal configuration:
// depth 1, horizontal radii U[1,2]+1, edge chance .75, vegetation .6.
// BonemealableFeaturePlacerBlock accepts air above and consumes the meal
// even when no neighboring ground can be converted by the feature.
func (s *Server) bonemealMossPatch(d *dimension, pos world.Pos) bool {
	origin := posAbove(pos, 1)
	if !isAirCell(d.w.GetBlock(origin)) {
		return false
	}
	rx, rz := 2+s.rng.intn(2), 2+s.rng.intn(2)
	var ground []world.Pos
	for dx := -rx; dx <= rx; dx++ {
		for dz := -rz; dz <= rz; dz++ {
			xedge, zedge := dx == -rx || dx == rx, dz == -rz || dz == rz
			if xedge && zedge || (xedge || zedge) && float32(s.rng.next()>>40)/(1<<24) >= 0.75 {
				continue
			}
			p := world.Pos{X: origin.X + dx, Y: origin.Y, Z: origin.Z + dz}
			// Surface search is bounded independently in both directions.
			for n := 0; n < 5 && isAirCell(d.w.GetBlock(p)); n++ {
				p.Y--
			}
			for n := 0; n < 5 && !isAirCell(d.w.GetBlock(p)); n++ {
				p.Y++
			}
			soil := posBelow(p, 1)
			name, _ := world.StateOwner(d.w.GetBlock(soil))
			if !isAirCell(d.w.GetBlock(p)) || !rsFaceSturdyUp(d.w.GetBlock(soil)) || !blockTagHas("moss_replaceable", name) {
				continue
			}
			moss, _ := world.BlockStateOf("moss_block", nil)
			s.setSilentBlock(d, soil, moss)
			ground = append(ground, p)
		}
	}
	for _, p := range ground {
		if float32(s.rng.next()>>40)/(1<<24) < float32(0.6) {
			s.bonemealMossVegetation(d, p)
		}
	}
	return true
}

// The moss_vegetation weighted provider has 96 total entries. Tall grass
// is a double plant: SimpleBlockFeature requires empty space for its top.
func (s *Server) bonemealMossVegetation(d *dimension, pos world.Pos) {
	name := "tall_grass"
	switch r := s.rng.intn(96); {
	case r < 4:
		name = "flowering_azalea"
	case r < 11:
		name = "azalea"
	case r < 36:
		name = "moss_carpet"
	case r < 86:
		name = "short_grass"
	}
	if name == "tall_grass" {
		above := posAbove(pos, 1)
		if !isAirCell(d.w.GetBlock(above)) {
			return
		}
		lower, _ := world.BlockStateOf(name, map[string]string{"half": "lower"})
		upper, _ := world.BlockStateOf(name, map[string]string{"half": "upper"})
		s.setSilentBlock(d, pos, lower)
		s.setSilentBlock(d, above, upper)
		return
	}
	b, _ := world.BlockStateOf(name, nil)
	s.setSilentBlock(d, pos, b)
}
