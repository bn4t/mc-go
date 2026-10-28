package worldgen

import "fmt"

// Template-built ruins: ocean ruins, ruined portals and nether fossils.

func init() {
	structureGenerators["minecraft:ocean_ruin"] = oceanRuin
	structureGenerators["minecraft:ruined_portal"] = ruinedPortal
	structureGenerators["minecraft:nether_fossil"] = netherFossil
}

// genBounds are the Y range a height provider draws from: the level's,
// clipped to the noise.
func (c *structureContext) genBounds() (minY, height int32) {
	s := c.g.rs.Settings
	minY = max(c.g.levelMinY, int32(s.MinY))
	return minY, min(c.g.levelHeight, int32(s.Height))
}

// columnBlock reads a base column at y, air outside the noise.
func (c *structureContext) columnBlock(col []StateID, y int32) StateID {
	i := y - int32(c.g.rs.Settings.MinY)
	if i < 0 || int(i) >= len(col) {
		return stAir
	}
	return col[i]
}

func cfgFloat(cfg map[string]any, key string, def float64) float64 {
	if v, ok := cfg[key].(float64); ok {
		return v
	}
	return def
}

func cfgBool(cfg map[string]any, key string) bool {
	v, _ := cfg[key].(bool)
	return v
}

type oceanRuinPiece struct {
	templatePiece
	integrity float32
	warm      bool
	large     bool
}

func (p *oceanRuinPiece) saveTag(tag map[string]any) {
	p.saveTemplate(tag)
	tag["Rot"] = rotationNames[p.settings.rot]
	tag["Integrity"] = p.integrity
	tag["BiomeType"] = "COLD"
	if p.warm {
		tag["BiomeType"] = "WARM"
	}
	tag["IsLarge"] = boolTag(p.large)
}

func underwaterRuins(prefix string, numbers ...int) []string {
	out := make([]string, len(numbers))
	for i, n := range numbers {
		out[i] = fmt.Sprintf("minecraft:underwater_ruin/%s%d", prefix, n)
	}
	return out
}

var (
	warmRuins       = underwaterRuins("warm_", 1, 2, 3, 4, 5, 6, 7, 8)
	bigWarmRuins    = underwaterRuins("big_warm_", 4, 5, 6, 7)
	brickRuins      = underwaterRuins("brick_", 1, 2, 3, 4, 5, 6, 7, 8)
	crackedRuins    = underwaterRuins("cracked_", 1, 2, 3, 4, 5, 6, 7, 8)
	mossyRuins      = underwaterRuins("mossy_", 1, 2, 3, 4, 5, 6, 7, 8)
	bigBrickRuins   = underwaterRuins("big_brick_", 1, 2, 3, 8)
	bigCrackedRuins = underwaterRuins("big_cracked_", 1, 2, 3, 8)
	bigMossyRuins   = underwaterRuins("big_mossy_", 1, 2, 3, 8)
)

// oceanRuin is a ruin on the ocean floor at the chunk's middle, laid at
// y 90 of the chunk's corner: some large, and most large ones with a
// cluster of small ruins around them. A cold ruin stacks its brick,
// cracked and mossy variants.
func oceanRuin(c *structureContext) (genStub, bool) {
	cfg := c.def.cfg
	warm := cfg["biome_temp"] == "warm"
	largeChance := float32(cfgFloat(cfg, "large_probability", 0))
	clusterChance := float32(cfgFloat(cfg, "cluster_probability", 0))
	return c.onTopOfChunkCenter(oceanFloorWG, func() []structurePiece {
		r := c.random
		pos := Point{c.minX(), 90, c.minZ()}
		rot := randomRotation(r)
		var out []structurePiece
		add := func(at Point, rot rotation, large bool, integrity float32) {
			piece := func(name string, integrity float32) {
				out = append(out, &oceanRuinPiece{newTemplatePiece("minecraft:orp", 0, name, placeSettings{rot: rot}, at), integrity, warm, large})
			}
			if warm {
				names := warmRuins
				if large {
					names = bigWarmRuins
				}
				piece(names[r.NextIntn(int32(len(names)))], integrity)
				return
			}
			brick, cracked, mossy := brickRuins, crackedRuins, mossyRuins
			if large {
				brick, cracked, mossy = bigBrickRuins, bigCrackedRuins, bigMossyRuins
			}
			i := r.NextIntn(int32(len(brick)))
			piece(brick[i], integrity)
			piece(cracked[i], 0.7)
			piece(mossy[i], 0.5)
		}
		large := !(r.NextFloat() > largeChance)
		integrity := float32(0.8)
		if large {
			integrity = 0.9
		}
		add(pos, rot, large, integrity)
		if large && !(r.NextFloat() > clusterChance) {
			// The small ruins scatter around the large one's 16×16
			// footprint without overlapping it.
			corner := transformPos(Point{15, 0, 15}, mirrorNone, rot, Point{}).add(pos)
			footprint := boxFromCorners(pos, corner)
			origin := Point{min(pos.X, corner.X), pos.Y, min(pos.Z, corner.Z)}
			spots := clusterRuinSpots(r, origin)
			n := mthNextInt(r, 4, 8)
			for i := int32(0); i < n; i++ {
				if len(spots) == 0 {
					continue
				}
				k := r.NextIntn(int32(len(spots)))
				at := spots[k]
				spots = append(spots[:k], spots[k+1:]...)
				rot := randomRotation(r)
				far := transformPos(Point{5, 0, 6}, mirrorNone, rot, Point{}).add(at)
				if boxFromCorners(at, far).intersects(footprint) {
					continue
				}
				add(at, rot, false, 0.8)
			}
		}
		return out
	})
}

