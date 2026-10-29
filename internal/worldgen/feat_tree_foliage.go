package worldgen

import (
	"encoding/json"

	"mc-go/internal/world"
)

// foliagePlacer is a FoliagePlacer.
type foliagePlacer interface {
	foliageHeight(r Random, treeHeight int32) int32
	foliageRadius(r Random, h int32) int32
	sampleOffset(r Random) int32
	create(t *treeCtx, treeHeight int32, att foliageAttachment, fh, fr, off int32)
	skip(r Random, dx, y, dz, rng int32, double bool) bool
	skipSigned(r Random, dx, y, dz, rng int32, double bool) bool
}

// foliageBase holds FoliagePlacer's radius and offset providers and the
// default signed-skip folding.
type foliageBase struct {
	radius, off intProvider
	self        foliagePlacer
}

func (b *foliageBase) foliageRadius(r Random, _ int32) int32 { return b.radius(r) }
func (b *foliageBase) sampleOffset(r Random) int32           { return b.off(r) }

// skipSigned is FoliagePlacer.shouldSkipLocationSigned: a double trunk
// folds the offset onto the nearer of its two columns.
func (b *foliageBase) skipSigned(r Random, dx, y, dz, rng int32, double bool) bool {
	var ax, az int32
	if double {
		ax = min(absI(dx), absI(dx-1))
		az = min(absI(dz), absI(dz-1))
	} else {
		ax, az = absI(dx), absI(dz)
	}
	return b.self.skip(r, ax, y, az, rng, double)
}

// createFoliage is FoliagePlacer.createFoliage: the offset is sampled
// per attachment.
func createFoliage(fp foliagePlacer, t *treeCtx, treeHeight int32, att foliageAttachment, fh, fr int32) {
	fp.create(t, treeHeight, att, fh, fr, fp.sampleOffset(t.r))
}

// placeLeavesRow is FoliagePlacer.placeLeavesRow.
func placeLeavesRow(fp foliagePlacer, t *treeCtx, pos BlockPos, rng, y int32, double bool) {
	e := int32(0)
	if double {
		e = 1
	}
	for dx := -rng; dx <= rng+e; dx++ {
		for dz := -rng; dz <= rng+e; dz++ {
			if !fp.skipSigned(t.r, dx, y, dz, rng, double) {
				tryPlaceLeaf(t, offset(pos, dx, y, dz))
			}
		}
	}
}

// placeLeavesRowWithHangingLeavesBelow is the FoliagePlacer helper the
// cherry placer uses: a row, then chance-gated hanging leaves (and
// their extensions) under the row's rim.
func placeLeavesRowWithHangingLeavesBelow(fp foliagePlacer, t *treeCtx, pos BlockPos, rng, y int32, double bool, hang, ext float32) {
	placeLeavesRow(fp, t, pos, rng, y, double)
	e := int32(0)
	if double {
		e = 1
	}
	below := offset(pos, 0, -1, 0)
	for _, d := range horizontalDirs {
		cw := clockWise(d)
		n := rng
		if cw == dirSouth || cw == dirEast {
			n = rng + e
		}
		m := relDir(relDir(offset(pos, 0, y-1, 0), cw, n), d, -rng)
		for i := -rng; i < rng+e; i++ {
			set := t.leaves.contains(offset(m, 0, 1, 0))
			if set && tryPlaceExtension(t, hang, below, m) {
				tryPlaceExtension(t, ext, below, offset(m, 0, -1, 0))
			}
			m = relDir(m, d, 1)
		}
	}
}

func tryPlaceExtension(t *treeCtx, chance float32, origin, p BlockPos) bool {
	if distManhattan(p, origin) >= 7 {
		return false
	}
	if t.r.NextFloat() > chance {
		return false
	}
	return tryPlaceLeaf(t, p)
}

// tryPlaceLeaf is FoliagePlacer.tryPlaceLeaf: persistent leaves and
// invalid tree positions are kept; a waterlogged-capable leaf takes
// waterlogging from a water source there.
func tryPlaceLeaf(t *treeCtx, p BlockPos) bool {
	cur := blockAt(t.rg, p)
	if propIs(cur, "persistent", "true") || !validTreePos(t.rg, p) {
		return false
	}
	b := t.cfg.foliageProvider.getState(t.rg, t.r, p)
	if world.HasProperty(b, "waterlogged") {
		wl := "false"
		if isWaterFluid(cur) && isFluidSource(cur) {
			wl = "true"
		}
		b = withProp(b, "waterlogged", wl)
	}
	t.setLeaf(p, b)
	return true
}

