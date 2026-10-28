package worldgen

// Nether features: glowstone clusters, twisting and weeping vines, the
// nylium forests' vegetation, blobs of one block replacing another,
// basalt pillars and columns, and lava deltas.

func init() {
	featureKinds["minecraft:glowstone_blob"] = func(any) func(*featureCtx) bool { return placeGlowstone }
	featureKinds["minecraft:twisting_vines"] = decodeTwistingVines
	featureKinds["minecraft:weeping_vines"] = func(any) func(*featureCtx) bool { return placeWeepingVines }
	featureKinds["minecraft:nether_forest_vegetation"] = decodeNetherVegetation
	featureKinds["minecraft:netherrack_replace_blobs"] = decodeReplaceBlobs
	featureKinds["minecraft:basalt_pillar"] = func(any) func(*featureCtx) bool { return placeBasaltPillar }
	featureKinds["minecraft:basalt_columns"] = decodeBasaltColumns
	featureKinds["minecraft:delta_feature"] = decodeDelta
}

func (r *decoRegion) maxY() int32 { return r.minY + r.height - 1 }

// placeGlowstone hangs a glowstone block from netherrack, basalt or
// blackstone above an air origin, then makes 1500 tries within 7 blocks
// sideways and 11 down to add glowstone to air touching exactly one
// glowstone block.
func placeGlowstone(fc *featureCtx) bool {
	r, o, rand := fc.r, fc.origin, fc.rand
	if !r.get(o).isAir() {
		return false
	}
	if up := r.get(o.offset(dirUp)); !up.is("netherrack") && !up.is("basalt") && !up.is("blackstone") {
		return false
	}
	glow := defaultState("glowstone")
	r.set(o, glow)
	for i := 0; i < 1500; i++ {
		dx := rand.NextIntn(8) - rand.NextIntn(8)
		dy := -rand.NextIntn(12)
		dz := rand.NextIntn(8) - rand.NextIntn(8)
		p := Point{o.X + dx, o.Y + dy, o.Z + dz}
		if !r.get(p).isAir() {
			continue
		}
		if touching(r, p, 1, func(s StateID) bool { return s.is("glowstone") }) == 1 {
			r.set(p, glow)
		}
	}
	return true
}

// touching counts p's neighbours, in direction order, that match, up to
// one more than limit.
func touching(r *decoRegion, p Point, limit int, match func(StateID) bool) int {
	n := 0
	for d := 0; d < 6 && n <= limit; d++ {
		if match(r.get(p.offset(d))) {
			n++
		}
	}
	return n
}

// vineLength is a nether vine column's length: one to max, doubled one
// time in six, then cut to one a time in five.
func vineLength(rand Random, max int32) int32 {
	n := mthNextInt(rand, 1, max)
	if rand.NextIntn(6) == 0 {
		n *= 2
	}
	if rand.NextIntn(5) == 0 {
		n = 1
	}
	return n
}

// vineColumn grows a nether vine column from p in direction dir: plant
// blocks through air, ending in a head of age 17–25 at the last block or
// where air ends. Twisting vines start counting at one, weeping at zero.
func vineColumn(r *decoRegion, rand Random, p Point, length, first int32, dir int, head, plant StateID) {
	for i := first; i <= length; i, p = i+1, p.offset(dir) {
		if !r.get(p).isAir() {
			continue
		}
		if i == length || !r.get(p.offset(dir)).isAir() {
			r.set(p, head.with("age", itoa(mthNextInt(rand, 17, 25))))
			return
		}
		r.set(p, plant)
	}
}

func twistingInvalid(r *decoRegion, p Point) bool {
	if !r.get(p).isAir() {
		return true
	}
	b := r.get(p.offset(dirDown))
	return !b.is("netherrack") && !b.is("warped_nylium") && !b.is("warped_wart_block")
}

