package worldgen

import "math"

// Trunk placers beyond the straight one: forking (acacia), two-by-two
// giants and mega jungle trunks, dark oak trunks with roots, fancy oak
// limbs, bending and upward-branching trunks, and cherry branches.

// randomHorizontal is Direction.Plane.HORIZONTAL.getRandomDirection.
func randomHorizontal(rand Random) int { return horizontalDirs[rand.NextIntn(4)] }

func stepX(d int) int32 { return dirOffsets[d][0] }
func stepZ(d int) int32 { return dirOffsets[d][2] }

func axisOf(d int) string { return [3]string{"y", "z", "x"}[d>>1] }

// withAxis is trySetValue(AXIS, axis).
func withAxis(axis string) func(StateID) StateID {
	return func(s StateID) StateID {
		if s.hasProp("axis") {
			return s.with("axis", axis)
		}
		return s
	}
}

type forkingTrunk struct{ trunkHeights }

func (f forkingTrunk) placeTrunk(t *tree, height int32, o Point) []foliageAttachment {
	t.placeBelowTrunk(o.offset(dirDown))
	rand := t.rand
	var out []foliageAttachment
	dir := randomHorizontal(rand)
	bend := height - rand.NextIntn(4) - 1
	steps := 3 - rand.NextIntn(3)
	x, z := o.X, o.Z
	top, ok := int32(0), false
	for i := int32(0); i < height; i++ {
		y := o.Y + i
		if i >= bend && steps > 0 {
			x, z = x+stepX(dir), z+stepZ(dir)
			steps--
		}
		if t.placeLog(Point{x, y, z}, nil) {
			top, ok = y+1, true
		}
	}
	if ok {
		out = append(out, foliageAttachment{pos: Point{x, top, z}, radiusOffset: 1})
	}
	x, z = o.X, o.Z
	dir2 := randomHorizontal(rand)
	if dir2 != dir {
		start := bend - rand.NextIntn(2) - 1
		n := 1 + rand.NextIntn(3)
		ok = false
		for i := start; i < height && n > 0; i, n = i+1, n-1 {
			if i < 1 {
				continue
			}
			y := o.Y + i
			x, z = x+stepX(dir2), z+stepZ(dir2)
			if t.placeLog(Point{x, y, z}, nil) {
				top, ok = y+1, true
			}
		}
		if ok {
			out = append(out, foliageAttachment{pos: Point{x, top, z}})
		}
	}
	return out
}

type giantTrunk struct{ trunkHeights }

func (t *tree) placeLogIfFree(p Point) {
	if t.isFree(p) {
		t.placeLog(p, nil)
	}
}

func placeGiantTrunk(t *tree, height int32, o Point) []foliageAttachment {
	b := o.offset(dirDown)
	t.placeBelowTrunk(b)
	t.placeBelowTrunk(b.offset(dirEast))
	t.placeBelowTrunk(b.offset(dirSouth))
	t.placeBelowTrunk(b.offset(dirSouth).offset(dirEast))
	for i := int32(0); i < height; i++ {
		t.placeLogIfFree(Point{o.X, o.Y + i, o.Z})
		if i < height-1 {
			t.placeLogIfFree(Point{o.X + 1, o.Y + i, o.Z})
			t.placeLogIfFree(Point{o.X + 1, o.Y + i, o.Z + 1})
			t.placeLogIfFree(Point{o.X, o.Y + i, o.Z + 1})
		}
	}
	return []foliageAttachment{{pos: Point{o.X, o.Y + height, o.Z}, doubleTrunk: true}}
}

func (g giantTrunk) placeTrunk(t *tree, height int32, o Point) []foliageAttachment {
	return placeGiantTrunk(t, height, o)
}

type megaJungleTrunk struct{ trunkHeights }

func (m megaJungleTrunk) placeTrunk(t *tree, height int32, o Point) []foliageAttachment {
	out := placeGiantTrunk(t, height, o)
	rand := t.rand
	for i := height - 2 - rand.NextIntn(4); i > height/2; i -= 2 + rand.NextIntn(4) {
		angle := rand.NextFloat() * 6.2831855
		var x, z int32
		for j := int32(0); j < 5; j++ {
			x = int32(1.5 + mthCos(float64(angle))*float32(j))
			z = int32(1.5 + mthSin(float64(angle))*float32(j))
			t.placeLog(o.add(Point{x, i - 3 + j/2, z}), nil)
		}
		out = append(out, foliageAttachment{pos: o.add(Point{x, i, z}), radiusOffset: -2})
	}
	return out
}

type darkOakTrunk struct{ trunkHeights }

