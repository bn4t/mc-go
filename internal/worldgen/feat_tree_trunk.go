package worldgen

import (
	"encoding/json"
	"math"

	"mc-go/internal/world"
)

// foliageAttachment is FoliagePlacer.FoliageAttachment.
type foliageAttachment struct {
	pos          BlockPos
	radiusOffset int32
	double       bool
}

// trunkPlacer is a TrunkPlacer.
type trunkPlacer interface {
	treeHeight(r Random) int32
	placeTrunk(t *treeCtx, height int32, origin BlockPos) []foliageAttachment
	validPos(rg *Region, p BlockPos) bool
}

// trunkBase holds TrunkPlacer's height parameters and the default
// validTreePos.
type trunkBase struct{ base, randA, randB int32 }

// treeHeight is TrunkPlacer.getTreeHeight.
func (b trunkBase) treeHeight(r Random) int32 {
	h := b.base + r.NextIntN(b.randA+1)
	return h + r.NextIntN(b.randB+1)
}

func (trunkBase) validPos(rg *Region, p BlockPos) bool { return validTreePos(rg, p) }

// trunkIsFree is TrunkPlacer.isFree: a valid tree position or a log.
func trunkIsFree(tp trunkPlacer, rg *Region, p BlockPos) bool {
	return tp.validPos(rg, p) || stateInTag(blockAt(rg, p), "logs")
}

// placeLog is TrunkPlacer.placeLog with a state modifier (nil is the
// identity).
func placeLog(tp trunkPlacer, t *treeCtx, p BlockPos, f func(world.Block) world.Block) bool {
	if !tp.validPos(t.rg, p) {
		return false
	}
	b := t.cfg.trunkProvider.getState(t.rg, t.r, p)
	if f != nil {
		b = f(b)
	}
	t.setLog(p, b)
	return true
}

func placeLogIfFree(tp trunkPlacer, t *treeCtx, p BlockPos) {
	if trunkIsFree(tp, t.rg, p) {
		placeLog(tp, t, p, nil)
	}
}

// placeBelowTrunk is TrunkPlacer.placeBelowTrunkBlock: the provider's
// optional state goes through the trunk setter (so the block joins the
// tree's logs).
func placeBelowTrunk(t *treeCtx, p BlockPos) {
	if b, ok := t.cfg.belowTrunk(t.rg, t.r, p); ok {
		t.setLog(p, b)
	}
}

func axisSetter(axis string) func(world.Block) world.Block {
	return func(b world.Block) world.Block { return withProp(b, "axis", axis) }
}

func parseTrunkPlacer(raw json.RawMessage) trunkPlacer {
	var o obj
	jsonUnmarshal(raw, &o)
	base := trunkBase{o.i32("base_height"), o.i32("height_rand_a"), o.i32("height_rand_b")}
	switch t := trimNS(o.str("type")); t {
	case "straight_trunk_placer":
		return straightTrunk{base}
	case "forking_trunk_placer":
		return forkingTrunk{base}
	case "giant_trunk_placer":
		return giantTrunk{base}
	case "mega_jungle_trunk_placer":
		return megaJungleTrunk{giantTrunk{base}}
	case "dark_oak_trunk_placer":
		return darkOakTrunk{base}
	case "fancy_trunk_placer":
		return fancyTrunk{base}
	case "bending_trunk_placer":
		return bendingTrunk{base, o.i32("min_height_for_leaves"), parseIntProvider(o["bend_length"])}
	case "upwards_branching_trunk_placer":
		return upwardsBranchingTrunk{trunkBase: base,
			extraSteps:  parseIntProvider(o["extra_branch_steps"]),
			perLog:      parseF32(o["place_branch_per_log_probability"]),
			extraLength: parseIntProvider(o["extra_branch_length"]),
			growThrough: parseHolderBlocks(o["can_grow_through"]),
		}
	case "cherry_trunk_placer":
		start := parseUniformNoType(o["branch_start_offset_from_top"])
		var so obj
		jsonUnmarshal(o["branch_start_offset_from_top"], &so)
		lo, hi := so.i32("min_inclusive"), so.i32("max_inclusive")-1
		return cherryTrunk{trunkBase: base,
			count:      parseIntProvider(o["branch_count"]),
			horizontal: parseIntProvider(o["branch_horizontal_length"]),
			start:      start,
			start2:     func(r Random) int32 { return randBetweenInclusive(r, lo, hi) },
			end:        parseIntProvider(o["branch_end_offset_from_top"]),
		}
	default:
		panic("worldgen: unknown trunk placer " + t)
	}
}