func decodeTwistingVines(cfg any) func(*featureCtx) bool {
	w := fint(ffield(cfg, "spread_width"), 0)
	h := fint(ffield(cfg, "spread_height"), 0)
	max := fint(ffield(cfg, "max_height"), 0)
	head, plant := defaultState("twisting_vines"), defaultState("twisting_vines_plant")
	return func(fc *featureCtx) bool {
		r, o, rand := fc.r, fc.origin, fc.rand
		if twistingInvalid(r, o) {
			return false
		}
		for i := int32(0); i < w*w; i++ {
			dx := mthNextInt(rand, -w, w)
			dy := mthNextInt(rand, -h, h)
			dz := mthNextInt(rand, -w, w)
			p := Point{o.X + dx, o.Y + dy, o.Z + dz}
			// Down through air to the ground, then back up one.
			for {
				p = p.offset(dirDown)
				if r.outside(p.Y) {
					break
				}
				if !r.get(p).isAir() {
					break
				}
			}
			if r.outside(p.Y) {
				continue
			}
			p = p.offset(dirUp)
			if twistingInvalid(r, p) {
				continue
			}
			vineColumn(r, rand, p, vineLength(rand, max), 1, dirUp, head, plant)
		}
		return true
	}
}

func isNetherrackOrWart(s StateID) bool { return s.is("netherrack") || s.is("nether_wart_block") }

// placeWeepingVines grows a nether wart cluster on the ceiling above an
// air origin, then hangs weeping vines from netherrack or wart around it.
func placeWeepingVines(fc *featureCtx) bool {
	r, o, rand := fc.r, fc.origin, fc.rand
	if !r.get(o).isAir() || !isNetherrackOrWart(r.get(o.offset(dirUp))) {
		return false
	}
	wart := defaultState("nether_wart_block")
	r.set(o, wart)
	for i := 0; i < 200; i++ {
		dx := rand.NextIntn(6) - rand.NextIntn(6)
		dy := rand.NextIntn(2) - rand.NextIntn(5)
		dz := rand.NextIntn(6) - rand.NextIntn(6)
		p := Point{o.X + dx, o.Y + dy, o.Z + dz}
		if r.get(p).isAir() && touching(r, p, 1, isNetherrackOrWart) == 1 {
			r.set(p, wart)
		}
	}
	head, plant := defaultState("weeping_vines"), defaultState("weeping_vines_plant")
	for i := 0; i < 100; i++ {
		dx := rand.NextIntn(8) - rand.NextIntn(8)
		dy := rand.NextIntn(2) - rand.NextIntn(7)
		dz := rand.NextIntn(8) - rand.NextIntn(8)
		p := Point{o.X + dx, o.Y + dy, o.Z + dz}
		if !r.get(p).isAir() || !isNetherrackOrWart(r.get(p.offset(dirUp))) {
			continue
		}
		vineColumn(r, rand, p, vineLength(rand, 8), 0, dirDown, head, plant)
	}
	return true
}

// decodeNetherVegetation scatters the provider's plants over a nylium
// origin: width² tries within the spread, each drawing its state before
// testing for air and survival.
func decodeNetherVegetation(cfg any) func(*featureCtx) bool {
	provider := decodeStateProvider(ffield(cfg, "state_provider"))
	w := fint(ffield(cfg, "spread_width"), 0)
	h := fint(ffield(cfg, "spread_height"), 0)
	return func(fc *featureCtx) bool {
		r, o, rand := fc.r, fc.origin, fc.rand
		if !r.get(o.offset(dirDown)).inTag("nylium") {
			return false
		}
		if o.Y < r.minY+1 || o.Y+1 > r.maxY() {
			return false
		}
		placed := false
		for i := int32(0); i < w*w; i++ {
			dx := rand.NextIntn(w) - rand.NextIntn(w)
			dy := rand.NextIntn(h) - rand.NextIntn(h)
			dz := rand.NextIntn(w) - rand.NextIntn(w)
			p := Point{o.X + dx, o.Y + dy, o.Z + dz}
			s := provider.get(r, rand, p)
			if r.get(p).isAir() && p.Y > r.minY && canSurvive(r, s, p) {
				r.set(p, s)
				placed = true
			}
		}
		return placed
	}
}