// isAirOrLeaves is TreeFeature.isAirOrLeaves.
func isAirOrLeaves(r *decoRegion, p Point) bool {
	s := r.get(p)
	return s.isAir() || s.inTag("leaves")
}

func (d darkOakTrunk) placeTrunk(t *tree, height int32, o Point) []foliageAttachment {
	b := o.offset(dirDown)
	t.placeBelowTrunk(b)
	t.placeBelowTrunk(b.offset(dirEast))
	t.placeBelowTrunk(b.offset(dirSouth))
	t.placeBelowTrunk(b.offset(dirSouth).offset(dirEast))
	rand := t.rand
	dir := randomHorizontal(rand)
	bend := height - rand.NextIntn(4)
	steps := 2 - rand.NextIntn(3)
	x, z := o.X, o.Z
	top := o.Y + height - 1
	for i := int32(0); i < height; i++ {
		if i >= bend && steps > 0 {
			x, z = x+stepX(dir), z+stepZ(dir)
			steps--
		}
		p := Point{x, o.Y + i, z}
		if isAirOrLeaves(t.r, p) {
			t.placeLog(p, nil)
			t.placeLog(p.offset(dirEast), nil)
			t.placeLog(p.offset(dirSouth), nil)
			t.placeLog(p.offset(dirEast).offset(dirSouth), nil)
		}
	}
	out := []foliageAttachment{{pos: Point{x, top, z}, doubleTrunk: true}}
	for dx := int32(-1); dx <= 2; dx++ {
		for dz := int32(-1); dz <= 2; dz++ {
			if dx >= 0 && dx <= 1 && dz >= 0 && dz <= 1 {
				continue
			}
			if rand.NextIntn(3) > 0 {
				continue
			}
			n := rand.NextIntn(3) + 2
			for j := int32(0); j < n; j++ {
				t.placeLog(Point{o.X + dx, top - j - 1, o.Z + dz}, nil)
			}
			out = append(out, foliageAttachment{pos: Point{o.X + dx, top, o.Z + dz}})
		}
	}
	return out
}

type fancyTrunk struct{ trunkHeights }

// mthFloorF is Mth.floor of a float.
func mthFloorF(v float32) int32 {
	i := int32(v)
	if v < float32(i) {
		return i - 1
	}
	return i
}

func mthFloorD(v float64) int32 { return int32(math.Floor(v)) }

func fancyTreeShape(height, y int32) float32 {
	if float32(y) < float32(height)*0.3 {
		return -1
	}
	f := float32(height) / 2
	g := f - float32(y)
	h := float32(math.Sqrt(float64(f*f - g*g)))
	if g == 0 {
		h = f
	} else if float32(math.Abs(float64(g))) >= f {
		return 0
	}
	return h * 0.5
}

// fancyLimb is FancyTrunkPlacer.makeLimb: the straight run of blocks from
// start to end, placed as logs along it (place) or only checked free.
func (t *tree) fancyLimb(start, end Point, place bool) bool {
	if !place && start == end {
		return true
	}
	d := Point{end.X - start.X, end.Y - start.Y, end.Z - start.Z}
	steps := max32(abs(d.X), max32(abs(d.Y), abs(d.Z)))
	var fx, fy, fz float32
	if steps > 0 {
		fx, fy, fz = float32(d.X)/float32(steps), float32(d.Y)/float32(steps), float32(d.Z)/float32(steps)
	}
	for i := int32(0); i <= steps; i++ {
		p := start.add(Point{mthFloorF(0.5 + float32(i)*fx), mthFloorF(0.5 + float32(i)*fy), mthFloorF(0.5 + float32(i)*fz)})
		if place {
			ax, az := abs(p.X-start.X), abs(p.Z-start.Z)
			axis := "y"
			if m := max32(ax, az); m > 0 {
				if ax == m {
					axis = "x"
				} else {
					axis = "z"
				}
			}
			t.placeLog(p, withAxis(axis))
		} else if !t.isFree(p) {
			return false
		}
	}
	return true
}