func parseFoliagePlacer(raw json.RawMessage) foliagePlacer {
	var o obj
	jsonUnmarshal(raw, &o)
	base := foliageBase{radius: parseIntProvider(o["radius"]), off: parseIntProvider(o["offset"])}
	var fp foliagePlacer
	switch t := trimNS(o.str("type")); t {
	case "blob_foliage_placer":
		p := &blobFoliage{foliageBase: base, height: o.i32("height")}
		fp = p
	case "fancy_foliage_placer":
		p := &fancyFoliage{blobFoliage{foliageBase: base, height: o.i32("height")}}
		fp = p
	case "bush_foliage_placer":
		p := &bushFoliage{blobFoliage{foliageBase: base, height: o.i32("height")}}
		fp = p
	case "spruce_foliage_placer":
		fp = &spruceFoliage{foliageBase: base, trunkHeight: parseIntProvider(o["trunk_height"])}
	case "pine_foliage_placer":
		fp = &pineFoliage{foliageBase: base, height: parseIntProvider(o["height"])}
	case "mega_pine_foliage_placer":
		fp = &megaPineFoliage{foliageBase: base, crown: parseIntProvider(o["crown_height"])}
	case "jungle_foliage_placer":
		fp = &megaJungleFoliage{foliageBase: base, height: o.i32("height")}
	case "acacia_foliage_placer":
		fp = &acaciaFoliage{foliageBase: base}
	case "dark_oak_foliage_placer":
		fp = &darkOakFoliage{foliageBase: base}
	case "random_spread_foliage_placer":
		fp = &randomSpreadFoliage{foliageBase: base, height: parseIntProvider(o["foliage_height"]), attempts: o.i32("leaf_placement_attempts")}
	case "cherry_foliage_placer":
		fp = &cherryFoliage{foliageBase: base, height: parseIntProvider(o["height"]),
			wideHole: parseF32(o["wide_bottom_layer_hole_chance"]), cornerHole: parseF32(o["corner_hole_chance"]),
			hang: parseF32(o["hanging_leaves_chance"]), hangExt: parseF32(o["hanging_leaves_extension_chance"])}
	default:
		panic("worldgen: unknown foliage placer " + t)
	}
	setSelf(fp)
	return fp
}

func setSelf(fp foliagePlacer) {
	switch p := fp.(type) {
	case *blobFoliage:
		p.self = p
	case *fancyFoliage:
		p.self = p
	case *bushFoliage:
		p.self = p
	case *spruceFoliage:
		p.self = p
	case *pineFoliage:
		p.self = p
	case *megaPineFoliage:
		p.self = p
	case *megaJungleFoliage:
		p.self = p
	case *acaciaFoliage:
		p.self = p
	case *darkOakFoliage:
		p.self = p
	case *randomSpreadFoliage:
		p.self = p
	case *cherryFoliage:
		p.self = p
	}
}

// blobFoliage is BlobFoliagePlacer.
type blobFoliage struct {
	foliageBase
	height int32
}

func (b *blobFoliage) foliageHeight(Random, int32) int32 { return b.height }

func (b *blobFoliage) create(t *treeCtx, _ int32, att foliageAttachment, fh, fr, off int32) {
	for y := off; y >= off-fh; y-- {
		rr := max(fr+att.radiusOffset-1-y/2, 0)
		placeLeavesRow(b, t, att.pos, rr, y, att.double)
	}
}

func (b *blobFoliage) skip(r Random, dx, y, dz, rng int32, _ bool) bool {
	return dx == rng && dz == rng && (r.NextIntN(2) == 0 || y == 0)
}

// fancyFoliage is FancyFoliagePlacer.
type fancyFoliage struct{ blobFoliage }

func (f *fancyFoliage) create(t *treeCtx, _ int32, att foliageAttachment, fh, fr, off int32) {
	for y := off; y >= off-fh; y-- {
		rr := fr + 1
		if y == off || y == off-fh {
			rr = fr
		}
		placeLeavesRow(f, t, att.pos, rr, y, att.double)
	}
}

func (f *fancyFoliage) skip(_ Random, dx, _, dz, rng int32, _ bool) bool {
	a := float32(dx) + 0.5
	b := float32(dz) + 0.5
	return float32(a*a)+float32(b*b) > float32(rng*rng)
}

// bushFoliage is BushFoliagePlacer.
type bushFoliage struct{ blobFoliage }

