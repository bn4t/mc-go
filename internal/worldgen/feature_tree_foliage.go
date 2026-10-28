package worldgen

import "math"

// Foliage placers beyond the blob: conifers, bushes, acacia and dark
// oak canopies, fancy spheres, jungle and mega pine crowns, random
// spreads and cherry canopies with hanging leaves.

func conicSkip(_ Random, dx, _, dz, rng int32, _ bool) bool { return dx == rng && dz == rng && rng > 0 }

type spruceFoliage struct {
	foliageBase
	trunkHeight intProvider
}

func (s spruceFoliage) foliageHeight(rand Random, height int32, _ *treeConfig) int32 {
	return max32(4, height-s.trunkHeight.sample(rand))
}

func (s spruceFoliage) createFoliage(t *tree, _ int32, a foliageAttachment, foliageHeight, radius int32) {
	offset := s.offset.sample(t.rand)
	cur := t.rand.NextIntn(2)
	top, next := int32(1), int32(0)
	for y := offset; y >= -foliageHeight; y-- {
		t.placeLeavesRow(absSkip(conicSkip), a.pos, cur, y, a.doubleTrunk)
		if cur >= top {
			cur, next = next, 1
			top = min32(top+1, radius+a.radiusOffset)
		} else {
			cur++
		}
	}
}

type pineFoliage struct {
	foliageBase
	height intProvider
}

func (p pineFoliage) foliageHeight(rand Random, _ int32, _ *treeConfig) int32 {
	return p.height.sample(rand)
}

func (p pineFoliage) foliageRadius(rand Random, trunkHeight int32) int32 {
	return p.radius.sample(rand) + rand.NextIntn(max32(trunkHeight+1, 1))
}

func (p pineFoliage) createFoliage(t *tree, _ int32, a foliageAttachment, foliageHeight, radius int32) {
	offset := p.offset.sample(t.rand)
	cur := int32(0)
	for y := offset; y >= offset-foliageHeight; y-- {
		t.placeLeavesRow(absSkip(conicSkip), a.pos, cur, y, a.doubleTrunk)
		if cur >= 1 && y == offset-foliageHeight+1 {
			cur--
		} else if cur < radius+a.radiusOffset {
			cur++
		}
	}
}

type bushFoliage struct{ blobFoliage }

func bushSkip(rand Random, dx, _, dz, rng int32, _ bool) bool {
	return dx == rng && dz == rng && rand.NextIntn(2) == 0
}

func (b bushFoliage) createFoliage(t *tree, _ int32, a foliageAttachment, foliageHeight, radius int32) {
	offset := b.offset.sample(t.rand)
	for y := offset; y >= offset-foliageHeight; y-- {
		t.placeLeavesRow(absSkip(bushSkip), a.pos, radius+a.radiusOffset-1-y, y, a.doubleTrunk)
	}
}

type acaciaFoliage struct{ foliageBase }

func (acaciaFoliage) foliageHeight(Random, int32, *treeConfig) int32 { return 0 }

func acaciaSkip(_ Random, dx, y, dz, rng int32, _ bool) bool {
	if y == 0 {
		return (dx > 1 || dz > 1) && dx != 0 && dz != 0
	}
	return dx == rng && dz == rng && rng > 0
}

func (f acaciaFoliage) createFoliage(t *tree, _ int32, a foliageAttachment, foliageHeight, radius int32) {
	offset := f.offset.sample(t.rand)
	p := Point{a.pos.X, a.pos.Y + offset, a.pos.Z}
	skip := absSkip(acaciaSkip)
	t.placeLeavesRow(skip, p, radius+a.radiusOffset, -1-foliageHeight, a.doubleTrunk)
	t.placeLeavesRow(skip, p, radius-1, -foliageHeight, a.doubleTrunk)
	t.placeLeavesRow(skip, p, radius+a.radiusOffset-1, 0, a.doubleTrunk)
}

type fancyFoliage struct{ blobFoliage }

func fancySkip(_ Random, dx, _, dz, rng int32, _ bool) bool {
	x, z := float32(dx)+0.5, float32(dz)+0.5
	return x*x+z*z > float32(rng*rng)
}

func (f fancyFoliage) createFoliage(t *tree, _ int32, a foliageAttachment, foliageHeight, radius int32) {
	offset := f.offset.sample(t.rand)
	for y := offset; y >= offset-foliageHeight; y-- {
		r := radius + 1
		if y == offset || y == offset-foliageHeight {
			r = radius
		}
		t.placeLeavesRow(absSkip(fancySkip), a.pos, r, y, a.doubleTrunk)
	}
}