func (f fancyTrunk) placeTrunk(t *tree, height int32, o Point) []foliageAttachment {
	rand := t.rand
	j := height + 2
	k := mthFloorD(float64(j) * 0.618)
	t.placeBelowTrunk(o.offset(dirDown))
	clusters := min32(1, mthFloorD(1.382+math.Pow(1.0*float64(j)/13.0, 2.0)))
	m := o.Y + k
	type coords struct {
		pos  Point
		base int32
	}
	list := []coords{{Point{o.X, o.Y + j - 5, o.Z}, m}}
	for n := j - 5; n >= 0; n-- {
		shape := fancyTreeShape(j, n)
		if shape < 0 {
			continue
		}
		for c := int32(0); c < clusters; c++ {
			e := 1.0 * float64(shape) * (float64(rand.NextFloat()) + 0.328)
			g := float64(rand.NextFloat()*2) * math.Pi
			h := float64(e*jsin(g)) + 0.5
			p := float64(e*jcos(g)) + 0.5
			bp := o.add(Point{mthFloorD(h), n - 1, mthFloorD(p)})
			if !t.fancyLimb(bp, bp.add(Point{0, 5, 0}), false) {
				continue
			}
			q, r := o.X-bp.X, o.Z-bp.Z
			s := float64(bp.Y) - math.Sqrt(float64(q*q+r*r))*0.381
			ty := m
			if !(s > float64(m)) {
				ty = int32(s)
			}
			b3 := Point{o.X, ty, o.Z}
			if t.fancyLimb(b3, bp, false) {
				list = append(list, coords{bp, b3.Y})
			}
		}
	}
	t.fancyLimb(o, Point{o.X, o.Y + k, o.Z}, true)
	trim := func(rel int32) bool { return float64(rel) >= float64(j)*0.2 }
	for _, c := range list {
		b := Point{o.X, c.base, o.Z}
		if b != c.pos && trim(c.base-o.Y) {
			t.fancyLimb(b, c.pos, true)
		}
	}
	var out []foliageAttachment
	for _, c := range list {
		if trim(c.base - o.Y) {
			out = append(out, foliageAttachment{pos: c.pos})
		}
	}
	return out
}

type bendingTrunk struct {
	trunkHeights
	minHeightForLeaves int32
	bendLength         intProvider
}

func (b bendingTrunk) placeTrunk(t *tree, height int32, o Point) []foliageAttachment {
	rand := t.rand
	dir := randomHorizontal(rand)
	top := height - 1
	m := o
	t.placeBelowTrunk(m.offset(dirDown))
	var out []foliageAttachment
	for j := int32(0); j <= top; j++ {
		if j+1 >= top+rand.NextIntn(2) {
			m = m.offset(dir)
		}
		if validTreePos(t.r, m) {
			t.placeLog(m, nil)
		}
		if j >= b.minHeightForLeaves {
			out = append(out, foliageAttachment{pos: m})
		}
		m = m.offset(dirUp)
	}
	n := b.bendLength.sample(rand)
	for j := int32(0); j <= n; j++ {
		if validTreePos(t.r, m) {
			t.placeLog(m, nil)
		}
		out = append(out, foliageAttachment{pos: m})
		m = m.offset(dir)
	}
	return out
}

type upwardsBranchingTrunk struct {
	trunkHeights
	extraSteps   intProvider
	branchChance float32
	extraLength  intProvider
	through      map[string]bool
}

func (u upwardsBranchingTrunk) growsThrough(s StateID) bool { return u.through[s.block()] }

func (u upwardsBranchingTrunk) placeTrunk(t *tree, height int32, o Point) []foliageAttachment {
	rand := t.rand
	var out []foliageAttachment
	for i := int32(0); i < height; i++ {
		y := o.Y + i
		if t.placeLog(Point{o.X, y, o.Z}, nil) && i < height-1 && rand.NextFloat() < u.branchChance {
			dir := randomHorizontal(rand)
			length := u.extraLength.sample(rand)
			start := max32(0, length-u.extraLength.sample(rand)-1)
			steps := u.extraSteps.sample(rand)
			out = u.placeBranch(t, height, out, Point{o.X, y, o.Z}, y, dir, start, steps)
		}
		if i == height-1 {
			out = append(out, foliageAttachment{pos: Point{o.X, y + 1, o.Z}})
		}
	}
	return out
}

func (u upwardsBranchingTrunk) placeBranch(t *tree, height int32, out []foliageAttachment, m Point, y int32, dir int, start, steps int32) []foliageAttachment {
	top := y + start
	x, z := m.X, m.Z
	for i := start; i < height && steps > 0; i, steps = i+1, steps-1 {
		if i < 1 {
			continue
		}
		yy := y + i
		x, z = x+stepX(dir), z+stepZ(dir)
		top = yy
		p := Point{x, yy, z}
		if t.placeLog(p, nil) {
			top++
		}
		out = append(out, foliageAttachment{pos: p})
	}
	if top-y > 1 {
		p := Point{x, top, z}
		out = append(out, foliageAttachment{pos: p}, foliageAttachment{pos: Point{x, top - 2, z}})
	}
	return out
}

type cherryTrunk struct {
	trunkHeights
	branchCount, horizontalLength, endOffset intProvider
	startOffset, secondStartOffset           uniformInt
}

