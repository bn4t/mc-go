package worldgen

import (
	"mc-go/internal/world"
)

// Nether terrain features: glowstone blobs, basalt columns and pillars,
// lava deltas and netherrack replacement blobs.

func init() {
	featureBuilders["glowstone_blob"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return func(c *featCtx, r Random, p BlockPos) bool { return nethGlowstone(c.rg, r, p) }
	}
	featureBuilders["basalt_columns"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		height := parseIntProvider(cfg["height"])
		reach := parseIntProvider(cfg["reach"])
		return func(c *featCtx, r Random, p BlockPos) bool { return nethBasaltColumns(c.rg, r, height, reach, p) }
	}
	featureBuilders["basalt_pillar"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return func(c *featCtx, r Random, p BlockPos) bool { return nethBasaltPillar(c.rg, r, p) }
	}
	featureBuilders["delta_feature"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		var co, ri obj
		jsonUnmarshal(cfg["contents"], &co)
		jsonUnmarshal(cfg["rim"], &ri)
		d := &nethDelta{
			contents: stateFromJSON(co),
			rim:      stateFromJSON(ri),
			size:     parseIntProvider(cfg["size"]),
			rimSize:  parseIntProvider(cfg["rim_size"]),
		}
		return func(c *featCtx, r Random, p BlockPos) bool { return d.place(c.rg, r, p) }
	}
	featureBuilders["netherrack_replace_blobs"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		var tg, st obj
		jsonUnmarshal(cfg["target"], &tg)
		jsonUnmarshal(cfg["state"], &st)
		target, _ := world.StateOwner(stateFromJSON(tg))
		state := stateFromJSON(st)
		radius := parseIntProvider(cfg["radius"])
		return func(c *featCtx, r Random, p BlockPos) bool {
			return nethReplaceBlobs(c.rg, r, target, state, radius, p)
		}
	}
}

func nethIs(b world.Block, names ...string) bool {
	n, _ := world.StateOwner(b)
	for _, m := range names {
		if n == m {
			return true
		}
	}
	return false
}

// nethGlowstone is GlowstoneFeature.place: an air origin hanging from
// netherrack, basalt or blackstone gets glowstone, then 1500 tries at
// offsets (n8-n8, -n12, n8-n8) turn air cells touching exactly one
// glowstone face into glowstone.
func nethGlowstone(rg *Region, r Random, o BlockPos) bool {
	if !isAir(rg.at(o)) {
		return false
	}
	if !nethIs(rg.at(offset(o, 0, 1, 0)), "netherrack", "basalt", "blackstone") {
		return false
	}
	glow := blockNamed("glowstone")
	rg.set(o, glow)
	for i := 0; i < 1500; i++ {
		dx := r.NextIntN(8) - r.NextIntN(8)
		dy := -r.NextIntN(12)
		dz := r.NextIntN(8) - r.NextIntN(8)
		q := offset(o, dx, dy, dz)
		if !isAir(rg.at(q)) {
			continue
		}
		n := 0
		for _, d := range dirOffsets {
			if isBlock(rg.at(offset(q, d[0], d[1], d[2])), "glowstone") {
				n++
			}
			if n > 1 {
				break
			}
		}
		if n == 1 {
			rg.set(q, glow)
		}
	}
	return true
}

// nethColumnBlocked is BasaltColumnsFeature's CANNOT_PLACE_ON list.
func nethColumnBlocked(b world.Block) bool {
	return nethIs(b, "lava", "bedrock", "magma_block", "soul_sand", "nether_bricks", "nether_brick_fence",
		"nether_brick_stairs", "nether_wart", "chest", "spawner")
}

// nethAirOrLavaSea is isAirOrLavaOcean: air, or lava at or below the
// sea level.
func nethAirOrLavaSea(rg *Region, sea int32, p BlockPos) bool {
	b := rg.at(p)
	return isAir(b) || (isBlock(b, "lava") && p[1] <= sea)
}

// nethColumnBase is canPlaceAt: air/lava-sea over a non-air floor that
// is not on the blocked list.
func nethColumnBase(rg *Region, sea int32, p BlockPos) bool {
	if !nethAirOrLavaSea(rg, sea, p) {
		return false
	}
	below := rg.at(offset(p, 0, -1, 0))
	return !isAir(below) && !nethColumnBlocked(below)
}

