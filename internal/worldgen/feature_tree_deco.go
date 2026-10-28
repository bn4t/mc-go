package worldgen

// Tree decorators: bee nests, ground cover, and vines on leaves and logs.

func init() {
	treeDecorators["minecraft:beehive"] = func(cfg any) treeDecorator {
		return beehiveDecorator{ffloat(ffield(cfg, "probability"), 0)}
	}
	treeDecorators["minecraft:place_on_ground"] = func(cfg any) treeDecorator {
		get := func(key string, def int32) int32 {
			if fhas(cfg, key) {
				return fint(ffield(cfg, key), def)
			}
			return def
		}
		return placeOnGround{get("tries", 128), get("radius", 2), get("height", 1), decodeStateProvider(ffield(cfg, "block_state_provider"))}
	}
	treeDecorators["minecraft:leave_vine"] = func(cfg any) treeDecorator {
		return leaveVine{ffloat(ffield(cfg, "probability"), 0)}
	}
	treeDecorators["minecraft:trunk_vine"] = func(any) treeDecorator { return trunkVine{} }
}

func shufflePoints(rand Random, l []Point) {
	for i := int32(len(l)); i > 1; i-- {
		j := rand.NextIntn(i)
		l[i-1], l[j] = l[j], l[i-1]
	}
}

func (t *tree) isAir(p Point) bool { return t.r.get(p).isAir() }

// placeVine sets a vine with one face through the decoration setter.
func (t *tree) placeVine(p Point, face string) {
	t.setDecoration(p, defaultState("vine").with(face, "true"))
}

type beehiveDecorator struct{ probability float32 }

// beeSpawnSides are the horizontal sides a nest may sit on, all but the
// one opposite its south facing.
var beeSpawnSides = []int{dirEast, dirSouth, dirWest}

func (b beehiveDecorator) place(t *tree) {
	leaves, logs := sortedByY(t.leaves), sortedByY(t.logs)
	if len(logs) == 0 {
		return
	}
	rand := t.rand
	if rand.NextFloat() >= b.probability {
		return
	}
	var y int32
	if len(leaves) > 0 {
		y = max32(leaves[0].Y-1, logs[0].Y+1)
	} else {
		y = min32(logs[0].Y+1+rand.NextIntn(3), logs[len(logs)-1].Y)
	}
	var cands []Point
	for _, l := range logs {
		if l.Y != y {
			continue
		}
		for _, d := range beeSpawnSides {
			cands = append(cands, l.offset(d))
		}
	}
	if len(cands) == 0 {
		return
	}
	shufflePoints(rand, cands)
	for _, p := range cands {
		if !t.isAir(p) || !t.isAir(p.offset(dirSouth)) {
			continue
		}
		t.setDecoration(p, defaultState("bee_nest").with("facing", "south"))
		be := &GenBlockEntity{ID: "minecraft:beehive"}
		n := 2 + rand.NextIntn(2)
		for i := int32(0); i < n; i++ {
			be.BeeTicks = append(be.BeeTicks, rand.NextIntn(599))
		}
		t.r.setBlockEntity(p, be)
		return
	}
}

// lowestTrunkOrRoots is TreeFeature.getLowestTrunkOrRootOfTree: the
// logs, or the roots, or both when they start at the same height.
func lowestTrunkOrRoots(t *tree) []Point {
	roots, logs := sortedByY(t.roots), sortedByY(t.logs)
	switch {
	case len(roots) == 0:
		return logs
	case len(logs) > 0 && roots[0].Y == logs[0].Y:
		return append(logs, roots...)
	}
	return roots
}

type placeOnGround struct {
	tries, radius, height int32
	provider              stateProvider
}

func (g placeOnGround) place(t *tree) {
	base := lowestTrunkOrRoots(t)
	if len(base) == 0 {
		return
	}
	first := base[0]
	y := first.Y
	minX, maxX, minZ, maxZ := first.X, first.X, first.Z, first.Z
	for _, p := range base {
		if p.Y == y {
			minX, maxX = min32(minX, p.X), max32(maxX, p.X)
			minZ, maxZ = min32(minZ, p.Z), max32(maxZ, p.Z)
		}
	}
	minX, maxX = minX-g.radius, maxX+g.radius
	minZ, maxZ = minZ-g.radius, maxZ+g.radius
	minY, maxY := y-g.height, y+g.height
	rand := t.rand
	for i := int32(0); i < g.tries; i++ {
		x := rand.NextIntn(maxX-minX+1) + minX
		py := rand.NextIntn(maxY-minY+1) + minY
		z := rand.NextIntn(maxZ-minZ+1) + minZ
		p := Point{x, py, z}
		above := p.offset(dirUp)
		if a := t.r.get(above); !(a.isAir() || a.is("vine")) {
			continue
		}
		if !t.r.get(p).isSolidRender() || t.r.heightAt(hmMotionBlockingNoLeaves, p.X, p.Z) > above.Y {
			continue
		}
		t.setDecoration(above, g.provider.get(t.r, rand, above))
	}
}