// straightTrunk is StraightTrunkPlacer.
type straightTrunk struct{ trunkBase }

func (s straightTrunk) placeTrunk(t *treeCtx, h int32, o BlockPos) []foliageAttachment {
	placeBelowTrunk(t, offset(o, 0, -1, 0))
	for i := int32(0); i < h; i++ {
		placeLog(s, t, offset(o, 0, i, 0), nil)
	}
	return []foliageAttachment{{offset(o, 0, h, 0), 0, false}}
}

// forkingTrunk is ForkingTrunkPlacer (acacia).
type forkingTrunk struct{ trunkBase }

func (f forkingTrunk) placeTrunk(t *treeCtx, h int32, o BlockPos) []foliageAttachment {
	r := t.r
	placeBelowTrunk(t, offset(o, 0, -1, 0))
	var out []foliageAttachment
	dir := randomHorizontal(r)
	bendAt := h - r.NextIntN(4) - 1
	bend := 3 - r.NextIntN(3)
	x, z := o[0], o[2]
	top, hasTop := int32(0), false
	for i := int32(0); i < h; i++ {
		y := o[1] + i
		if i >= bendAt && bend > 0 {
			x += stepX(dir)
			z += stepZ(dir)
			bend--
		}
		if placeLog(f, t, BlockPos{x, y, z}, nil) {
			top, hasTop = y+1, true
		}
	}
	if hasTop {
		out = append(out, foliageAttachment{BlockPos{x, top, z}, 1, false})
	}
	x, z = o[0], o[2]
	dir2 := randomHorizontal(r)
	if dir2 != dir {
		start := bendAt - r.NextIntN(2) - 1
		n := 1 + r.NextIntN(3)
		hasTop = false
		for i := start; i < h && n > 0; i, n = i+1, n-1 {
			if i < 1 {
				continue
			}
			y := o[1] + i
			x += stepX(dir2)
			z += stepZ(dir2)
			if placeLog(f, t, BlockPos{x, y, z}, nil) {
				top, hasTop = y+1, true
			}
		}
		if hasTop {
			out = append(out, foliageAttachment{BlockPos{x, top, z}, 0, false})
		}
	}
	return out
}

// giantTrunk is GiantTrunkPlacer (2x2 mega spruce/pine).
type giantTrunk struct{ trunkBase }

func (g giantTrunk) placeTrunk(t *treeCtx, h int32, o BlockPos) []foliageAttachment {
	b := offset(o, 0, -1, 0)
	placeBelowTrunk(t, b)
	placeBelowTrunk(t, offset(b, 1, 0, 0))
	placeBelowTrunk(t, offset(b, 0, 0, 1))
	placeBelowTrunk(t, offset(b, 1, 0, 1))
	for i := int32(0); i < h; i++ {
		placeLogIfFree(g, t, offset(o, 0, i, 0))
		if i < h-1 {
			placeLogIfFree(g, t, offset(o, 1, i, 0))
			placeLogIfFree(g, t, offset(o, 1, i, 1))
			placeLogIfFree(g, t, offset(o, 0, i, 1))
		}
	}
	return []foliageAttachment{{offset(o, 0, h, 0), 0, true}}
}

// megaJungleTrunk is MegaJungleTrunkPlacer: the giant trunk plus
// branches every few blocks.
type megaJungleTrunk struct{ giantTrunk }

func (m megaJungleTrunk) placeTrunk(t *treeCtx, h int32, o BlockPos) []foliageAttachment {
	r := t.r
	out := append([]foliageAttachment(nil), m.giantTrunk.placeTrunk(t, h, o)...)
	for i := h - 2 - r.NextIntN(4); i > h/2; i -= 2 + r.NextIntN(4) {
		f := r.NextFloat() * 6.2831855
		var bx, bz int32
		for j := int32(0); j < 5; j++ {
			bx = int32(1.5 + float32(mthCos(float64(f))*float32(j)))
			bz = int32(1.5 + float32(mthSin(float64(f))*float32(j)))
			placeLog(m, t, offset(o, bx, i-3+j/2, bz), nil)
		}
		out = append(out, foliageAttachment{offset(o, bx, i, bz), -2, false})
	}
	return out
}