// nethBasaltColumns is BasaltColumnsFeature.place: from a valid base,
// height h and a 90/10 clustered/unclustered roll set the scatter
// radius (5 or 8, capped by h) and count (50 or 15); each random cell
// of the flat square (BlockPos.randomBetweenClosed, drawn lazily) with
// h minus its Manhattan distance >= 0 grows a column of that height
// with a fresh reach sample.
func nethBasaltColumns(rg *Region, r Random, height, reach intProvider, o BlockPos) bool {
	sea := rg.seaLevel()
	if !nethColumnBase(rg, sea, o) {
		return false
	}
	h := height(r)
	clustered := r.NextFloat() < 0.9
	size := min(h, 8)
	count := 15
	if clustered {
		size = min(h, 5)
		count = 50
	}
	placed := false
	w := 2*size + 1
	for i := 0; i < count; i++ {
		x := o[0] - size + r.NextIntN(w)
		y := o[1] + r.NextIntN(1)
		z := o[2] - size + r.NextIntN(w)
		q := BlockPos{x, y, z}
		left := h - manhattan(q, o)
		if left >= 0 {
			if nethColumn(rg, sea, q, left, reach(r)) {
				placed = true
			}
		}
	}
	return placed
}

func manhattan(a, b BlockPos) int32 {
	return absI32(a[0]-b[0]) + absI32(a[1]-b[1]) + absI32(a[2]-b[2])
}

func absI32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// nethColumn is placeColumn: every cell of the reach square (x fastest,
// then z) finds a base — sinking through air/lava sea to a placeable
// floor, or rising through solid to air — and stacks basalt up through
// air/lava sea for height - dist/2 cells, passing through basalt
// already there and stopping at anything else.
func nethColumn(rg *Region, sea int32, p BlockPos, height, reach int32) bool {
	placed := false
	basalt := blockNamed("basalt")
	for z := p[2] - reach; z <= p[2]+reach; z++ {
		for x := p[0] - reach; x <= p[0]+reach; x++ {
			q := BlockPos{x, p[1], z}
			d := manhattan(q, p)
			var base BlockPos
			var ok bool
			if nethAirOrLavaSea(rg, sea, q) {
				base, ok = nethFindSurface(rg, sea, q, d)
			} else {
				base, ok = nethFindAir(rg, q, d)
			}
			if !ok {
				continue
			}
			m := base
			for k := height - d/2; k >= 0; k-- {
				if nethAirOrLavaSea(rg, sea, m) {
					rg.set(m, basalt)
					m[1]++
					placed = true
				} else if isBlock(rg.at(m), "basalt") {
					m[1]++
				} else {
					break
				}
			}
		}
	}
	return placed
}

// nethFindSurface sinks at most dist cells to a canPlaceAt cell.
func nethFindSurface(rg *Region, sea int32, p BlockPos, dist int32) (BlockPos, bool) {
	for p[1] > rg.MinY()+1 && dist > 0 {
		dist--
		if nethColumnBase(rg, sea, p) {
			return p, true
		}
		p[1]--
	}
	return p, false
}

// nethFindAir rises at most dist cells to air, failing on a blocked
// block.
func nethFindAir(rg *Region, p BlockPos, dist int32) (BlockPos, bool) {
	for p[1] <= rg.MaxY() && dist > 0 {
		dist--
		b := rg.at(p)
		if nethColumnBlocked(b) {
			return p, false
		}
		if isAir(b) {
			return p, true
		}
		p[1]++
	}
	return p, false
}

// nethBasaltPillar is BasaltPillarFeature.place: from an air cell under
// a ceiling, basalt runs down through air with hang-offs on each side
// (each side keeps trying while its last 9-in-10 roll placed), coin-flip
// hang-offs around the lowest cell, then a 7x7 skirt on the floor whose
// cells roll nextInt(10) < 10-|dx*dz| and settle up to three cells
// down.
func nethBasaltPillar(rg *Region, r Random, o BlockPos) bool {
	if !isAir(rg.at(o)) || isAir(rg.at(offset(o, 0, 1, 0))) {
		return false
	}
	basalt := blockNamed("basalt")
	hang := func(p BlockPos) bool {
		if r.NextIntN(10) != 0 {
			rg.set(p, basalt)
			return true
		}
		return false
	}
	m := o
	n, s, w, e := true, true, true, true
	for isAir(rg.at(m)) {
		if !rg.inBuildHeight(m[1]) {
			return true
		}
		rg.set(m, basalt)
		n = n && hang(offset(m, 0, 0, -1))
		s = s && hang(offset(m, 0, 0, 1))
		w = w && hang(offset(m, -1, 0, 0))
		e = e && hang(offset(m, 1, 0, 0))
		m[1]--
	}
	m[1]++
	for _, d := range [4]BlockPos{{0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}} {
		if r.NextBool() {
			rg.set(offset(m, d[0], d[1], d[2]), basalt)
		}
	}
	m[1]--
	for dx := int32(-3); dx < 4; dx++ {
		for dz := int32(-3); dz < 4; dz++ {
			k := absI32(dx) * absI32(dz)
			if r.NextIntN(10) >= 10-k {
				continue
			}
			q := offset(m, dx, 0, dz)
			for left := 3; isAir(rg.at(offset(q, 0, -1, 0))); {
				q[1]--
				left--
				if left <= 0 {
					break
				}
			}
			if !isAir(rg.at(offset(q, 0, -1, 0))) {
				rg.set(q, basalt)
			}
		}
	}
	return true
}

