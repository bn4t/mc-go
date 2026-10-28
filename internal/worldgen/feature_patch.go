package worldgen

// Vegetation patches: a rounded square of ground blocks laid into the
// floor or ceiling around the origin, then a nested feature on some of
// them; the waterlogged kind turns enclosed ground into water.

func init() {
	featureKinds["minecraft:vegetation_patch"] = func(cfg any) func(*featureCtx) bool { return decodeVegetationPatch(cfg, false) }
	featureKinds["minecraft:waterlogged_vegetation_patch"] = func(cfg any) func(*featureCtx) bool { return decodeVegetationPatch(cfg, true) }
}

type vegetationPatch struct {
	replaceable                   map[string]bool
	ground                        stateProvider
	vegetation                    *placedFeature
	dir                           int // toward the surface: down for a floor
	depth, xzRadius               intProvider
	verticalRange                 int32
	extraBottom, vegChance, extra float32
	waterlogged                   bool
}

func decodeVegetationPatch(cfg any, waterlogged bool) func(*featureCtx) bool {
	v := &vegetationPatch{
		replaceable:   blockSet(ffield(cfg, "replaceable")),
		ground:        decodeStateProvider(ffield(cfg, "ground_state")),
		vegetation:    decodePlaced(ffield(cfg, "vegetation_feature")),
		dir:           dirDown,
		depth:         decodeIntProvider(ffield(cfg, "depth")),
		xzRadius:      decodeIntProvider(ffield(cfg, "xz_radius")),
		verticalRange: fint(ffield(cfg, "vertical_range"), 0),
		extraBottom:   ffloat(ffield(cfg, "extra_bottom_block_chance"), 0),
		vegChance:     ffloat(ffield(cfg, "vegetation_chance"), 0),
		extra:         ffloat(ffield(cfg, "extra_edge_column_chance"), 0),
		waterlogged:   waterlogged,
	}
	if fstr(ffield(cfg, "surface")) == "ceiling" {
		v.dir = dirUp
	}
	return v.place
}

func (v *vegetationPatch) place(fc *featureCtx) bool {
	rand := fc.rand
	xr := v.xzRadius.sample(rand) + 1
	zr := v.xzRadius.sample(rand) + 1
	ground := v.groundPatch(fc, xr, zr)
	ground.each(func(p Point) {
		if v.vegChance > 0 && rand.NextFloat() < v.vegChance {
			v.placeVegetation(fc, p)
		}
	})
	return ground.size > 0
}

// groundPatch lays ground in each column of the patch: corners never,
// the rest of the edge by chance. A column finds the surface within
// the vertical range and needs air on a sturdy face there.
func (v *vegetationPatch) groundPatch(fc *featureCtx, xr, zr int32) *posSet {
	r, o, rand := fc.r, fc.origin, fc.rand
	set := &posSet{}
	for dx := -xr; dx <= xr; dx++ {
		edgeX := dx == -xr || dx == xr
		for dz := -zr; dz <= zr; dz++ {
			edgeZ := dz == -zr || dz == zr
			if edgeX && edgeZ {
				continue
			}
			if (edgeX || edgeZ) && (v.extra == 0 || rand.NextFloat() > v.extra) {
				continue
			}
			m := Point{o.X + dx, o.Y, o.Z + dz}
			for i := int32(0); r.get(m).isAir() && i < v.verticalRange; i++ {
				m = m.offset(v.dir)
			}
			for i := int32(0); !r.get(m).isAir() && i < v.verticalRange; i++ {
				m = m.offset(v.dir ^ 1)
			}
			surface := m.offset(v.dir)
			if !r.get(m).isAir() || !r.get(surface).sturdy(v.dir^1) {
				continue
			}
			depth := v.depth.sample(rand)
			if v.extraBottom > 0 && rand.NextFloat() < v.extraBottom {
				depth++
			}
			if v.placeGround(fc, surface, depth) {
				set.add(surface)
			}
		}
	}
	if !v.waterlogged {
		return set
	}
	// Ground open to the side or below stays dry.
	water := &posSet{}
	set.each(func(p Point) {
		for _, d := range []int{dirNorth, dirEast, dirSouth, dirWest, dirDown} {
			if !r.get(p.offset(d)).sturdy(d ^ 1) {
				return
			}
		}
		water.add(p)
	})
	waterState := defaultState("water")
	water.each(func(p Point) { r.set(p, waterState) })
	return water
}

// placeGround sets up to depth ground blocks into the surface from p,
// stopping at a block it may not replace (failing only when nothing was
// set). A block already of the ground's kind is drawn again in place.
func (v *vegetationPatch) placeGround(fc *featureCtx, p Point, depth int32) bool {
	r := fc.r
	for i := int32(0); i < depth; i++ {
		s := v.ground.get(r, fc.rand, p)
		cur := r.get(p)
		if s.is(cur.block()) {
			continue
		}
		if !v.replaceable[cur.block()] {
			return i != 0
		}
		r.set(p, s)
		p = p.offset(v.dir)
	}
	return true
}

// placeVegetation runs the nested feature on the block over the ground;
// the waterlogged kind runs it one lower and waterlogs what it placed.
func (v *vegetationPatch) placeVegetation(fc *featureCtx, p Point) {
	at := p.offset(v.dir ^ 1)
	if v.waterlogged {
		at = p.offset(dirDown).offset(v.dir ^ 1)
	}
	if !v.vegetation.placeIn(&featureCtx{r: fc.r, rand: fc.rand}, at) || !v.waterlogged {
		return
	}
	if s := fc.r.get(p); s.hasProp("waterlogged") && s.prop("waterlogged") != "true" {
		fc.r.set(p, s.with("waterlogged", "true"))
	}
}
