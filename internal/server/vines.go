package server

import (
	"mc-go/internal/world"
)

// Regular-vine (VineBlock) mechanics, measured off the 26.2 jar.
//
// Faces live in the boolean props up/north/south/east/west — a vine
// cell can hug several walls at once. randomTick (gated on the
// spread_vines game rule, then a 1/4 roll) picks a random direction:
//
//   - a horizontal dir the cell doesn't already have spreads around
//     corners, or grows the cell's own face onto a solid side;
//   - up gains an `up` face under a solid ceiling or sprouts a new
//     cell above carrying a randomly-thinned copy of the faces;
//   - anything else (down, or an already-held face) extends into or
//     under the cell below via copyRandomFaces.
//
// canSpread aborts once 5+ vine blocks sit in a 9×3×9 box.
// updateShape prunes every face that lost its support (any write not
// arriving from below) and folds to air when none remain.

// dirOffsets maps a Direction name to its unit offset; index order
// matches vanilla Direction.values() (down, up, north, south, west,
// east) so rng.intn(6) lines up.
var vineDirOffsets = map[string]world.Pos{
	"down":  {X: 0, Y: -1, Z: 0},
	"up":    {X: 0, Y: 1, Z: 0},
	"north": {X: 0, Y: 0, Z: -1},
	"south": {X: 0, Y: 0, Z: 1},
	"west":  {X: -1, Y: 0, Z: 0},
	"east":  {X: 1, Y: 0, Z: 0},
}

var vineDirNames = [6]string{"down", "up", "north", "south", "west", "east"}

// Horizontal prop names in vanilla Plane.HORIZONTAL order
// (south, west, north, east) — iteration order only shapes RNG draw
// counts, which differ across implementations anyway.
var vineHorizontalFaces = [4]string{"south", "west", "north", "east"}

func vineClockwise(dir string) string {
	switch dir {
	case "north":
		return "east"
	case "east":
		return "south"
	case "south":
		return "west"
	case "west":
		return "north"
	}
	return dir
}

func vineCounterClockwise(dir string) string {
	switch dir {
	case "north":
		return "west"
	case "west":
		return "south"
	case "south":
		return "east"
	case "east":
		return "north"
	}
	return dir
}

func vineOpposite(dir string) string {
	switch dir {
	case "north":
		return "south"
	case "south":
		return "north"
	case "west":
		return "east"
	case "east":
		return "west"
	case "up":
		return "down"
	case "down":
		return "up"
	}
	return dir
}

func vineRel(p world.Pos, dir string) world.Pos {
	o := vineDirOffsets[dir]
	return world.Pos{X: p.X + o.X, Y: p.Y + o.Y, Z: p.Z + o.Z}
}

// vineAcceptable is MultifaceBlock.canAttachTo: the cell has a full
// support or collision face toward the vine. mc-go approximates that
// with the sturdy predicate (not non-solid, not a fluid).
func (s *Server) vineAcceptable(d *dimension, p world.Pos) bool {
	b := d.w.GetBlock(p)
	_, _, isFluid := fluidOf(b)
	return !isFluid && !world.NonSolid(b)
}

// vineCanSupportAtFace is VineBlock.canSupportAtFace: DOWN never
// supports; otherwise the side cell is acceptable, or — for the
// horizontal faces — a vine directly above holds the same face.
func (s *Server) vineCanSupportAtFace(d *dimension, pos world.Pos, dir string) bool {
	if dir == "down" {
		return false
	}
	if s.vineAcceptable(d, vineRel(pos, dir)) {
		return true
	}
	if dir == "up" {
		return false
	}
	above := d.w.GetBlock(vineRel(pos, "up"))
	if !isBlockNamed(above, "vine") {
		return false
	}
	v, _ := world.StateProperty(above, dir)
	return v == "true"
}

// vineCanSpread is the 9×3×9 spread cap: under five vine blocks.
func (s *Server) vineCanSpread(d *dimension, pos world.Pos) bool {
	left := 5
	for dy := -1; dy <= 1; dy++ {
		for dx := -4; dx <= 4; dx++ {
			for dz := -4; dz <= 4; dz++ {
				if isBlockNamed(d.w.GetBlock(world.Pos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}), "vine") {
					left--
					if left <= 0 {
						return false
					}
				}
			}
		}
	}
	return true
}

func vineHasHorizontalFace(b world.Block) bool {
	for _, f := range vineHorizontalFaces {
		if v, _ := world.StateProperty(b, f); v == "true" {
			return true
		}
	}
	return false
}

// vineWithFace builds the default vine state carrying a single face.
func vineDefaultWithFace(face string) (world.Block, bool) {
	return world.BlockStateOf("vine", map[string]string{face: "true"})
}