// nethManhattan is BlockPos.withinManhattan's iteration order: shells
// of growing Manhattan depth; within a shell x rises, then y, and each
// (x, y) yields +z before its mirrored -z.
func nethManhattan(o BlockPos, rx, ry, rz int32, yield func(BlockPos) bool) {
	maxDepth := rx + ry + rz
	for depth := int32(0); depth <= maxDepth; depth++ {
		mx := min(rx, depth)
		for x := -mx; x <= mx; x++ {
			my := min(ry, depth-absI32(x))
			for y := -my; y <= my; y++ {
				z := depth - absI32(x) - absI32(y)
				if z > rz {
					continue
				}
				if !yield(BlockPos{o[0] + x, o[1] + y, o[2] + z}) {
					return
				}
				if z != 0 && !yield(BlockPos{o[0] + x, o[1] + y, o[2] - z}) {
					return
				}
			}
		}
	}
}

type nethDelta struct {
	contents, rim world.Block
	size, rimSize intProvider
}

// nethDeltaClear is DeltaFeature.isClear: not already the contents
// block nor a protected block, every side but the top closed and the
// top open.
func (d *nethDelta) clear(rg *Region, p BlockPos) bool {
	b := rg.at(p)
	if cn, _ := world.StateOwner(d.contents); isBlock(b, cn) {
		return false
	}
	if nethIs(b, "bedrock", "nether_bricks", "nether_brick_fence", "nether_brick_stairs", "nether_wart", "chest", "spawner") {
		return false
	}
	for i, o := range dirOffsets {
		air := isAir(rg.at(offset(p, o[0], o[1], o[2])))
		if air != (i == dirUp) {
			return false
		}
	}
	return true
}

// place is DeltaFeature.place: a 9-in-10 rim roll draws both rim
// offsets (a rim only when both are non-zero), then the two size draws
// span a withinManhattan disc walked until the larger size; clear cells
// take the rim, and the cell shifted by the rim offsets the contents.
func (d *nethDelta) place(rg *Region, r Random, o BlockPos) bool {
	placed := false
	hasRim := r.NextDouble() < 0.9
	var rx, rz int32
	if hasRim {
		rx = d.rimSize(r)
	}
	if hasRim {
		rz = d.rimSize(r)
	}
	rim := hasRim && rx != 0 && rz != 0
	sx := d.size(r)
	sz := d.size(r)
	most := max(sx, sz)
	nethManhattan(o, sx, 0, sz, func(q BlockPos) bool {
		if manhattan(q, o) > most {
			return false
		}
		if d.clear(rg, q) {
			if rim {
				placed = true
				rg.set(q, d.rim)
			}
			c := offset(q, rx, 0, rz)
			if d.clear(rg, c) {
				placed = true
				rg.set(c, d.contents)
			}
		}
		return true
	})
	return placed
}

// nethReplaceBlobs is ReplaceBlobsFeature.place: sink from the origin
// (clamped into the build range) to the first target block, then three
// radius draws span a withinManhattan blob, walked up to the largest
// radius, whose target blocks are replaced.
func nethReplaceBlobs(rg *Region, r Random, target string, state world.Block, radius intProvider, o BlockPos) bool {
	p := o
	p[1] = min(max(p[1], rg.MinY()+1), rg.MaxY())
	found := false
	for p[1] > rg.MinY()+1 {
		if isBlock(rg.at(p), target) {
			found = true
			break
		}
		p[1]--
	}
	if !found {
		return false
	}
	a, b, c := radius(r), radius(r), radius(r)
	most := max(a, b, c)
	placed := false
	nethManhattan(p, a, b, c, func(q BlockPos) bool {
		if manhattan(q, p) > most {
			return false
		}
		if isBlock(rg.at(q), target) {
			rg.set(q, state)
			placed = true
		}
		return true
	})
	return placed
}