func (b *bushFoliage) create(t *treeCtx, _ int32, att foliageAttachment, fh, fr, off int32) {
	for y := off; y >= off-fh; y-- {
		placeLeavesRow(b, t, att.pos, fr+att.radiusOffset-1-y, y, att.double)
	}
}

func (b *bushFoliage) skip(r Random, dx, _, dz, rng int32, _ bool) bool {
	return dx == rng && dz == rng && r.NextIntN(2) == 0
}

// spruceFoliage is SpruceFoliagePlacer.
type spruceFoliage struct {
	foliageBase
	trunkHeight intProvider
}

func (s *spruceFoliage) foliageHeight(r Random, h int32) int32 { return max(4, h-s.trunkHeight(r)) }

func (s *spruceFoliage) create(t *treeCtx, _ int32, att foliageAttachment, fh, fr, off int32) {
	i := t.r.NextIntN(2)
	j := int32(1)
	k := int32(0)
	for y := off; y >= -fh; y-- {
		placeLeavesRow(s, t, att.pos, i, y, att.double)
		if i >= j {
			i = k
			k = 1
			j = min(j+1, fr+att.radiusOffset)
		} else {
			i++
		}
	}
}

func (s *spruceFoliage) skip(_ Random, dx, _, dz, rng int32, _ bool) bool {
	return dx == rng && dz == rng && rng > 0
}

// pineFoliage is PineFoliagePlacer.
type pineFoliage struct {
	foliageBase
	height intProvider
}

func (p *pineFoliage) foliageHeight(r Random, _ int32) int32 { return p.height(r) }

func (p *pineFoliage) foliageRadius(r Random, h int32) int32 {
	v := p.radius(r)
	return v + r.NextIntN(max(h+1, 1))
}

func (p *pineFoliage) create(t *treeCtx, _ int32, att foliageAttachment, fh, fr, off int32) {
	i := int32(0)
	for y := off; y >= off-fh; y-- {
		placeLeavesRow(p, t, att.pos, i, y, att.double)
		if i >= 1 && y == off-fh+1 {
			i--
		} else if i < fr+att.radiusOffset {
			i++
		}
	}
}

func (p *pineFoliage) skip(_ Random, dx, _, dz, rng int32, _ bool) bool {
	return dx == rng && dz == rng && rng > 0
}

// megaPineFoliage is MegaPineFoliagePlacer.
type megaPineFoliage struct {
	foliageBase
	crown intProvider
}

func (m *megaPineFoliage) foliageHeight(r Random, _ int32) int32 { return m.crown(r) }

func (m *megaPineFoliage) create(t *treeCtx, _ int32, att foliageAttachment, fh, fr, off int32) {
	pos := att.pos
	prev := int32(0)
	for y := pos[1] - fh + off; y <= pos[1]+off; y++ {
		d := pos[1] - y
		k := fr + att.radiusOffset + floorI(float64(float32(float32(d)/float32(fh))*3.5))
		rr := k
		if d > 0 && k == prev && y&1 == 0 {
			rr = k + 1
		}
		placeLeavesRow(m, t, BlockPos{pos[0], y, pos[2]}, rr, 0, att.double)
		prev = k
	}
}

func (m *megaPineFoliage) skip(_ Random, dx, _, dz, rng int32, _ bool) bool {
	if dx+dz >= 7 {
		return true
	}
	return dx*dx+dz*dz > rng*rng
}

// megaJungleFoliage is MegaJungleFoliagePlacer (jungle_foliage_placer).
type megaJungleFoliage struct {
	foliageBase
	height int32
}

func (m *megaJungleFoliage) foliageHeight(Random, int32) int32 { return m.height }

func (m *megaJungleFoliage) create(t *treeCtx, _ int32, att foliageAttachment, fh, fr, off int32) {
	n := fh
	if !att.double {
		n = 1 + t.r.NextIntN(2)
	}
	for y := off; y >= off-n; y-- {
		placeLeavesRow(m, t, att.pos, fr+att.radiusOffset+1-y, y, att.double)
	}
}

func (m *megaJungleFoliage) skip(_ Random, dx, _, dz, rng int32, _ bool) bool {
	if dx+dz >= 7 {
		return true
	}
	return dx*dx+dz*dz > rng*rng
}

// acaciaFoliage is AcaciaFoliagePlacer.
type acaciaFoliage struct{ foliageBase }

func (a *acaciaFoliage) foliageHeight(Random, int32) int32 { return 0 }

