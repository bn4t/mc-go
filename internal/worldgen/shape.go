package worldgen

// Neighbour shape updates: BlockState.updateShape for the blocks the
// features stage touches, dispatched on the class that declares it, and
// the edge pass that runs it across the boundary of a placed shape.

var stAirState = InternState("minecraft:air")

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// updateShape is s at p reacting to its neighbour in direction dir (at
// np, now ns). Scheduled ticks are not part of the features stage's
// output, so the classes that only schedule return s unchanged.
func updateShape(r *decoRegion, s StateID, p Point, dir int, np Point, ns StateID) StateID {
	l := s.layout()
	if l == nil {
		return s
	}
	switch l.shape {
	case "BlockBehaviour", "-", "LeavesBlock", "LiquidBlock", "FallingBlock", "SugarCaneBlock", "CactusBlock",
		"WaterloggedTransparentBlock", "MangroveRootsBlock", "BeehiveBlock":
		return s
	case "SnowyBlock":
		if dir == dirUp {
			return s.with("snowy", boolStr(ns.inTag("snow")))
		}
		return s
	case "VegetationBlock", "CarpetBlock", "SnowLayerBlock", "MangrovePropaguleBlock":
		// A propagule's own rule (from above) falls through to the
		// vegetation one, which tests survival from every side.
		if !canSurvive(r, s, p) {
			return stAirState
		}
		return s
	case "DoublePlantBlock":
		half := s.prop("half")
		if dir == dirUp || dir == dirDown {
			if (half == "lower") == (dir == dirUp) && (ns.block() != l.name || ns.prop("half") == half) {
				return stAirState
			}
		}
		if half == "lower" && dir == dirDown && !canSurvive(r, s, p) {
			return stAirState
		}
		if !canSurvive(r, s, p) {
			return stAirState
		}
		return s
	case "VineBlock":
		if dir == dirDown {
			return s
		}
		u := vineUpdatedState(r, s, p)
		if !vineHasFaces(u) {
			return stAirState
		}
		return u
	case "CocoaBlock":
		if dir == directionNames[s.prop("facing")] && !canSurvive(r, s, p) {
			return stAirState
		}
		return s
	case "BambooStalkBlock":
		// A survival failure only schedules a tick; a taller-aged stalk
		// above advances this one's age.
		if dir == dirUp && ns.is("bamboo") && ns.prop("age") > s.prop("age") {
			return s.with("age", "1")
		}
		return s
	case "HugeMushroomBlock":
		if ns.block() == l.name {
			return s.with(faceProps[dir], "false")
		}
		return s
	}
	unsupported("updateShape of %s (%s)", l.name, l.shape)
	return s
}

var vineFaces = []struct {
	dir  int
	prop string
}{{dirUp, "up"}, {dirNorth, "north"}, {dirEast, "east"}, {dirSouth, "south"}, {dirWest, "west"}}

func vineHasFaces(s StateID) bool {
	for _, f := range vineFaces {
		if s.prop(f.prop) == "true" {
			return true
		}
	}
	return false
}

// vineAcceptable is VineBlock.isAcceptableNeighbour: the block at np
// offers a full face back toward the vine (in direction d from it).
func vineAcceptable(r *decoRegion, np Point, d int) bool { return canAttachTo(r, d, np) }

// vineCanSupportAtFace is whether a vine at p can keep its face d: the
// neighbour there is acceptable, or (sideways) the vine above has it.
func vineCanSupportAtFace(r *decoRegion, p Point, d int) bool {
	if d == dirDown {
		return false
	}
	if vineAcceptable(r, p.offset(d), d) {
		return true
	}
	if d == dirUp {
		return false
	}
	above := r.get(p.offset(dirUp))
	return above.is("vine") && above.prop(faceProps[d]) == "true"
}