// decodeReplaceBlobs finds the target block at or below the origin and
// replaces it within a blob of three sampled reaches, out to the
// largest of them by Manhattan distance.
func decodeReplaceBlobs(cfg any) func(*featureCtx) bool {
	target := blockStateOf(ffield(cfg, "target")).block()
	state := blockStateOf(ffield(cfg, "state"))
	radius := decodeIntProvider(ffield(cfg, "radius"))
	return func(fc *featureCtx) bool {
		r, rand := fc.r, fc.rand
		p := fc.origin
		p.Y = max32(r.minY+1, min32(p.Y, r.maxY()))
		for ; p.Y > r.minY+1 && !r.get(p).is(target); p.Y-- {
		}
		if p.Y <= r.minY+1 {
			return false
		}
		rx, ry, rz := radius.sample(rand), radius.sample(rand), radius.sample(rand)
		reach := max32(rx, max32(ry, rz))
		placed := false
		withinManhattan(p, rx, ry, rz, func(q Point) bool {
			if manhattan(q, p) > reach {
				return false
			}
			if r.get(q).is(target) {
				r.set(q, state)
				placed = true
			}
			return true
		})
		return placed
	}
}

func placeHangOff(r *decoRegion, rand Random, p Point) bool {
	if rand.NextIntn(10) != 0 {
		r.set(p, defaultState("basalt"))
		return true
	}
	return false
}

// placeBasaltPillar runs a basalt column down from an air origin under
// a solid block until it meets something, each side growing a hang-off
// until one fails, then scatters basalt at the pillar's foot.
func placeBasaltPillar(fc *featureCtx) bool {
	r, o, rand := fc.r, fc.origin, fc.rand
	if !r.get(o).isAir() || r.get(o.offset(dirUp)).isAir() {
		return false
	}
	basalt := defaultState("basalt")
	p := o
	sides := [4]int{dirNorth, dirSouth, dirWest, dirEast}
	grow := [4]bool{true, true, true, true}
	for r.get(p).isAir() {
		if r.outside(p.Y) {
			return true
		}
		r.set(p, basalt)
		for i, d := range sides {
			grow[i] = grow[i] && placeHangOff(r, rand, p.offset(d))
		}
		p = p.offset(dirDown)
	}
	p = p.offset(dirUp)
	for _, d := range sides {
		if rand.NextBool() {
			r.set(p.offset(d), basalt)
		}
	}
	p = p.offset(dirDown)
	for dx := int32(-3); dx < 4; dx++ {
		for dz := int32(-3); dz < 4; dz++ {
			if rand.NextIntn(10) >= 10-abs(dx)*abs(dz) {
				continue
			}
			q := Point{p.X + dx, p.Y, p.Z + dz}
			for n := 3; n > 0 && r.get(q.offset(dirDown)).isAir(); n-- {
				q = q.offset(dirDown)
			}
			if !r.get(q.offset(dirDown)).isAir() {
				r.set(q, basalt)
			}
		}
	}
	return true
}

var basaltCannotPlaceOn = map[string]bool{
	"lava": true, "bedrock": true, "magma_block": true, "soul_sand": true, "nether_bricks": true,
	"nether_brick_fence": true, "nether_brick_stairs": true, "nether_wart": true, "chest": true, "spawner": true,
}

type basaltColumns struct {
	r        *decoRegion
	seaLevel int32
}

func (b basaltColumns) airOrLavaOcean(p Point) bool {
	s := b.r.get(p)
	return s.isAir() || s.is("lava") && p.Y <= b.seaLevel
}

func (b basaltColumns) canPlaceAt(p Point) bool {
	if !b.airOrLavaOcean(p) {
		return false
	}
	below := b.r.get(p.offset(dirDown))
	return !below.isAir() && !basaltCannotPlaceOn[below.block()]
}

// surface walks down from p, at most n steps, to a spot basalt can
// stand on.
func (b basaltColumns) surface(p Point, n int32) (Point, bool) {
	for ; p.Y > b.r.minY+1 && n > 0; p = p.offset(dirDown) {
		n--
		if b.canPlaceAt(p) {
			return p, true
		}
	}
	return p, false
}

// air walks up from p, at most n steps, to air, failing on a block
// basalt never stands on.
func (b basaltColumns) air(p Point, n int32) (Point, bool) {
	for ; p.Y <= b.r.maxY() && n > 0; p = p.offset(dirUp) {
		n--
		s := b.r.get(p)
		if basaltCannotPlaceOn[s.block()] {
			return p, false
		}
		if s.isAir() {
			return p, true
		}
	}
	return p, false
}

