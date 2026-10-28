package worldgen

import "math"

// Ground features: disks, the freeze-and-snow pass and scattered ore.

func init() {
	featureKinds["minecraft:disk"] = decodeDisk
	featureKinds["minecraft:freeze_top_layer"] = func(any) func(*featureCtx) bool { return freezeTopLayer }
	featureKinds["minecraft:scattered_ore"] = decodeScatteredOre
	featureKinds["minecraft:underwater_magma"] = decodeUnderwaterMagma
}

// decodeDisk is a disk: inside a circle of the sampled radius, each
// column from halfHeight above the origin down to halfHeight below it
// takes the provider's state wherever the target predicate holds.
func decodeDisk(cfg any) func(*featureCtx) bool {
	provider := decodeStateProvider(ffield(cfg, "state_provider"))
	target := decodeBlockPredicate(ffield(cfg, "target"))
	radius := decodeIntProvider(ffield(cfg, "radius"))
	half := fint(ffield(cfg, "half_height"), 0)
	return func(fc *featureCtx) bool {
		r, o := fc.r, fc.origin
		top, bottom := o.Y+half, o.Y-half-1
		rad := radius.sample(fc.rand)
		placed := false
		for z := o.Z - rad; z <= o.Z+rad; z++ {
			for x := o.X - rad; x <= o.X+rad; x++ {
				dx, dz := x-o.X, z-o.Z
				if dx*dx+dz*dz > rad*rad {
					continue
				}
				// A run of placed blocks keeps going through columns
				// the provider has nothing for; a failed target ends it.
				for y := top; y > bottom; y-- {
					p := Point{x, y, z}
					if !target(r, p) {
						continue
					}
					if s, ok := provider.optional(r, fc.rand, p); ok {
						r.set(p, s)
						placed = true
					}
				}
			}
		}
		return placed
	}
}

// freezeTopLayer freezes and snows the chunk's top layer: each column's
// first free block above MOTION_BLOCKING may take snow, the block under
// it may freeze to ice, and a snowy-capable block under snow turns snowy.
func freezeTopLayer(fc *featureCtx) bool {
	r, o := fc.r, fc.origin
	for i := int32(0); i < 16; i++ {
		for j := int32(0); j < 16; j++ {
			x, z := o.X+i, o.Z+j
			y := r.heightAt(hmMotionBlocking, x, z)
			top, below := Point{x, y, z}, Point{x, y - 1, z}
			biome := r.biome(top)
			if r.shouldFreeze(biome, below) {
				r.set(below, stIce)
			}
			if r.shouldSnow(biome, top) {
				r.set(top, stSnowLayer)
				if s := r.get(below); s.hasProp("snowy") {
					r.set(below, s.with("snowy", "true"))
				}
			}
		}
	}
	return true
}

var (
	stIce       = InternState("minecraft:ice")
	stSnowLayer = InternState("minecraft:snow[layers=1]")
)

// warmEnoughToRain is the biome's temperature test (0.15 and up).
func (r *decoRegion) warmEnoughToRain(biome string, p Point) bool {
	return !ColdEnoughToSnow(biome, p.X, p.Y, p.Z, r.seaLevel)
}

// shouldFreeze is Biome.shouldFreeze without the edge requirement: a
// cold biome's water source block in build height (nothing is lit yet,
// so block light is 0).
func (r *decoRegion) shouldFreeze(biome string, p Point) bool {
	if r.warmEnoughToRain(biome, p) || r.outside(p.Y) {
		return false
	}
	s := r.get(p)
	return s.isWaterFluid() && s.isSourceFluid() && s.isLiquidBlock()
}

// shouldSnow is Biome.shouldSnow: snowy precipitation over an air or
// snow block in build height that snow can survive on.
func (r *decoRegion) shouldSnow(biome string, p Point) bool {
	if !biomeClimates[biome].precipitation || r.warmEnoughToRain(biome, p) || r.outside(p.Y) {
		return false
	}
	s := r.get(p)
	return (s.isAir() || s.is("snow")) && canSurvive(r, stSnowLayer, p)
}