type leaveVine struct{ probability float32 }

// hangVine places a vine and lets it hang down through air up to four
// more blocks.
func (t *tree) hangVine(p Point, face string) {
	t.placeVine(p, face)
	p = p.offset(dirDown)
	for n := 4; t.isAir(p) && n > 0; n-- {
		t.placeVine(p, face)
		p = p.offset(dirDown)
	}
}

func (v leaveVine) place(t *tree) {
	sides := []struct {
		dir  int
		face string
	}{{dirWest, "east"}, {dirEast, "west"}, {dirNorth, "south"}, {dirSouth, "north"}}
	for _, l := range sortedByY(t.leaves) {
		for _, s := range sides {
			if t.rand.NextFloat() >= v.probability {
				continue
			}
			if p := l.offset(s.dir); t.isAir(p) {
				t.hangVine(p, s.face)
			}
		}
	}
}

type trunkVine struct{}

func (trunkVine) place(t *tree) {
	sides := []struct {
		dir  int
		face string
	}{{dirWest, "east"}, {dirEast, "west"}, {dirNorth, "south"}, {dirSouth, "north"}}
	for _, l := range sortedByY(t.logs) {
		for _, s := range sides {
			if t.rand.NextIntn(3) <= 0 {
				continue
			}
			if p := l.offset(s.dir); t.isAir(p) {
				t.placeVine(p, s.face)
			}
		}
	}
}

type alterGround struct{ provider stateProvider }

func (a alterGround) place(t *tree) {
	base := lowestTrunkOrRoots(t)
	if len(base) == 0 {
		return
	}
	y := base[0].Y
	for _, p := range base {
		if p.Y != y {
			continue
		}
		a.circle(t, Point{p.X - 1, p.Y, p.Z - 1})
		a.circle(t, Point{p.X + 2, p.Y, p.Z - 1})
		a.circle(t, Point{p.X - 1, p.Y, p.Z + 2})
		a.circle(t, Point{p.X + 2, p.Y, p.Z + 2})
		for i := 0; i < 5; i++ {
			j := t.rand.NextIntn(64)
			k, l := j%8, j/8
			if k == 0 || k == 7 || l == 0 || l == 7 {
				a.circle(t, Point{p.X - 3 + k, p.Y, p.Z - 3 + l})
			}
		}
	}
}

func (a alterGround) circle(t *tree, p Point) {
	for dx := int32(-2); dx <= 2; dx++ {
		for dz := int32(-2); dz <= 2; dz++ {
			if abs(dx) != 2 || abs(dz) != 2 {
				a.at(t, Point{p.X + dx, p.Y, p.Z + dz})
			}
		}
	}
}

// at replaces the first block from two above p down to three below that
// the provider has a state for, stopping at a solid block below p.
func (a alterGround) at(t *tree, p Point) {
	for dy := int32(2); dy >= -3; dy-- {
		q := Point{p.X, p.Y + dy, p.Z}
		if s, ok := a.provider.optional(t.r, t.rand, q); ok {
			t.setDecoration(q, s)
			return
		}
		if !t.isAir(q) && dy < 0 {
			return
		}
	}
}

type cocoaDecorator struct{ probability float32 }

func (c cocoaDecorator) place(t *tree) {
	rand := t.rand
	if rand.NextFloat() >= c.probability {
		return
	}
	logs := sortedByY(t.logs)
	if len(logs) == 0 {
		return
	}
	y := logs[0].Y
	for _, l := range logs {
		if l.Y-y > 2 {
			continue
		}
		for _, d := range horizontalDirs {
			if rand.NextFloat() > 0.25 {
				continue
			}
			o := d ^ 1
			q := Point{l.X + stepX(o), l.Y, l.Z + stepZ(o)}
			if t.isAir(q) {
				age := rand.NextIntn(3)
				t.setDecoration(q, defaultState("cocoa").with("age", itoa(age)).with("facing", facingNames[d]))
			}
		}
	}
}

type attachedToLeaves struct {
	probability       float32
	radiusXZ, radiusY int32
	provider          stateProvider
	requiredEmpty     int32
	directions        []int
}