func (c cherryTrunk) placeTrunk(t *tree, height int32, o Point) []foliageAttachment {
	rand := t.rand
	t.placeBelowTrunk(o.offset(dirDown))
	first := max32(0, height-1+c.startOffset.sample(rand))
	second := max32(0, height-1+c.secondStartOffset.sample(rand))
	if second >= first {
		second++
	}
	count := c.branchCount.sample(rand)
	three, two := count == 3, count >= 2
	trunk := first + 1
	switch {
	case three:
		trunk = height
	case two:
		trunk = max32(first, second) + 1
	}
	for i := int32(0); i < trunk; i++ {
		t.placeLog(Point{o.X, o.Y + i, o.Z}, nil)
	}
	var out []foliageAttachment
	if three {
		out = append(out, foliageAttachment{pos: Point{o.X, o.Y + trunk, o.Z}})
	}
	dir := randomHorizontal(rand)
	axis := withAxis(axisOf(dir))
	out = append(out, c.branch(t, height, o, axis, dir, first, first < trunk-1))
	if two {
		out = append(out, c.branch(t, height, o, axis, dir^1, second, second < trunk-1))
	}
	return out
}

func (c cherryTrunk) branch(t *tree, height int32, o Point, axis func(StateID) StateID, dir int, start int32, continues bool) foliageAttachment {
	rand := t.rand
	m := Point{o.X, o.Y + start, o.Z}
	end := height - 1 + c.endOffset.sample(rand)
	longer := continues || end < start
	n := c.horizontalLength.sample(rand)
	if longer {
		n++
	}
	target := o.add(scaleDir(dir, n)).add(Point{0, end, 0})
	steps := 1
	if longer {
		steps = 2
	}
	for i := 0; i < steps; i++ {
		m = m.offset(dir)
		t.placeLog(m, axis)
	}
	vdir := dirDown
	if target.Y > m.Y {
		vdir = dirUp
	}
	for {
		d := manhattan(m, target)
		if d == 0 {
			break
		}
		f := float32(abs(target.Y-m.Y)) / float32(d)
		vertical := rand.NextFloat() < f
		if vertical {
			m = m.offset(vdir)
			t.placeLog(m, nil)
		} else {
			m = m.offset(dir)
			t.placeLog(m, axis)
		}
	}
	return foliageAttachment{pos: target.offset(dirUp)}
}

func init() {
	h := decodeTrunkHeights
	treeTrunkPlacers["minecraft:forking_trunk_placer"] = func(cfg any) trunkPlacer { return forkingTrunk{h(cfg)} }
	treeTrunkPlacers["minecraft:giant_trunk_placer"] = func(cfg any) trunkPlacer { return giantTrunk{h(cfg)} }
	treeTrunkPlacers["minecraft:mega_jungle_trunk_placer"] = func(cfg any) trunkPlacer { return megaJungleTrunk{h(cfg)} }
	treeTrunkPlacers["minecraft:dark_oak_trunk_placer"] = func(cfg any) trunkPlacer { return darkOakTrunk{h(cfg)} }
	treeTrunkPlacers["minecraft:fancy_trunk_placer"] = func(cfg any) trunkPlacer { return fancyTrunk{h(cfg)} }
	treeTrunkPlacers["minecraft:bending_trunk_placer"] = func(cfg any) trunkPlacer {
		min := int32(1)
		if fhas(cfg, "min_height_for_leaves") {
			min = fint(ffield(cfg, "min_height_for_leaves"), 1)
		}
		return bendingTrunk{h(cfg), min, decodeIntProvider(ffield(cfg, "bend_length"))}
	}
	treeTrunkPlacers["minecraft:upwards_branching_trunk_placer"] = func(cfg any) trunkPlacer {
		return upwardsBranchingTrunk{h(cfg), decodeIntProvider(ffield(cfg, "extra_branch_steps")),
			ffloat(ffield(cfg, "place_branch_per_log_probability"), 0), decodeIntProvider(ffield(cfg, "extra_branch_length")),
			blockSet(ffield(cfg, "can_grow_through"))}
	}
	treeTrunkPlacers["minecraft:cherry_trunk_placer"] = func(cfg any) trunkPlacer {
		// The branch start is a uniform range written without a type.
		v := ffield(cfg, "branch_start_offset_from_top")
		start := uniformInt{fint(ffield(v, "min_inclusive"), 0), fint(ffield(v, "max_inclusive"), 0)}
		return cherryTrunk{h(cfg), decodeIntProvider(ffield(cfg, "branch_count")), decodeIntProvider(ffield(cfg, "branch_horizontal_length")),
			decodeIntProvider(ffield(cfg, "branch_end_offset_from_top")), start, uniformInt{start.lo, start.hi - 1}}
	}
}
