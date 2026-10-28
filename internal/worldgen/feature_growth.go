package worldgen

import "strings"

// Multiface growth: glow lichen and sculk veins attached to the faces
// around an air or water block, then spread once toward a random side.

func init() {
	featureKinds["minecraft:multiface_growth"] = decodeMultifaceGrowth
}

var faceProps = [6]string{"down", "up", "north", "south", "west", "east"}

// horizontalDirs is the game's horizontal plane order.
var horizontalDirs = []int{dirNorth, dirEast, dirSouth, dirWest}

// shuffleDirs is Util.shuffle: from the end, each slot swaps with a
// random earlier-or-same slot.
func shuffleDirs(rand Random, dirs []int) []int {
	for i := int32(len(dirs)); i > 1; i-- {
		j := rand.NextIntn(i)
		dirs[i-1], dirs[j] = dirs[j], dirs[i-1]
	}
	return dirs
}

func dirAxis(d int) int { return d >> 1 }

// multiface is a multiface block's placement and spreading rules.
type multiface struct {
	block string
	sculk bool // sculk vein spreading rules
}

func hasFace(s StateID, d int) bool { return s.prop(faceProps[d]) == "true" }

func isAirOrWater(s StateID) bool { return s.isAir() || s.is("water") }

// canAttachTo is whether the neighbour at np (in direction d) offers a
// full face back: its support or collision shape covers it.
func canAttachTo(r *decoRegion, d int, np Point) bool {
	n := r.get(np)
	return n.sturdy(d^1) || n.collisionFaceFull(d^1)
}

func (m multiface) validFor(r *decoRegion, s StateID, p Point, d int) bool {
	if s.is(m.block) && hasFace(s, d) {
		return false
	}
	return canAttachTo(r, d, p.offset(d))
}

// stateFor is getStateForPlacement: the block at p with face d added,
// nil-equivalent (false) when the face cannot attach.
func (m multiface) stateFor(r *decoRegion, s StateID, p Point, d int) (StateID, bool) {
	if !m.validFor(r, s, p, d) {
		return 0, false
	}
	base := defaultState(m.block)
	switch {
	case s.is(m.block):
		base = s
	case s.isWaterFluid() && s.isSourceFluid():
		base = base.with("waterlogged", "true")
	}
	return base.with(faceProps[d], "true"), true
}

func (m multiface) replaceable(r *decoRegion, src, p Point, face int, s StateID) bool {
	if m.sculk {
		n := r.get(p.offset(face))
		if n.is("sculk") || n.is("sculk_catalyst") || n.is("moving_piston") {
			return false
		}
		if manhattan(src, p) == 2 {
			q := src.offset(face ^ 1)
			if r.get(q).sturdy(face) {
				return false
			}
		}
		if s.hasFluid() && !(s.isWaterFluid() && s.isSourceFluid()) {
			return false
		}
		if s.inTag("fire") {
			return false
		}
		if s.canBeReplaced() {
			return true
		}
	}
	return s.isAir() || s.is(m.block) || (s.is("water") && s.isSourceFluid())
}

func manhattan(a, b Point) int32 {
	abs := func(v int32) int32 {
		if v < 0 {
			return -v
		}
		return v
	}
	return abs(a.X-b.X) + abs(a.Y-b.Y) + abs(a.Z-b.Z)
}

// spreadOnce is spreadFromFaceTowardRandomDirection: over the six
// directions shuffled, the first that spreads the face from p — into
// p itself, the neighbour on the same plane, or around the edge — wins.
func (m multiface) spreadOnce(r *decoRegion, s StateID, p Point, face int, rand Random) {
	for _, d := range shuffleDirs(rand, []int{dirDown, dirUp, dirNorth, dirSouth, dirWest, dirEast}) {
		if dirAxis(d) == dirAxis(face) {
			continue
		}
		otherSource := m.sculk && !s.is(m.block)
		if !otherSource && (!hasFace(s, face) || hasFace(s, d)) {
			continue
		}
		targets := [3]struct {
			p    Point
			face int
		}{{p, d}, {p.offset(d), face}, {p.offset(d).offset(face), d ^ 1}}
		for _, t := range targets {
			cur := r.get(t.p)
			if !m.replaceable(r, p, t.p, t.face, cur) || !m.validFor(r, cur, t.p, t.face) {
				continue
			}
			// The first spread position the rules accept is the one
			// tried; placing there decides this direction.
			if ns, ok := m.stateFor(r, cur, t.p, t.face); ok && r.set(t.p, ns) {
				return
			}
			break
		}
	}
}

func decodeMultifaceGrowth(cfg any) func(*featureCtx) bool {
	m := multiface{block: strings.TrimPrefix(fstr(ffield(cfg, "block")), "minecraft:")}
	m.sculk = m.block == "sculk_vein"
	search := int32(10)
	if fhas(cfg, "search_range") {
		search = fint(ffield(cfg, "search_range"), 10)
	}
	chance := float32(0.5)
	if fhas(cfg, "chance_of_spreading") {
		chance = ffloat(ffield(cfg, "chance_of_spreading"), 0.5)
	}
	var valid []int
	if fbool(ffield(cfg, "can_place_on_ceiling"), false) {
		valid = append(valid, dirUp)
	}
	if fbool(ffield(cfg, "can_place_on_floor"), false) {
		valid = append(valid, dirDown)
	}
	if fbool(ffield(cfg, "can_place_on_wall"), false) {
		valid = append(valid, horizontalDirs...)
	}
	on := blockSet(ffield(cfg, "can_be_placed_on"))
	grow := func(fc *featureCtx, p Point, s StateID, dirs []int) bool {
		r := fc.r
		for _, d := range dirs {
			if !on[r.get(p.offset(d)).block()] {
				continue
			}
			ns, ok := m.stateFor(r, s, p, d)
			if !ok {
				return false
			}
			r.set(p, ns)
			if fc.rand.NextFloat() < chance {
				m.spreadOnce(r, ns, p, d, fc.rand)
			}
			return true
		}
		return false
	}
	return func(fc *featureCtx) bool {
		r, o := fc.r, fc.origin
		if !isAirOrWater(r.get(o)) {
			return false
		}
		dirs := shuffleDirs(fc.rand, append([]int(nil), valid...))
		if grow(fc, o, r.get(o), dirs) {
			return true
		}
		for _, d := range dirs {
			var except []int
			for _, v := range valid {
				if v != d^1 {
					except = append(except, v)
				}
			}
			shuffleDirs(fc.rand, except)
			// The search re-tests the block next to the origin each
			// step rather than walking outward.
			for i := int32(0); i < search; i++ {
				p := o.offset(d)
				s := r.get(p)
				if !isAirOrWater(s) && !s.is(m.block) {
					break
				}
				if grow(fc, p, s, except) {
					return true
				}
			}
		}
		return false
	}
}