// darkOakTrunk is DarkOakTrunkPlacer.
type darkOakTrunk struct{ trunkBase }

func (d darkOakTrunk) placeTrunk(t *treeCtx, h int32, o BlockPos) []foliageAttachment {
	r := t.r
	var out []foliageAttachment
	b := offset(o, 0, -1, 0)
	placeBelowTrunk(t, b)
	placeBelowTrunk(t, offset(b, 1, 0, 0))
	placeBelowTrunk(t, offset(b, 0, 0, 1))
	placeBelowTrunk(t, offset(b, 1, 0, 1))
	dir := randomHorizontal(r)
	bendAt := h - r.NextIntN(4)
	bend := 2 - r.NextIntN(3)
	ox, oy, oz := o[0], o[1], o[2]
	x, z := ox, oz
	top := oy + h - 1
	for i := int32(0); i < h; i++ {
		if i >= bendAt && bend > 0 {
			x += stepX(dir)
			z += stepZ(dir)
			bend--
		}
		p := BlockPos{x, oy + i, z}
		if b := blockAt(t.rg, p); isAir(b) || stateInTag(b, "leaves") {
			placeLog(d, t, p, nil)
			placeLog(d, t, offset(p, 1, 0, 0), nil)
			placeLog(d, t, offset(p, 0, 0, 1), nil)
			placeLog(d, t, offset(p, 1, 0, 1), nil)
		}
	}
	out = append(out, foliageAttachment{BlockPos{x, top, z}, 0, true})
	for dx := int32(-1); dx <= 2; dx++ {
		for dz := int32(-1); dz <= 2; dz++ {
			if dx >= 0 && dx <= 1 && dz >= 0 && dz <= 1 {
				continue
			}
			if r.NextIntN(3) > 0 {
				continue
			}
			n := r.NextIntN(3) + 2
			for k := int32(0); k < n; k++ {
				placeLog(d, t, BlockPos{ox + dx, top - k - 1, oz + dz}, nil)
			}
			out = append(out, foliageAttachment{BlockPos{ox + dx, top, oz + dz}, 0, false})
		}
	}
	return out
}

// fancyTrunk is FancyTrunkPlacer (big oak).
type fancyTrunk struct{ trunkBase }

type fancyCoords struct {
	att        foliageAttachment
	branchBase int32
}

func (f fancyTrunk) placeTrunk(t *treeCtx, h int32, o BlockPos) []foliageAttachment {
	r := t.r
	j := h + 2
	k := floorI(float64(float64(j) * 0.618))
	placeBelowTrunk(t, offset(o, 0, -1, 0))
	q := float64(1.382 + float64(float64(j)/13.0)*float64(float64(j)/13.0))
	l := min(int32(1), floorI(q))
	m := o[1] + k
	coords := []fancyCoords{{foliageAttachment{offset(o, 0, j-5, 0), 0, false}, m}}
	for n := j - 5; n >= 0; n-- {
		s := fancyTreeShape(j, n)
		if s < 0 {
			continue
		}
		for c := int32(0); c < l; c++ {
			e := float64(float64(s) * float64(float64(r.NextFloat())+0.328))
			g := float64(float64(r.NextFloat()*2) * math.Pi)
			hx := float64(e*math.Sin(g)) + 0.5
			hz := float64(e*math.Cos(g)) + 0.5
			p := offset(o, floorI(hx), n-1, floorI(hz))
			p2 := offset(p, 0, 5, 0)
			if !f.makeLimb(t, p, p2, false) {
				continue
			}
			dx := o[0] - p[0]
			dz := o[2] - p[2]
			y := float64(float64(p[1]) - float64(math.Sqrt(float64(dx*dx+dz*dz))*0.381))
			var by int32
			if y > float64(m) {
				by = m
			} else {
				by = d2i(y)
			}
			bp := BlockPos{o[0], by, o[2]}
			if f.makeLimb(t, bp, p, false) {
				coords = append(coords, fancyCoords{foliageAttachment{p, 0, false}, bp[1]})
			}
		}
	}
	f.makeLimb(t, o, offset(o, 0, k, 0), true)
	f.makeBranches(t, j, o, coords)
	var out []foliageAttachment
	for _, c := range coords {
		if fancyTrimBranches(j, c.branchBase-o[1]) {
			out = append(out, c.att)
		}
	}
	return out
}