func (a *acaciaFoliage) create(t *treeCtx, _ int32, att foliageAttachment, fh, fr, off int32) {
	pos := offset(att.pos, 0, off, 0)
	placeLeavesRow(a, t, pos, fr+att.radiusOffset, -1-fh, att.double)
	placeLeavesRow(a, t, pos, fr-1, -fh, att.double)
	placeLeavesRow(a, t, pos, fr+att.radiusOffset-1, 0, att.double)
}

func (a *acaciaFoliage) skip(_ Random, dx, y, dz, rng int32, _ bool) bool {
	if y == 0 {
		return (dx > 1 || dz > 1) && dx != 0 && dz != 0
	}
	return dx == rng && dz == rng && rng > 0
}

// darkOakFoliage is DarkOakFoliagePlacer.
type darkOakFoliage struct{ foliageBase }

func (d *darkOakFoliage) foliageHeight(Random, int32) int32 { return 4 }

func (d *darkOakFoliage) create(t *treeCtx, _ int32, att foliageAttachment, fh, fr, off int32) {
	pos := offset(att.pos, 0, off, 0)
	dbl := att.double
	if dbl {
		placeLeavesRow(d, t, pos, fr+2, -1, dbl)
		placeLeavesRow(d, t, pos, fr+3, 0, dbl)
		placeLeavesRow(d, t, pos, fr+2, 1, dbl)
		if t.r.NextBool() {
			placeLeavesRow(d, t, pos, fr, 2, dbl)
		}
	} else {
		placeLeavesRow(d, t, pos, fr+2, -1, dbl)
		placeLeavesRow(d, t, pos, fr+1, 0, dbl)
	}
}

func (d *darkOakFoliage) skipSigned(r Random, dx, y, dz, rng int32, double bool) bool {
	if y == 0 && double && (dx == -rng || dx >= rng) && (dz == -rng || dz >= rng) {
		return true
	}
	return d.foliageBase.skipSigned(r, dx, y, dz, rng, double)
}

func (d *darkOakFoliage) skip(_ Random, dx, y, dz, rng int32, double bool) bool {
	if y == -1 && !double {
		return dx == rng && dz == rng
	}
	if y == 1 {
		return dx+dz > rng*2-2
	}
	return false
}

// randomSpreadFoliage is RandomSpreadFoliagePlacer (mangrove, azalea).
type randomSpreadFoliage struct {
	foliageBase
	height   intProvider
	attempts int32
}

func (s *randomSpreadFoliage) foliageHeight(r Random, _ int32) int32 { return s.height(r) }

func (s *randomSpreadFoliage) create(t *treeCtx, _ int32, att foliageAttachment, fh, fr, off int32) {
	r := t.r
	for i := int32(0); i < s.attempts; i++ {
		x := r.NextIntN(fr) - r.NextIntN(fr)
		y := r.NextIntN(fh) - r.NextIntN(fh)
		z := r.NextIntN(fr) - r.NextIntN(fr)
		tryPlaceLeaf(t, offset(att.pos, x, y, z))
	}
}

func (s *randomSpreadFoliage) skip(Random, int32, int32, int32, int32, bool) bool { return false }

// cherryFoliage is CherryFoliagePlacer.
type cherryFoliage struct {
	foliageBase
	height                              intProvider
	wideHole, cornerHole, hang, hangExt float32
}

func (c *cherryFoliage) foliageHeight(r Random, _ int32) int32 { return c.height(r) }

func (c *cherryFoliage) create(t *treeCtx, _ int32, att foliageAttachment, fh, fr, off int32) {
	dbl := att.double
	pos := offset(att.pos, 0, off, 0)
	k := fr + att.radiusOffset - 1
	placeLeavesRow(c, t, pos, k-2, fh-3, dbl)
	placeLeavesRow(c, t, pos, k-1, fh-4, dbl)
	for y := fh - 5; y >= 0; y-- {
		placeLeavesRow(c, t, pos, k, y, dbl)
	}
	placeLeavesRowWithHangingLeavesBelow(c, t, pos, k, -1, dbl, c.hang, c.hangExt)
	placeLeavesRowWithHangingLeavesBelow(c, t, pos, k-1, -2, dbl, c.hang, c.hangExt)
}

func (c *cherryFoliage) skip(r Random, dx, y, dz, rng int32, _ bool) bool {
	if y == -1 && (dx == rng || dz == rng) && r.NextFloat() < c.wideHole {
		return true
	}
	corner := dx == rng && dz == rng
	if rng > 2 {
		return corner || (dx+dz > rng*2-2 && r.NextFloat() < c.cornerHole)
	}
	return corner && r.NextFloat() < c.cornerHole
}