// column raises basalt over a square of the given reach around p, each
// cell from its own start up to height minus half its distance.
func (b basaltColumns) column(p Point, height, reach int32) bool {
	placed := false
	basalt := defaultState("basalt")
	for z := p.Z - reach; z <= p.Z+reach; z++ {
		for x := p.X - reach; x <= p.X+reach; x++ {
			q := Point{x, p.Y, z}
			d := manhattan(q, p)
			var start Point
			var ok bool
			if b.airOrLavaOcean(q) {
				start, ok = b.surface(q, d)
			} else {
				start, ok = b.air(q, d)
			}
			if !ok {
				continue
			}
			m := start
			for n := height - d/2; n >= 0; n-- {
				if b.airOrLavaOcean(m) {
					b.r.set(m, basalt)
					placed = true
				} else if !b.r.get(m).is("basalt") {
					break
				}
				m = m.offset(dirUp)
			}
		}
	}
	return placed
}

func decodeBasaltColumns(cfg any) func(*featureCtx) bool {
	height := decodeIntProvider(ffield(cfg, "height"))
	reach := decodeIntProvider(ffield(cfg, "reach"))
	return func(fc *featureCtx) bool {
		r, o, rand := fc.r, fc.origin, fc.rand
		b := basaltColumns{r, r.seaLevel}
		if !b.canPlaceAt(o) {
			return false
		}
		h := height.sample(rand)
		dense := rand.NextFloat() < 0.9
		spread, tries := min32(h, 8), int32(15)
		if dense {
			spread, tries = min32(h, 5), 50
		}
		placed := false
		for i := int32(0); i < tries; i++ {
			x := o.X - spread + rand.NextIntn(2*spread+1)
			y := o.Y + rand.NextIntn(1)
			z := o.Z - spread + rand.NextIntn(2*spread+1)
			p := Point{x, y, z}
			if k := h - manhattan(p, o); k >= 0 {
				if b.column(p, k, reach.sample(rand)) {
					placed = true
				}
			}
		}
		return placed
	}
}

var deltaCannotReplace = map[string]bool{
	"bedrock": true, "nether_bricks": true, "nether_brick_fence": true, "nether_brick_stairs": true,
	"nether_wart": true, "chest": true, "spawner": true,
}

// deltaClear is a spot a delta may fill: not already its contents nor
// protected, open to the air above and closed on every other side.
func deltaClear(r *decoRegion, p Point, contents string) bool {
	s := r.get(p)
	if s.is(contents) || deltaCannotReplace[s.block()] {
		return false
	}
	for d := 0; d < 6; d++ {
		if r.get(p.offset(d)).isAir() != (d == dirUp) {
			return false
		}
	}
	return true
}

// decodeDelta fills a flat Manhattan disc with the contents, nine times
// in ten with a rim offset by two sampled amounts.
func decodeDelta(cfg any) func(*featureCtx) bool {
	contents := blockStateOf(ffield(cfg, "contents"))
	rim := blockStateOf(ffield(cfg, "rim"))
	rimSize := decodeIntProvider(ffield(cfg, "rim_size"))
	size := decodeIntProvider(ffield(cfg, "size"))
	return func(fc *featureCtx) bool {
		r, o, rand := fc.r, fc.origin, fc.rand
		withRim := rand.NextDouble() < 0.9
		var rx, rz int32
		if withRim {
			rx = rimSize.sample(rand)
			rz = rimSize.sample(rand)
		}
		withRim = withRim && rx != 0 && rz != 0
		sx, sz := size.sample(rand), size.sample(rand)
		reach := max32(sx, sz)
		placed := false
		withinManhattan(o, sx, 0, sz, func(p Point) bool {
			if manhattan(p, o) > reach {
				return false
			}
			if !deltaClear(r, p, contents.block()) {
				return true
			}
			if withRim {
				placed = true
				r.set(p, rim)
			}
			q := Point{p.X + rx, p.Y, p.Z + rz}
			if deltaClear(r, q, contents.block()) {
				placed = true
				r.set(q, contents)
			}
			return true
		})
		return placed
	}
}