func (f fancyTrunk) makeLimb(t *treeCtx, from, to BlockPos, make bool) bool {
	if !make && from == to {
		return true
	}
	d := BlockPos{to[0] - from[0], to[1] - from[1], to[2] - from[2]}
	steps := max(absI(d[0]), max(absI(d[1]), absI(d[2])))
	fx := float32(d[0]) / float32(steps)
	fy := float32(d[1]) / float32(steps)
	fz := float32(d[2]) / float32(steps)
	for i := int32(0); i <= steps; i++ {
		p := offset(from, floorI(float64(0.5+float32(float32(i)*fx))), floorI(float64(0.5+float32(float32(i)*fy))), floorI(float64(0.5+float32(float32(i)*fz))))
		if make {
			axis := fancyLogAxis(from, p)
			placeLog(f, t, p, axisSetter(axis))
		} else if !trunkIsFree(f, t.rg, p) {
			return false
		}
	}
	return true
}

func fancyLogAxis(a, b BlockPos) string {
	dx := absI(b[0] - a[0])
	dz := absI(b[2] - a[2])
	m := max(dx, dz)
	if m > 0 {
		if dx == m {
			return "x"
		}
		return "z"
	}
	return "y"
}

func fancyTrimBranches(h, l int32) bool { return float64(l) >= float64(float64(h)*0.2) }

func (f fancyTrunk) makeBranches(t *treeCtx, h int32, o BlockPos, coords []fancyCoords) {
	for _, c := range coords {
		b := BlockPos{o[0], c.branchBase, o[2]}
		if b != c.att.pos && fancyTrimBranches(h, c.branchBase-o[1]) {
			f.makeLimb(t, b, c.att.pos, true)
		}
	}
}

func fancyTreeShape(h, y int32) float32 {
	if float32(y) < float32(float32(h)*0.3) {
		return -1
	}
	f := float32(h) / 2
	g := f - float32(y)
	s := float32(math.Sqrt(float64(float32(f*f) - float32(g*g))))
	if g == 0 {
		s = f
	} else if absF(g) >= f {
		return 0
	}
	return s * 0.5
}