// vineUpdatedState drops the faces of a vine at p that lost support.
func vineUpdatedState(r *decoRegion, s StateID, p Point) StateID {
	if s.prop("up") == "true" {
		s = s.with("up", boolStr(vineAcceptable(r, p.offset(dirUp), dirDown)))
	}
	var above StateID
	haveAbove := false
	for _, d := range horizontalDirs {
		prop := faceProps[d]
		if s.prop(prop) != "true" {
			continue
		}
		ok := vineCanSupportAtFace(r, p, d)
		if !ok {
			if !haveAbove {
				above, haveAbove = r.get(p.offset(dirUp)), true
			}
			ok = above.is("vine") && above.prop(prop) == "true"
		}
		s = s.with(prop, boolStr(ok))
	}
	return s
}

// voxelBox is a bit-set voxel shape over a box of positions.
type voxelBox struct {
	min        Point
	sx, sy, sz int32
	bits       []bool
}

func newVoxelBox(min, max Point) *voxelBox {
	v := &voxelBox{min: min, sx: max.X - min.X + 1, sy: max.Y - min.Y + 1, sz: max.Z - min.Z + 1}
	v.bits = make([]bool, v.sx*v.sy*v.sz)
	return v
}

func (v *voxelBox) inside(p Point) bool {
	x, y, z := p.X-v.min.X, p.Y-v.min.Y, p.Z-v.min.Z
	return x >= 0 && y >= 0 && z >= 0 && x < v.sx && y < v.sy && z < v.sz
}

func (v *voxelBox) full(x, y, z int32) bool {
	if x < 0 || y < 0 || z < 0 || x >= v.sx || y >= v.sy || z >= v.sz {
		return false
	}
	return v.bits[(x*v.sy+y)*v.sz+z]
}

func (v *voxelBox) fill(p Point) {
	x, y, z := p.X-v.min.X, p.Y-v.min.Y, p.Z-v.min.Z
	v.bits[(x*v.sy+y)*v.sz+z] = true
}

func (v *voxelBox) isFull(p Point) bool { return v.full(p.X-v.min.X, p.Y-v.min.Y, p.Z-v.min.Z) }

// forAllFaces visits each face between a filled cell and an empty one,
// in the game's order: north/south faces (x, then y, then z), then
// down/up (z, x, y), then west/east (y, z, x).
func (v *voxelBox) forAllFaces(f func(dir int, x, y, z int32)) {
	for x := int32(0); x < v.sx; x++ {
		for y := int32(0); y < v.sy; y++ {
			prev := false
			for z := int32(0); z <= v.sz; z++ {
				cur := z != v.sz && v.full(x, y, z)
				if !prev && cur {
					f(dirNorth, x, y, z)
				}
				if prev && !cur {
					f(dirSouth, x, y, z-1)
				}
				prev = cur
			}
		}
	}
	for z := int32(0); z < v.sz; z++ {
		for x := int32(0); x < v.sx; x++ {
			prev := false
			for y := int32(0); y <= v.sy; y++ {
				cur := y != v.sy && v.full(x, y, z)
				if !prev && cur {
					f(dirDown, x, y, z)
				}
				if prev && !cur {
					f(dirUp, x, y-1, z)
				}
				prev = cur
			}
		}
	}
	for y := int32(0); y < v.sy; y++ {
		for z := int32(0); z < v.sz; z++ {
			prev := false
			for x := int32(0); x <= v.sx; x++ {
				cur := x != v.sx && v.full(x, y, z)
				if !prev && cur {
					f(dirWest, x, y, z)
				}
				if prev && !cur {
					f(dirEast, x-1, y, z)
				}
				prev = cur
			}
		}
	}
}

// updateShapeAtEdge is StructureTemplate.updateShapeAtEdge: across every
// face of the shape, the cell inside updates against the one outside,
// then the outside one against the inside's new state.
func updateShapeAtEdge(r *decoRegion, v *voxelBox) {
	v.forAllFaces(func(dir int, x, y, z int32) {
		p := Point{v.min.X + x, v.min.Y + y, v.min.Z + z}
		np := p.offset(dir)
		s, ns := r.get(p), r.get(np)
		u := updateShape(r, s, p, dir, np, ns)
		if u != s {
			r.set(p, u)
		}
		nu := updateShape(r, ns, np, dir^1, p, u)
		if nu != ns {
			r.set(np, nu)
		}
	})
}