// clusterRuinSpots are eight random spots around a large ruin: three
// to its west, two in line with it, three to its east.
func clusterRuinSpots(r Random, o Point) []Point {
	at := func(dx, dz int32) Point { return o.add(Point{dx, 0, dz}) }
	var out []Point
	x := -16 + mthNextInt(r, 1, 8)
	out = append(out, at(x, 16+mthNextInt(r, 1, 7)))
	x = -16 + mthNextInt(r, 1, 8)
	out = append(out, at(x, mthNextInt(r, 1, 7)))
	x = -16 + mthNextInt(r, 1, 8)
	out = append(out, at(x, -16+mthNextInt(r, 4, 8)))
	x = mthNextInt(r, 1, 7)
	out = append(out, at(x, 16+mthNextInt(r, 1, 7)))
	x = mthNextInt(r, 1, 7)
	out = append(out, at(x, -16+mthNextInt(r, 4, 6)))
	x = 16 + mthNextInt(r, 1, 7)
	out = append(out, at(x, 16+mthNextInt(r, 3, 8)))
	x = 16 + mthNextInt(r, 1, 7)
	out = append(out, at(x, mthNextInt(r, 1, 7)))
	x = 16 + mthNextInt(r, 1, 7)
	out = append(out, at(x, -16+mthNextInt(r, 4, 8)))
	return out
}

// portalSetup is one of a ruined portal structure's weighted setups.
type portalSetup struct {
	placement                                          string
	airPocket, mossiness, weight                       float32
	canBeCold, overgrown, vines, replaceWithBlackstone bool
}

type ruinedPortalPiece struct {
	templatePiece
	placement                                                string
	cold, airPocket, overgrown, vines, replaceWithBlackstone bool
	mossiness                                                float32
}

func (p *ruinedPortalPiece) saveTag(tag map[string]any) {
	p.saveTemplate(tag)
	tag["Rotation"] = rotationNames[p.settings.rot]
	tag["Mirror"] = mirrorNames[p.settings.mirror]
	tag["VerticalPlacement"] = p.placement
	tag["Properties"] = map[string]any{"cold": boolTag(p.cold), "mossiness": p.mossiness, "air_pocket": boolTag(p.airPocket),
		"overgrown": boolTag(p.overgrown), "vines": boolTag(p.vines), "replace_with_blackstone": boolTag(p.replaceWithBlackstone)}
}

// ruinedPortal picks a setup by weight, a portal (one in twenty giant),
// a rotation and a mirror, and settles the portal at a height its setup
// draws: on the surface, the ocean floor, partly buried, underground, in
// a mountain or in the nether. The height then sinks until three of the
// footprint's corner columns are solid there.
func ruinedPortal(c *structureContext) (genStub, bool) {
	r := c.random
	var setups []portalSetup
	for _, e := range c.def.cfg["setups"].([]any) {
		m := e.(map[string]any)
		setups = append(setups, portalSetup{placement: m["placement"].(string),
			airPocket: float32(cfgFloat(m, "air_pocket_probability", 0)), mossiness: float32(cfgFloat(m, "mossiness", 0)),
			weight: float32(cfgFloat(m, "weight", 0)), canBeCold: cfgBool(m, "can_be_cold"), overgrown: cfgBool(m, "overgrown"),
			vines: cfgBool(m, "vines"), replaceWithBlackstone: cfgBool(m, "replace_with_blackstone")})
	}
	setup := setups[0]
	if len(setups) > 1 {
		total := float32(0)
		for _, s := range setups {
			total += s.weight
		}
		f := r.NextFloat()
		for _, s := range setups {
			if f -= s.weight / total; f < 0 {
				setup = s
				break
			}
		}
	}
	airPocket := setup.airPocket == 1 || setup.airPocket != 0 && r.NextFloat() < setup.airPocket
	var name string
	if r.NextFloat() < 0.05 {
		name = fmt.Sprintf("minecraft:ruined_portal/giant_portal_%d", r.NextIntn(3)+1)
	} else {
		name = fmt.Sprintf("minecraft:ruined_portal/portal_%d", r.NextIntn(10)+1)
	}
	t := loadTemplate(name)
	rot := randomRotation(r)
	m := mirrorNone
	if !(r.NextFloat() < 0.5) {
		m = mirrorFrontBack
	}
	pivot := Point{t.size.X / 2, 0, t.size.Z / 2}
	corner := Point{c.minX(), 0, c.minZ()}
	box := templateBox(corner, rot, pivot, m, t.size)
	center := box.center()
	heightmap := worldSurfaceWG
	if setup.placement == "on_ocean_floor" {
		heightmap = oceanFloorWG
	}
	surface := c.g.baseHeight(center.X, center.Z, heightmap) - 1
	y := c.portalY(setup.placement, airPocket, surface, box)
	pos := Point{corner.X, y, corner.Z}
	return genStub{pos, func() []structurePiece {
		cold := setup.canBeCold && ColdEnoughToSnow(c.biomeAt(pos), pos.X, pos.Y, pos.Z, int32(c.g.rs.Settings.SeaLevel))
		p := &ruinedPortalPiece{templatePiece: newTemplatePiece("minecraft:rupo", 0, name, placeSettings{rot: rot, mirror: m, pivot: pivot}, pos),
			placement: setup.placement, cold: cold, airPocket: airPocket, overgrown: setup.overgrown, vines: setup.vines,
			replaceWithBlackstone: setup.replaceWithBlackstone, mossiness: setup.mossiness}
		return []structurePiece{p}
	}}, true
}