func absF(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// bendingTrunk is BendingTrunkPlacer (azalea).
type bendingTrunk struct {
	trunkBase
	minLeaves int32
	bendLen   intProvider
}

func (b bendingTrunk) placeTrunk(t *treeCtx, h int32, o BlockPos) []foliageAttachment {
	r := t.r
	dir := randomHorizontal(r)
	top := h - 1
	m := o
	placeBelowTrunk(t, offset(m, 0, -1, 0))
	var out []foliageAttachment
	for i := int32(0); i <= top; i++ {
		if i+1 >= top+r.NextIntN(2) {
			m = relDir(m, dir, 1)
		}
		if validTreePos(t.rg, m) {
			placeLog(b, t, m, nil)
		}
		if i >= b.minLeaves {
			out = append(out, foliageAttachment{m, 0, false})
		}
		m = offset(m, 0, 1, 0)
	}
	n := b.bendLen(r)
	for i := int32(0); i <= n; i++ {
		if validTreePos(t.rg, m) {
			placeLog(b, t, m, nil)
		}
		out = append(out, foliageAttachment{m, 0, false})
		m = relDir(m, dir, 1)
	}
	return out
}

// upwardsBranchingTrunk is UpwardsBranchingTrunkPlacer (mangrove).
type upwardsBranchingTrunk struct {
	trunkBase
	extraSteps, extraLength intProvider
	perLog                  float32
	growThrough             func(world.Block) bool
}

func (u upwardsBranchingTrunk) validPos(rg *Region, p BlockPos) bool {
	return validTreePos(rg, p) || u.growThrough(blockAt(rg, p))
}

func (u upwardsBranchingTrunk) placeTrunk(t *treeCtx, h int32, o BlockPos) []foliageAttachment {
	r := t.r
	var out []foliageAttachment
	for i := int32(0); i < h; i++ {
		y := o[1] + i
		p := BlockPos{o[0], y, o[2]}
		if placeLog(u, t, p, nil) && i < h-1 && r.NextFloat() < u.perLog {
			dir := randomHorizontal(r)
			l := u.extraLength(r)
			start := max(0, l-u.extraLength(r)-1)
			steps := u.extraSteps(r)
			out = u.placeBranch(t, h, out, p, y, dir, start, steps)
		}
		if i == h-1 {
			out = append(out, foliageAttachment{BlockPos{o[0], y + 1, o[2]}, 0, false})
		}
	}
	return out
}

func (u upwardsBranchingTrunk) placeBranch(t *treeCtx, h int32, out []foliageAttachment, m BlockPos, y int32, dir int, start, steps int32) []foliageAttachment {
	top := y + start
	x, z := m[0], m[2]
	for i := start; i < h && steps > 0; i, steps = i+1, steps-1 {
		if i < 1 {
			continue
		}
		yy := y + i
		x += stepX(dir)
		z += stepZ(dir)
		top = yy
		p := BlockPos{x, yy, z}
		if placeLog(u, t, p, nil) {
			top++
		}
		out = append(out, foliageAttachment{p, 0, false})
	}
	if top-y > 1 {
		p := BlockPos{x, top, z}
		out = append(out, foliageAttachment{p, 0, false}, foliageAttachment{offset(p, 0, -2, 0), 0, false})
	}
	return out
}

// cherryTrunk is CherryTrunkPlacer.
type cherryTrunk struct {
	trunkBase
	count, horizontal, end intProvider
	start, start2          intProvider
}

func (c cherryTrunk) placeTrunk(t *treeCtx, h int32, o BlockPos) []foliageAttachment {
	r := t.r
	placeBelowTrunk(t, offset(o, 0, -1, 0))
	i := max(0, h-1+c.start(r))
	j := max(0, h-1+c.start2(r))
	if j >= i {
		j++
	}
	n := c.count(r)
	three := n == 3
	two := n >= 2
	var k int32
	switch {
	case three:
		k = h
	case two:
		k = max(i, j) + 1
	default:
		k = i + 1
	}
	for y := int32(0); y < k; y++ {
		placeLog(c, t, offset(o, 0, y, 0), nil)
	}
	var out []foliageAttachment
	if three {
		out = append(out, foliageAttachment{offset(o, 0, k, 0), 0, false})
	}
	dir := randomHorizontal(r)
	f := axisSetter(axisOf(dir))
	out = append(out, c.branch(t, h, o, f, dir, i, i < k-1))
	if two {
		out = append(out, c.branch(t, h, o, f, oppositeDir(dir), j, j < k-1))
	}
	return out
}

func (c cherryTrunk) branch(t *treeCtx, h int32, o BlockPos, f func(world.Block) world.Block, dir int, start int32, forceLong bool) foliageAttachment {
	r := t.r
	m := offset(o, 0, start, 0)
	endY := h - 1 + c.end(r)
	long := forceLong || endY < start
	hl := c.horizontal(r)
	if long {
		hl++
	}
	end := offset(relDir(o, dir, hl), 0, endY, 0)
	n := int32(1)
	if long {
		n = 2
	}
	for k := int32(0); k < n; k++ {
		m = relDir(m, dir, 1)
		placeLog(c, t, m, f)
	}
	vdir := dirDown
	if end[1] > m[1] {
		vdir = dirUp
	}
	for {
		d := distManhattan(m, end)
		if d == 0 {
			break
		}
		p := float32(absI(end[1]-m[1])) / float32(d)
		vert := r.NextFloat() < p
		if vert {
			m = relDir(m, vdir, 1)
			placeLog(c, t, m, nil)
		} else {
			m = relDir(m, dir, 1)
			placeLog(c, t, m, f)
		}
	}
	return foliageAttachment{offset(end, 0, 1, 0), 0, false}
}
