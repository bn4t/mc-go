package worldgen

// Plant features placed block by block: a vine on the first face it can
// hold, seagrass and kelp on the sea floor, and columns of layered
// blocks grown in one direction.

func init() {
	featureKinds["minecraft:vines"] = func(any) func(*featureCtx) bool { return placeVines }
	featureKinds["minecraft:seagrass"] = decodeSeagrass
	featureKinds["minecraft:kelp"] = func(any) func(*featureCtx) bool { return placeKelp }
	featureKinds["minecraft:block_column"] = decodeBlockColumn
	featureKinds["minecraft:bamboo"] = decodeBamboo
}

// decodeBamboo is BambooFeature: a stalk of 5 to 16 on an air origin
// where bamboo survives, topped by large and small leaves once at least
// three tall; with the configured chance it first turns the surface
// disk of radius 1 to 4 below it to podzol.
func decodeBamboo(cfg any) func(*featureCtx) bool {
	chance := ffloat(ffield(cfg, "probability"), 0)
	trunk := defaultState("bamboo").with("age", "1").with("leaves", "none").with("stage", "0")
	final := trunk.with("leaves", "large").with("stage", "1")
	topLarge, topSmall := trunk.with("leaves", "large"), trunk.with("leaves", "small")
	podzol := defaultState("podzol")
	return func(fc *featureCtx) bool {
		r, random, o := fc.r, fc.rand, fc.origin
		if !r.get(o).isAir() {
			return false
		}
		if !canSurvive(r, defaultState("bamboo"), o) {
			return true
		}
		height := random.NextIntn(12) + 5
		if random.NextFloat() < chance {
			rad := random.NextIntn(4) + 1
			for x := o.X - rad; x <= o.X+rad; x++ {
				for z := o.Z - rad; z <= o.Z+rad; z++ {
					dx, dz := x-o.X, z-o.Z
					if dx*dx+dz*dz > rad*rad {
						continue
					}
					q := Point{x, r.heightAt(hmWorldSurface, x, z) - 1, z}
					if r.get(q).inTag("beneath_bamboo_podzol_replaceable") {
						r.set(q, podzol)
					}
				}
			}
		}
		p := o
		for i := int32(0); i < height && r.get(p).isAir(); i++ {
			r.set(p, trunk)
			p = p.offset(dirUp)
		}
		if p.Y-o.Y >= 3 {
			r.set(p, final)
			p = p.offset(dirDown)
			r.set(p, topLarge)
			p = p.offset(dirDown)
			r.set(p, topSmall)
		}
		return true
	}
}

// placeVines puts a vine in an air origin on its first side, in the
// game's direction order and never below, that an acceptable block
// backs.
func placeVines(fc *featureCtx) bool {
	r, o := fc.r, fc.origin
	if !r.get(o).isAir() {
		return false
	}
	for d := 0; d < 6; d++ {
		if d == dirDown || !vineAcceptable(r, o.offset(d), d) {
			continue
		}
		r.set(o, defaultState("vine").with(faceProps[d], "true"))
		return true
	}
	return false
}

// decodeSeagrass picks a spot up to seven blocks off the origin on the
// ocean floor; in water there it places seagrass, or tall seagrass at
// the configured chance when the block above is water too.
func decodeSeagrass(cfg any) func(*featureCtx) bool {
	chance := float64(ffloat(ffield(cfg, "probability"), 0))
	short, tall := defaultState("seagrass"), defaultState("tall_seagrass")
	return func(fc *featureCtx) bool {
		r, o, rand := fc.r, fc.origin, fc.rand
		dx := rand.NextIntn(8) - rand.NextIntn(8)
		dz := rand.NextIntn(8) - rand.NextIntn(8)
		x, z := o.X+dx, o.Z+dz
		p := Point{x, r.heightAt(hmOceanFloor, x, z), z}
		if !r.get(p).is("water") {
			return false
		}
		isTall := rand.NextDouble() < chance
		s := short
		if isTall {
			s = tall
		}
		if !canSurvive(r, s, p) {
			return false
		}
		if !isTall {
			r.set(p, s)
			return true
		}
		up := p.offset(dirUp)
		if r.get(up).is("water") {
			r.set(p, s)
			r.set(up, s.with("half", "upper"))
		}
		return true
	}
}

// placeKelp grows a kelp column from the ocean floor, one to ten blocks
// of plant topped by a head of age 20–23, while water continues above;
// a column cut short gets its head on the last plant block instead. A
// first block that cannot grow moves the start up one.
func placeKelp(fc *featureCtx) bool {
	r, o, rand := fc.r, fc.origin, fc.rand
	p := Point{o.X, r.heightAt(hmOceanFloor, o.X, o.Z), o.Z}
	if !r.get(p).is("water") {
		return false
	}
	head, plant := defaultState("kelp"), defaultState("kelp_plant")
	headAged := func() StateID { return head.with("age", itoa(rand.NextIntn(4)+20)) }
	height := 1 + rand.NextIntn(10)
	placed := false
	for i := int32(0); i <= height; i, p = i+1, p.offset(dirUp) {
		if r.get(p).is("water") && r.get(p.offset(dirUp)).is("water") && canSurvive(r, plant, p) {
			if i == height {
				r.set(p, headAged())
				placed = true
			} else {
				r.set(p, plant)
			}
			continue
		}
		if i > 0 {
			below := p.offset(dirDown)
			if canSurvive(r, head, below) && !r.get(below.offset(dirDown)).is("kelp") {
				r.set(below, headAged())
				placed = true
			}
			break
		}
	}
	return placed
}

type columnLayer struct {
	height intProvider
	state  stateProvider
}

// decodeBlockColumn samples every layer's height, shortens the column
// where the allowed-placement test first fails (from the tip or the
// base end), and lays the layers from the origin in the column's
// direction. The test runs one block ahead of each placed block.
func decodeBlockColumn(cfg any) func(*featureCtx) bool {
	var layers []columnLayer
	for _, l := range flist(ffield(cfg, "layers")) {
		layers = append(layers, columnLayer{decodeIntProvider(ffield(l, "height")), decodeStateProvider(ffield(l, "provider"))})
	}
	dir := directionNames[fstr(ffield(cfg, "direction"))]
	allowed := decodeBlockPredicate(ffield(cfg, "allowed_placement"))
	tip := fbool(ffield(cfg, "prioritize_tip"), false)
	return func(fc *featureCtx) bool {
		r, rand := fc.r, fc.rand
		heights := make([]int32, len(layers))
		total := int32(0)
		for i, l := range layers {
			heights[i] = l.height.sample(rand)
			total += heights[i]
		}
		if total == 0 {
			return false
		}
		check := fc.origin.offset(dir)
		for i := int32(0); i < total; i++ {
			if !allowed(r, check) {
				truncateColumn(heights, total-i, tip)
				break
			}
			check = check.offset(dir)
		}
		p := fc.origin
		for i, l := range layers {
			for k := int32(0); k < heights[i]; k++ {
				r.set(p, l.state.get(r, rand, p))
				p = p.offset(dir)
			}
		}
		return true
	}
}

// truncateColumn takes excess blocks off the layers, from the first
// layer onward when the tip has priority, otherwise from the last back.
func truncateColumn(heights []int32, excess int32, tip bool) {
	for j := range heights {
		if excess <= 0 {
			return
		}
		i := j
		if !tip {
			i = len(heights) - 1 - j
		}
		take := min32(heights[i], excess)
		excess -= take
		heights[i] -= take
	}
}