// decodeScatteredOre scatters up to size single ore blocks around the
// origin, each axis offset by the rounded difference of two floats
// times min(i, 7); each block becomes the first target it may.
func decodeScatteredOre(cfg any) func(*featureCtx) bool {
	c := decodeOreConfig(cfg)
	return func(fc *featureCtx) bool {
		rand, o := fc.rand, fc.origin
		n := rand.NextIntn(c.size + 1)
		for i := int32(0); i < n; i++ {
			d := float32(min(i, 7))
			axis := func() int32 { return roundFloat((rand.NextFloat() - rand.NextFloat()) * d) }
			dx := axis()
			dy := axis()
			dz := axis()
			p := Point{o.X + dx, o.Y + dy, o.Z + dz}
			cur := fc.r.get(p)
			for _, t := range c.targets {
				if c.canPlace(fc, cur, t, p) {
					fc.r.set(p, t.state)
					break
				}
			}
		}
		return true
	}
}

// roundFloat is Java's Math.round(float).
func roundFloat(v float32) int32 { return int32(math.Floor(float64(v) + 0.5)) }

// columnFloor is Column.scan's floor: from p (which must satisfy
// inside), up to maxDist-1 steps down while inside holds, then the Y
// reached if edge holds there.
func columnFloor(r *decoRegion, p Point, maxDist int32, inside, edge func(StateID) bool) (int32, bool) {
	if !inside(r.get(p)) {
		return 0, false
	}
	q := p
	for i := int32(1); i < maxDist && inside(r.get(q)); i++ {
		q.Y--
	}
	if edge(r.get(q)) {
		return q.Y, true
	}
	return 0, false
}

// decodeUnderwaterMagma scatters magma through the cube around a water
// column's floor: each position, x fastest then y then z, passes a
// chance roll, then must be neither water nor air with every face it
// shows (below's top, the four sides' inner faces) fully occluded.
func decodeUnderwaterMagma(cfg any) func(*featureCtx) bool {
	search := fint(ffield(cfg, "floor_search_range"), 0)
	rad := fint(ffield(cfg, "placement_radius_around_floor"), 0)
	chance := ffloat(ffield(cfg, "placement_probability_per_valid_position"), 0)
	water := func(s StateID) bool { return s.is("water") }
	magma := defaultState("magma_block")
	hidden := func(r *decoRegion, p Point, face int) bool { return r.get(p).faceOccludes(face) }
	return func(fc *featureCtx) bool {
		r, o := fc.r, fc.origin
		floor, ok := columnFloor(r, o, search, water, func(s StateID) bool { return !water(s) })
		if !ok {
			return false
		}
		placed := 0
		for z := o.Z - rad; z <= o.Z+rad; z++ {
			for y := floor - rad; y <= floor+rad; y++ {
				for x := o.X - rad; x <= o.X+rad; x++ {
					if fc.rand.NextFloat() >= chance {
						continue
					}
					p := Point{x, y, z}
					if s := r.get(p); water(s) || s.isAir() || !hidden(r, p.offset(dirDown), dirUp) {
						continue
					}
					valid := true
					for _, d := range []int{dirNorth, dirEast, dirSouth, dirWest} {
						if !hidden(r, p.offset(d), d^1) {
							valid = false
							break
						}
					}
					if valid {
						r.set(p, magma)
						placed++
					}
				}
			}
		}
		return placed > 0
	}
}

func init() {
	featureKinds["minecraft:blue_ice"] = func(any) func(*featureCtx) bool { return placeBlueIce }
	featureKinds["minecraft:block_blob"] = decodeBlockBlob
}

// placeBlueIce grows blue ice from a water spot below sea level next to
// packed ice: 200 tries around it, mostly below, each turning air,
// water or ice that touches blue ice into blue ice.
func placeBlueIce(fc *featureCtx) bool {
	r, o, rand := fc.r, fc.origin, fc.rand
	if o.Y > r.seaLevel-1 || !r.get(o).is("water") && !r.get(o.offset(dirDown)).is("water") {
		return false
	}
	nextToPacked := false
	for d := 0; d < 6 && !nextToPacked; d++ {
		nextToPacked = d != dirDown && r.get(o.offset(d)).is("packed_ice")
	}
	if !nextToPacked {
		return false
	}
	blue := defaultState("blue_ice")
	r.set(o, blue)
	for i := 0; i < 200; i++ {
		dy := rand.NextIntn(5) - rand.NextIntn(6)
		spread := int32(3)
		if dy < 2 {
			spread += dy / 2
		}
		if spread < 1 {
			continue
		}
		dx := rand.NextIntn(spread) - rand.NextIntn(spread)
		dz := rand.NextIntn(spread) - rand.NextIntn(spread)
		p := Point{o.X + dx, o.Y + dy, o.Z + dz}
		if s := r.get(p); !s.isAir() && !s.is("water") && !s.is("packed_ice") && !s.is("ice") {
			continue
		}
		for d := 0; d < 6; d++ {
			if r.get(p.offset(d)).is("blue_ice") {
				r.set(p, blue)
				break
			}
		}
	}
	return true
}