func vineRule(s *Server, d *dimension, pos world.Pos, _ world.Block) {
	if !spreadVinesEnabled(s) || s.rng.intn(4) != 0 {
		return
	}
	b := d.w.GetBlock(pos)
	di := s.rng.intn(6)
	dir := vineDirNames[di]
	horizontal := dir != "up" && dir != "down"
	face, _ := world.StateProperty(b, dir)
	if horizontal && face != "true" {
		if !s.vineCanSpread(d, pos) {
			return
		}
		side := vineRel(pos, dir)
		sb := d.w.GetBlock(side)
		if isAirCell(sb) {
			cw, ccw := vineClockwise(dir), vineCounterClockwise(dir)
			hasCW, _ := world.StateProperty(b, cw)
			hasCCW, _ := world.StateProperty(b, ccw)
			sideCW, sideCCW := vineRel(side, cw), vineRel(side, ccw)
			opp := vineOpposite(dir)
			switch {
			case hasCW == "true" && s.vineAcceptable(d, sideCW):
				if nb, ok := vineDefaultWithFace(cw); ok {
					s.setNaturalBlock(d, side, nb)
				}
			case hasCCW == "true" && s.vineAcceptable(d, sideCCW):
				if nb, ok := vineDefaultWithFace(ccw); ok {
					s.setNaturalBlock(d, side, nb)
				}
			case hasCW == "true" && isAirCell(d.w.GetBlock(sideCW)) && s.vineAcceptable(d, vineRel(pos, cw)):
				if nb, ok := vineDefaultWithFace(opp); ok {
					s.setNaturalBlock(d, sideCW, nb)
				}
			case hasCCW == "true" && isAirCell(d.w.GetBlock(sideCCW)) && s.vineAcceptable(d, vineRel(pos, ccw)):
				if nb, ok := vineDefaultWithFace(opp); ok {
					s.setNaturalBlock(d, sideCCW, nb)
				}
			case s.rng.nextDouble() < 0.05 && s.vineAcceptable(d, vineRel(side, "up")):
				if nb, ok := vineDefaultWithFace("up"); ok {
					s.setNaturalBlock(d, side, nb)
				}
			}
			return
		}
		if s.vineAcceptable(d, side) {
			if nb, ok := world.WithProperty(b, dir, "true"); ok {
				s.setNaturalBlock(d, pos, nb)
			}
		}
		return
	}
	if dir == "up" && pos.Y < d.minY+d.height-1 {
		if s.vineCanSupportAtFace(d, pos, "up") {
			if nb, ok := world.WithProperty(b, "up", "true"); ok {
				s.setNaturalBlock(d, pos, nb)
			}
			return
		}
		above := vineRel(pos, "up")
		if !isAirCell(d.w.GetBlock(above)) {
			return
		}
		if !s.vineCanSpread(d, pos) {
			return
		}
		grown := b
		for _, h := range vineHorizontalFaces {
			if s.rng.nextDouble() < 0.5 || !s.vineAcceptable(d, vineRel(above, h)) {
				if nb, ok := world.WithProperty(grown, h, "false"); ok {
					grown = nb
				}
			}
		}
		if vineHasHorizontalFace(grown) {
			s.setNaturalBlock(d, above, grown)
		}
		return
	}
	if pos.Y <= d.minY {
		return
	}
	below := vineRel(pos, "down")
	bs := d.w.GetBlock(below)
	if !isAirCell(bs) && !isBlockNamed(bs, "vine") {
		return
	}
	target := bs
	if isAirCell(bs) {
		if nb, ok := world.BlockStateOf("vine", nil); ok {
			target = nb
		}
	}
	grown := target
	for _, h := range vineHorizontalFaces {
		if s.rng.nextDouble() >= 0.5 {
			if v, _ := world.StateProperty(b, h); v == "true" {
				if nb, ok := world.WithProperty(grown, h, "true"); ok {
					grown = nb
				}
			}
		}
	}
	if grown != target && vineHasHorizontalFace(grown) {
		s.setNaturalBlock(d, below, grown)
	}
}

// spreadVinesEnabled reads the spread_vines game rule (default true).
func spreadVinesEnabled(s *Server) bool {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	v, ok := s.gameRules["minecraft:spread_vines"]
	return !ok || v != "false"
}

// vinePrune is VineBlock.updateShape: a write arriving from any
// direction but below clears every face that lost its support; a
// vine with no faces left folds to air (return-air family).
func (s *Server) vinePrune(d *dimension, pos, changed world.Pos, b world.Block) {
	if changed.Y < pos.Y {
		return
	}
	nb := b
	for _, f := range append([]string{"up"}, vineHorizontalFaces[:]...) {
		v, _ := world.StateProperty(nb, f)
		if v != "true" {
			continue
		}
		if !s.vineCanSupportAtFace(d, pos, f) {
			if cleared, ok := world.WithProperty(nb, f, "false"); ok {
				nb = cleared
			}
		}
	}
	if nb == b {
		return
	}
	if !vineHasAnyFace(nb) {
		s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
		s.setNaturalBlock(d, pos, world.Air)
		return
	}
	s.setNaturalBlock(d, pos, nb)
}

func vineHasAnyFace(b world.Block) bool {
	for _, f := range []string{"up", "north", "south", "east", "west"} {
		if v, _ := world.StateProperty(b, f); v == "true" {
			return true
		}
	}
	return false
}