func megaSkip(_ Random, dx, _, dz, rng int32, _ bool) bool {
	if dx+dz >= 7 {
		return true
	}
	return dx*dx+dz*dz > rng*rng
}

type megaJungleFoliage struct {
	foliageBase
	height int32
}

func (m megaJungleFoliage) foliageHeight(Random, int32, *treeConfig) int32 { return m.height }

func (m megaJungleFoliage) createFoliage(t *tree, _ int32, a foliageAttachment, foliageHeight, radius int32) {
	offset := m.offset.sample(t.rand)
	h := foliageHeight
	if !a.doubleTrunk {
		h = 1 + t.rand.NextIntn(2)
	}
	for y := offset; y >= offset-h; y-- {
		t.placeLeavesRow(absSkip(megaSkip), a.pos, radius+a.radiusOffset+1-y, y, a.doubleTrunk)
	}
}

type megaPineFoliage struct {
	foliageBase
	crownHeight intProvider
}

func (m megaPineFoliage) foliageHeight(rand Random, _ int32, _ *treeConfig) int32 {
	return m.crownHeight.sample(rand)
}

func (m megaPineFoliage) createFoliage(t *tree, _ int32, a foliageAttachment, foliageHeight, radius int32) {
	offset := m.offset.sample(t.rand)
	p := a.pos
	prev := int32(0)
	for y := p.Y - foliageHeight + offset; y <= p.Y+offset; y++ {
		k := p.Y - y
		r := radius + a.radiusOffset + int32(math.Floor(float64(float32(k)/float32(foliageHeight)*3.5)))
		rr := r
		if k > 0 && r == prev && y&1 == 0 {
			rr = r + 1
		}
		t.placeLeavesRow(absSkip(megaSkip), Point{p.X, y, p.Z}, rr, 0, a.doubleTrunk)
		prev = r
	}
}

type darkOakFoliage struct{ foliageBase }

func (darkOakFoliage) foliageHeight(Random, int32, *treeConfig) int32 { return 4 }

func darkOakAbsSkip(_ Random, dx, y, dz, rng int32, large bool) bool {
	if y == -1 && !large {
		return dx == rng && dz == rng
	}
	if y == 1 {
		return dx+dz > rng*2-2
	}
	return false
}

type darkOakSkip struct{}

func (darkOakSkip) skipSigned(rand Random, dx, y, dz, rng int32, large bool) bool {
	if y == 0 && large && (dx == -rng || dx >= rng) && (dz == -rng || dz >= rng) {
		return true
	}
	return absSkip(darkOakAbsSkip).skipSigned(rand, dx, y, dz, rng, large)
}

func (d darkOakFoliage) createFoliage(t *tree, _ int32, a foliageAttachment, _, radius int32) {
	offset := d.offset.sample(t.rand)
	p := Point{a.pos.X, a.pos.Y + offset, a.pos.Z}
	large := a.doubleTrunk
	skip := darkOakSkip{}
	if large {
		t.placeLeavesRow(skip, p, radius+2, -1, large)
		t.placeLeavesRow(skip, p, radius+3, 0, large)
		t.placeLeavesRow(skip, p, radius+2, 1, large)
		if t.rand.NextBool() {
			t.placeLeavesRow(skip, p, radius, 2, large)
		}
		return
	}
	t.placeLeavesRow(skip, p, radius+2, -1, large)
	t.placeLeavesRow(skip, p, radius+1, 0, large)
}

type randomSpreadFoliage struct {
	foliageBase
	height   intProvider
	attempts int32
}

func (r randomSpreadFoliage) foliageHeight(rand Random, _ int32, _ *treeConfig) int32 {
	return r.height.sample(rand)
}

func (r randomSpreadFoliage) createFoliage(t *tree, _ int32, a foliageAttachment, foliageHeight, radius int32) {
	r.offset.sample(t.rand)
	rand := t.rand
	for i := int32(0); i < r.attempts; i++ {
		dx := rand.NextIntn(radius) - rand.NextIntn(radius)
		dy := rand.NextIntn(foliageHeight) - rand.NextIntn(foliageHeight)
		dz := rand.NextIntn(radius) - rand.NextIntn(radius)
		t.tryPlaceLeaf(a.pos.add(Point{dx, dy, dz}))
	}
}