// portalY draws a ruined portal's height for its placement, then lowers
// it until three of the box's corner columns are solid at it (never
// below 15 above the level's floor).
func (c *structureContext) portalY(placement string, airPocket bool, surface int32, box boundingBox) int32 {
	r := c.random
	floor := c.g.levelMinY + 15
	within := func(lo, hi int32) int32 {
		if lo < hi {
			return randomBetweenInclusive(r, lo, hi)
		}
		return hi
	}
	var y int32
	switch placement {
	case "in_nether":
		switch {
		case airPocket:
			y = randomBetweenInclusive(r, 32, 100)
		case r.NextFloat() < 0.5:
			y = randomBetweenInclusive(r, 27, 29)
		default:
			y = randomBetweenInclusive(r, 29, 100)
		}
	case "in_mountain":
		y = within(70, surface-box.spanY())
	case "underground":
		y = within(floor, surface-box.spanY())
	case "partly_buried":
		y = surface - box.spanY() + randomBetweenInclusive(r, 2, 8)
	default:
		y = surface
	}
	heightmap := worldSurfaceWG
	if placement == "on_ocean_floor" {
		heightmap = oceanFloorWG
	}
	var cols [4][]StateID
	for i, p := range [4][2]int32{{box.minX, box.minZ}, {box.maxX, box.minZ}, {box.minX, box.maxZ}, {box.maxX, box.maxZ}} {
		cols[i] = c.g.baseColumn(p[0], p[1])
	}
	for ; y > floor; y-- {
		solid := 0
		for _, col := range cols {
			if c.columnBlock(col, y).opaqueFor(heightmap) {
				if solid++; solid == 3 {
					return y
				}
			}
		}
	}
	return y
}

// opaqueFor is whether a state counts for a heightmap: not air for the
// world surface, blocking motion for the ocean floor.
func (s StateID) opaqueFor(heightmap int) bool {
	if heightmap == worldSurfaceWG {
		return !s.isAir()
	}
	return s.blocksMotion()
}

// netherFossil draws a spot in the chunk and a height, then walks down
// to the first air standing on soul sand or a sturdy top; above the
// lava sea only.
func netherFossil(c *structureContext) (genStub, bool) {
	r := c.random
	x := c.minX() + r.NextIntn(16)
	z := c.minZ() + r.NextIntn(16)
	sea := int32(c.g.rs.Settings.SeaLevel)
	minY, height := c.genBounds()
	y := decodeHeightProvider(c.def.cfg["height"]).sample(r, minY, height)
	col := c.g.baseColumn(x, z)
	for y > sea {
		above := c.columnBlock(col, y)
		y--
		below := c.columnBlock(col, y)
		if above.isAir() && (below.is("soul_sand") || below.sturdy(dirUp)) {
			break
		}
	}
	if y <= sea {
		return genStub{}, false
	}
	pos := Point{x, y, z}
	return genStub{pos, func() []structurePiece {
		rot := randomRotation(r)
		name := fmt.Sprintf("minecraft:nether_fossils/fossil_%d", r.NextIntn(14)+1)
		return []structurePiece{&netherFossilPiece{newTemplatePiece("minecraft:nefos", 0, name, placeSettings{rot: rot}, pos)}}
	}}, true
}

type netherFossilPiece struct{ templatePiece }

func (p *netherFossilPiece) saveTag(tag map[string]any) {
	p.saveTemplate(tag)
	tag["Rot"] = rotationNames[p.settings.rot]
}