func (a attachedToLeaves) place(t *tree) {
	rand := t.rand
	excluded := map[Point]bool{}
	leaves := sortedByY(t.leaves)
	shufflePoints(rand, leaves)
	for _, l := range leaves {
		d := a.directions[rand.NextIntn(int32(len(a.directions)))]
		q := l.offset(d)
		if excluded[q] {
			continue
		}
		if !(rand.NextFloat() < a.probability) || !a.hasRequiredEmpty(t, l, d) {
			continue
		}
		for x := q.X - a.radiusXZ; x <= q.X+a.radiusXZ; x++ {
			for y := q.Y - a.radiusY; y <= q.Y+a.radiusY; y++ {
				for z := q.Z - a.radiusXZ; z <= q.Z+a.radiusXZ; z++ {
					excluded[Point{x, y, z}] = true
				}
			}
		}
		t.setDecoration(q, a.provider.get(t.r, rand, q))
	}
}

func (a attachedToLeaves) hasRequiredEmpty(t *tree, p Point, d int) bool {
	for i := int32(1); i <= a.requiredEmpty; i++ {
		if !t.isAir(p.add(scaleDir(d, i))) {
			return false
		}
	}
	return true
}

type paleMoss struct{ leaves, trunk, ground float32 }

// addMossHanger hangs pale moss from p down through air, each block
// ending the strand with even odds, the last one a tip.
func (t *tree) addMossHanger(p Point) {
	for t.isAir(p.offset(dirDown)) && !(float64(t.rand.NextFloat()) < 0.5) {
		t.setDecoration(p, defaultState("pale_hanging_moss").with("tip", "false"))
		p = p.offset(dirDown)
	}
	t.setDecoration(p, defaultState("pale_hanging_moss").with("tip", "true"))
}

func (m paleMoss) place(t *tree) {
	rand := t.rand
	shuffled := sortedByY(t.logs)
	shufflePoints(rand, shuffled)
	if len(shuffled) == 0 {
		return
	}
	lowest := shuffled[0]
	for _, p := range shuffled[1:] {
		if p.Y < lowest.Y {
			lowest = p
		}
	}
	if rand.NextFloat() < m.ground {
		if cf := lookupConfigured("minecraft:pale_moss_patch"); cf != nil {
			cf.run(t.r, rand, lowest.offset(dirUp))
		}
	}
	for _, l := range sortedByY(t.logs) {
		if rand.NextFloat() < m.trunk {
			if b := l.offset(dirDown); t.isAir(b) {
				t.addMossHanger(b)
			}
		}
	}
	for _, l := range sortedByY(t.leaves) {
		if rand.NextFloat() < m.leaves {
			if b := l.offset(dirDown); t.isAir(b) {
				t.addMossHanger(b)
			}
		}
	}
}

type creakingHeart struct{ probability float32 }

func (c creakingHeart) place(t *tree) {
	logs := sortedByY(t.logs)
	if len(logs) == 0 {
		return
	}
	rand := t.rand
	if rand.NextFloat() >= c.probability {
		return
	}
	shufflePoints(rand, logs)
	for _, p := range logs {
		enclosed := true
		for d := 0; d < 6; d++ {
			if !t.r.get(p.offset(d)).inTag("logs") {
				enclosed = false
				break
			}
		}
		if enclosed {
			t.setDecoration(p, defaultState("creaking_heart").with("creaking_heart_state", "dormant").with("natural", "true"))
			return
		}
	}
}

func init() {
	treeDecorators["minecraft:alter_ground"] = func(cfg any) treeDecorator {
		return alterGround{decodeStateProvider(ffield(cfg, "provider"))}
	}
	treeDecorators["minecraft:cocoa"] = func(cfg any) treeDecorator { return cocoaDecorator{ffloat(ffield(cfg, "probability"), 0)} }
	treeDecorators["minecraft:attached_to_leaves"] = func(cfg any) treeDecorator {
		a := attachedToLeaves{probability: ffloat(ffield(cfg, "probability"), 0),
			radiusXZ: fint(ffield(cfg, "exclusion_radius_xz"), 0), radiusY: fint(ffield(cfg, "exclusion_radius_y"), 0),
			provider: decodeStateProvider(ffield(cfg, "block_provider")), requiredEmpty: fint(ffield(cfg, "required_empty_blocks"), 0)}
		for _, d := range flist(ffield(cfg, "directions")) {
			a.directions = append(a.directions, directionNames[fstr(d)])
		}
		return a
	}
	treeDecorators["minecraft:pale_moss"] = func(cfg any) treeDecorator {
		return paleMoss{ffloat(ffield(cfg, "leaves_probability"), 0), ffloat(ffield(cfg, "trunk_probability"), 0), ffloat(ffield(cfg, "ground_probability"), 0)}
	}
	treeDecorators["minecraft:creaking_heart"] = func(cfg any) treeDecorator {
		return creakingHeart{ffloat(ffield(cfg, "probability"), 0)}
	}
}
