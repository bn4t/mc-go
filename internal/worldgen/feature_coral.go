package worldgen

import "mc-go/internal/data"

// Coral reef pieces, measured from CoralFeature and its tree, claw and
// mushroom shapes (26.2). Each picks one coral block for the whole
// piece; every coral block placed may carry a coral or a sea pickle on
// top and wall fans on its open sides.

func init() {
	featureKinds["minecraft:coral_tree"] = func(any) func(*featureCtx) bool { return coralPlacer(coralTree) }
	featureKinds["minecraft:coral_claw"] = func(any) func(*featureCtx) bool { return coralPlacer(coralClaw) }
	featureKinds["minecraft:coral_mushroom"] = func(any) func(*featureCtx) bool { return coralPlacer(coralMushroom) }
	featureKinds["minecraft:sea_pickle"] = decodeSeaPickle
}

// decodeSeaPickle is SeaPickleFeature: count tries, each on the ocean
// floor within a triangular spread of 7, of a pickle cluster of one to
// four where it is water and the pickle survives.
func decodeSeaPickle(cfg any) func(*featureCtx) bool {
	count := decodeIntProvider(ffield(cfg, "count"))
	return func(fc *featureCtx) bool {
		r, random, o := fc.r, fc.rand, fc.origin
		placed := false
		n := count.sample(random)
		for range n {
			dx := random.NextIntn(8) - random.NextIntn(8)
			dz := random.NextIntn(8) - random.NextIntn(8)
			x, z := o.X+dx, o.Z+dz
			p := Point{x, r.heightAt(hmOceanFloor, x, z), z}
			s := defaultState("sea_pickle").with("pickles", itoa(random.NextIntn(4)+1))
			if r.get(p).is("water") && canSurvive(r, s, p) {
				r.set(p, s)
				placed = true
			}
		}
		return placed
	}
}

// randomTagBlock is Registry.getRandomElementOf: a uniform pick from the
// tag in the game's element order.
func randomTagBlock(tag string, r Random) (string, bool) {
	l := data.BlockTagsOrdered[tag]
	if len(l) == 0 {
		return "", false
	}
	return l[r.NextIntn(int32(len(l)))], true
}

func counterClockwise(d int) int { return clockwise[clockwise[clockwise[d]]] }

type coralShape func(fc *featureCtx, block StateID) bool

func coralPlacer(shape coralShape) func(*featureCtx) bool {
	return func(fc *featureCtx) bool {
		b, ok := randomTagBlock("coral_blocks", fc.rand)
		if !ok {
			return false
		}
		return shape(fc, defaultState(b))
	}
}

// placeCoralBlock sets one coral block where water (or a coral) sits
// under water, then decorates it.
func placeCoralBlock(fc *featureCtx, p Point, block StateID) bool {
	r, random := fc.r, fc.rand
	above := p.offset(dirUp)
	if s := r.get(p); !(s.is("water") || s.inTag("corals")) || !r.get(above).is("water") {
		return false
	}
	r.set(p, block)
	if random.NextFloat() < 0.25 {
		if c, ok := randomTagBlock("corals", random); ok {
			r.set(above, defaultState(c))
		}
	} else if random.NextFloat() < 0.05 {
		r.set(above, defaultState("sea_pickle").with("pickles", itoa(random.NextIntn(4)+1)))
	}
	for _, d := range horizontalDirs {
		if random.NextFloat() >= 0.2 {
			continue
		}
		q := p.offset(d)
		if !r.get(q).is("water") {
			continue
		}
		if w, ok := randomTagBlock("wall_corals", random); ok {
			s := defaultState(w)
			if s.hasProp("facing") {
				s = s.with("facing", faceProps[d])
			}
			r.set(q, s)
		}
	}
	return true
}

func coralTree(fc *featureCtx, block StateID) bool {
	random := fc.rand
	p := fc.origin
	trunk := random.NextIntn(3) + 1
	for range trunk {
		if !placeCoralBlock(fc, p, block) {
			return true
		}
		p = p.offset(dirUp)
	}
	top := p
	branches := random.NextIntn(3) + 2
	dirs := shuffleDirs(random, append([]int(nil), horizontalDirs...))
	for _, d := range dirs[:branches] {
		p = top.offset(d)
		length := random.NextIntn(5) + 2
		run := 0
		for i := int32(0); i < length; i++ {
			if !placeCoralBlock(fc, p, block) {
				break
			}
			run++
			p = p.offset(dirUp)
			if i == 0 || run >= 2 && random.NextFloat() < 0.25 {
				p = p.offset(d)
				run = 0
			}
		}
	}
	return true
}

func coralClaw(fc *featureCtx, block StateID) bool {
	random := fc.rand
	o := fc.origin
	if !placeCoralBlock(fc, o, block) {
		return false
	}
	dir := horizontalDirs[random.NextIntn(4)]
	arms := random.NextIntn(2) + 2
	dirs := shuffleDirs(random, []int{dir, clockwise[dir], counterClockwise(dir)})
	for _, d := range dirs[:arms] {
		p := o
		first := random.NextIntn(2) + 1
		p = p.offset(d)
		var step int
		var second int32
		if d == dir {
			step = dir
			second = random.NextIntn(3) + 2
		} else {
			p = p.offset(dirUp)
			step = [2]int{d, dirUp}[random.NextIntn(2)]
			second = random.NextIntn(3) + 3
		}
		for range first {
			if !placeCoralBlock(fc, p, block) {
				break
			}
			p = p.offset(step)
		}
		p = p.offset(step ^ 1).offset(dirUp)
		for range second {
			p = p.offset(dir)
			if !placeCoralBlock(fc, p, block) {
				break
			}
			if random.NextFloat() < 0.25 {
				p = p.offset(dirUp)
			}
		}
	}
	return true
}

func coralMushroom(fc *featureCtx, block StateID) bool {
	random := fc.rand
	o := fc.origin
	ys := random.NextIntn(3) + 3
	xs := random.NextIntn(3) + 3
	zs := random.NextIntn(3) + 3
	sink := random.NextIntn(3) + 1
	for x := int32(0); x <= xs; x++ {
		for y := int32(0); y <= ys; y++ {
			for z := int32(0); z <= zs; z++ {
				xEdge, yEdge, zEdge := x == 0 || x == xs, y == 0 || y == ys, z == 0 || z == zs
				// The cap is a hollow box without its edges, a tenth of
				// its faces missing.
				if xEdge && yEdge || zEdge && yEdge || xEdge && zEdge || !xEdge && !yEdge && !zEdge {
					continue
				}
				if random.NextFloat() < 0.1 {
					continue
				}
				placeCoralBlock(fc, Point{o.X + x, o.Y + y - sink, o.Z + z}, block)
			}
		}
	}
	return true
}