// decodeBlockBlob drops to the first spot standing on an allowed block,
// then places three small balls of the state, each beside and below
// the last.
func decodeBlockBlob(cfg any) func(*featureCtx) bool {
	state := blockStateOf(ffield(cfg, "state"))
	canPlaceOn := decodeBlockPredicate(ffield(cfg, "can_place_on"))
	return func(fc *featureCtx) bool {
		r, o, rand := fc.r, fc.origin, fc.rand
		for o.Y > r.minY+3 && !canPlaceOn(r, o.offset(dirDown)) {
			o = o.offset(dirDown)
		}
		if o.Y <= r.minY+3 {
			return false
		}
		for i := 0; i < 3; i++ {
			a, b, c := rand.NextIntn(2), rand.NextIntn(2), rand.NextIntn(2)
			f := float32(float32(a+b+c)*0.333) + 0.5
			lim := float64(f * f)
			for z := o.Z - c; z <= o.Z+c; z++ {
				for y := o.Y - b; y <= o.Y+b; y++ {
					for x := o.X - a; x <= o.X+a; x++ {
						dx, dy, dz := float64(x-o.X), float64(y-o.Y), float64(z-o.Z)
						if dx*dx+dy*dy+dz*dz <= lim {
							r.set(Point{x, y, z}, state)
						}
					}
				}
			}
			o = Point{o.X - 1 + rand.NextIntn(2), o.Y - rand.NextIntn(2), o.Z - 1 + rand.NextIntn(2)}
		}
		return true
	}
}

func init() {
	featureKinds["minecraft:spike"] = decodeIceSpike
}

// decodeIceSpike drops to the ground, then builds a tapering spike up
// (and as deep again below) with ragged edges, a rare tall one floating
// higher, and roots of the state reaching down to Y 50.
func decodeIceSpike(cfg any) func(*featureCtx) bool {
	state := blockStateOf(ffield(cfg, "state"))
	canPlaceOn := decodeBlockPredicate(ffield(cfg, "can_place_on"))
	canReplace := decodeBlockPredicate(ffield(cfg, "can_replace"))
	return func(fc *featureCtx) bool {
		r, o, rand := fc.r, fc.origin, fc.rand
		for r.get(o).isAir() && o.Y > r.minY+2 {
			o = o.offset(dirDown)
		}
		if !canPlaceOn(r, o) {
			return false
		}
		o.Y += rand.NextIntn(4)
		height := rand.NextIntn(4) + 7
		width := height/4 + rand.NextIntn(2)
		if width > 1 && rand.NextIntn(60) == 0 {
			o.Y += 10 + rand.NextIntn(30)
		}
		put := func(p Point) {
			if r.get(p).isAir() || canReplace(r, p) {
				r.set(p, state)
			}
		}
		for i := int32(0); i < height; i++ {
			f := (1 - float32(i)/float32(height)) * float32(width)
			k := int32(math.Ceil(float64(f)))
			for dx := -k; dx <= k; dx++ {
				fx := float32(abs(dx)) - 0.25
				for dz := -k; dz <= k; dz++ {
					fz := float32(abs(dz)) - 0.25
					if (dx != 0 || dz != 0) && float32(fx*fx)+float32(fz*fz) > float32(f*f) {
						continue
					}
					if (dx == -k || dx == k || dz == -k || dz == k) && rand.NextFloat() > 0.75 {
						continue
					}
					put(Point{o.X + dx, o.Y + i, o.Z + dz})
					if i != 0 && k > 1 {
						put(Point{o.X + dx, o.Y - i, o.Z + dz})
					}
				}
			}
		}
		base := min32(max32(width-1, 0), 1)
		for dx := -base; dx <= base; dx++ {
			for dz := -base; dz <= base; dz++ {
				p := Point{o.X + dx, o.Y - 1, o.Z + dz}
				n := int32(50)
				if abs(dx) == 1 && abs(dz) == 1 {
					n = rand.NextIntn(5)
				}
				for p.Y > 50 {
					if s := r.get(p); !s.isAir() && !canReplace(r, p) && s != state {
						break
					}
					r.set(p, state)
					p = p.offset(dirDown)
					if n--; n <= 0 {
						p.Y -= rand.NextIntn(5) + 1
						n = rand.NextIntn(5)
					}
				}
			}
		}
		return true
	}
}