type cherryFoliage struct {
	foliageBase
	height                                      intProvider
	wideHole, cornerHole, hanging, hangingExtra float32
}

func (c cherryFoliage) foliageHeight(rand Random, _ int32, _ *treeConfig) int32 {
	return c.height.sample(rand)
}

func (c cherryFoliage) skip(rand Random, dx, y, dz, rng int32, _ bool) bool {
	if y == -1 && (dx == rng || dz == rng) && rand.NextFloat() < c.wideHole {
		return true
	}
	corner := dx == rng && dz == rng
	if rng > 2 {
		return corner || (dx+dz > rng*2-2 && rand.NextFloat() < c.cornerHole)
	}
	return corner && rand.NextFloat() < c.cornerHole
}

func (c cherryFoliage) createFoliage(t *tree, _ int32, a foliageAttachment, foliageHeight, radius int32) {
	offset := c.offset.sample(t.rand)
	large := a.doubleTrunk
	p := Point{a.pos.X, a.pos.Y + offset, a.pos.Z}
	r := radius + a.radiusOffset - 1
	skip := absSkip(c.skip)
	t.placeLeavesRow(skip, p, r-2, foliageHeight-3, large)
	t.placeLeavesRow(skip, p, r-1, foliageHeight-4, large)
	for y := foliageHeight - 5; y >= 0; y-- {
		t.placeLeavesRow(skip, p, r, y, large)
	}
	t.placeLeavesRowHanging(skip, p, r, -1, large, c.hanging, c.hangingExtra)
	t.placeLeavesRowHanging(skip, p, r-1, -2, large, c.hanging, c.hangingExtra)
}

func init() {
	blob := func(cfg any) blobFoliage { return blobFoliage{decodeFoliageBase(cfg), fint(ffield(cfg, "height"), 0)} }
	treeFoliagePlacers["minecraft:spruce_foliage_placer"] = func(cfg any) foliagePlacer {
		return spruceFoliage{decodeFoliageBase(cfg), decodeIntProvider(ffield(cfg, "trunk_height"))}
	}
	treeFoliagePlacers["minecraft:pine_foliage_placer"] = func(cfg any) foliagePlacer {
		return pineFoliage{decodeFoliageBase(cfg), decodeIntProvider(ffield(cfg, "height"))}
	}
	treeFoliagePlacers["minecraft:bush_foliage_placer"] = func(cfg any) foliagePlacer { return bushFoliage{blob(cfg)} }
	treeFoliagePlacers["minecraft:acacia_foliage_placer"] = func(cfg any) foliagePlacer { return acaciaFoliage{decodeFoliageBase(cfg)} }
	treeFoliagePlacers["minecraft:fancy_foliage_placer"] = func(cfg any) foliagePlacer { return fancyFoliage{blob(cfg)} }
	treeFoliagePlacers["minecraft:jungle_foliage_placer"] = func(cfg any) foliagePlacer {
		return megaJungleFoliage{decodeFoliageBase(cfg), fint(ffield(cfg, "height"), 0)}
	}
	treeFoliagePlacers["minecraft:mega_pine_foliage_placer"] = func(cfg any) foliagePlacer {
		return megaPineFoliage{decodeFoliageBase(cfg), decodeIntProvider(ffield(cfg, "crown_height"))}
	}
	treeFoliagePlacers["minecraft:dark_oak_foliage_placer"] = func(cfg any) foliagePlacer { return darkOakFoliage{decodeFoliageBase(cfg)} }
	treeFoliagePlacers["minecraft:random_spread_foliage_placer"] = func(cfg any) foliagePlacer {
		return randomSpreadFoliage{decodeFoliageBase(cfg), decodeIntProvider(ffield(cfg, "foliage_height")), fint(ffield(cfg, "leaf_placement_attempts"), 0)}
	}
	treeFoliagePlacers["minecraft:cherry_foliage_placer"] = func(cfg any) foliagePlacer {
		return cherryFoliage{decodeFoliageBase(cfg), decodeIntProvider(ffield(cfg, "height")),
			ffloat(ffield(cfg, "wide_bottom_layer_hole_chance"), 0), ffloat(ffield(cfg, "corner_hole_chance"), 0),
			ffloat(ffield(cfg, "hanging_leaves_chance"), 0), ffloat(ffield(cfg, "hanging_leaves_extension_chance"), 0)}
	}
}
