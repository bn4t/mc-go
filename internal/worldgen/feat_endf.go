package worldgen

import (
	"sync"

	"mc-go/internal/world"
)

// End features: chorus plants, end gateways, floating islands, the
// obsidian spikes and the arrival platform.

func init() {
	featureBuilders["chorus_plant"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return func(c *featCtx, r Random, p BlockPos) bool {
			if !isAir(c.rg.at(p)) || !blockInTag(c.rg.at(offset(p, 0, -1, 0)), "supports_chorus_plant") {
				return false
			}
			endfChorusPlant(c.rg, r, p, 8)
			return true
		}
	}
	featureBuilders["end_gateway"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		// EndGatewayConfiguration: an optional exit and whether the
		// teleport to it is exact.
		var exit []int32
		if raw, ok := cfg["exit"]; ok {
			jsonUnmarshal(raw, &exit)
		}
		exact := false
		if raw, ok := cfg["exact"]; ok {
			jsonUnmarshal(raw, &exact)
		}
		return func(c *featCtx, r Random, p BlockPos) bool {
			endfGateway(c.rg, p)
			// TheEndGatewayBlockEntity.setExitPosition on the gateway
			// the box just placed.
			if len(exit) == 3 {
				if be := c.rg.blockEntity(p); be != nil && isBlock(c.rg.at(p), "end_gateway") {
					be["exit_portal"] = []int32{exit[0], exit[1], exit[2]}
					if exact {
						be["ExactTeleport"] = int8(1)
					} else {
						delete(be, "ExactTeleport")
					}
				}
			}
			return true
		}
	}
	featureBuilders["end_island"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return func(c *featCtx, r Random, p BlockPos) bool { return endfIsland(c.rg, r, p) }
	}
	featureBuilders["end_platform"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return func(c *featCtx, r Random, p BlockPos) bool {
			endfPlatform(c.rg, p)
			return true
		}
	}
	featureBuilders["end_spike"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		var spikes []endfSpike
		var list []obj
		if raw, ok := cfg["spikes"]; ok {
			jsonUnmarshal(raw, &list)
		}
		for _, s := range list {
			sp := endfSpike{x: optI32(s, "centerX", 0), z: optI32(s, "centerZ", 0), radius: optI32(s, "radius", 0),
				height: optI32(s, "height", 0)}
			if v, ok := s["guarded"]; ok {
				jsonUnmarshal(v, &sp.guarded)
			}
			spikes = append(spikes, sp)
		}
		// EndSpikeConfiguration's crystal_invulnerable/crystal_beam_target
		// ride onto the spike's crystal (the respawn sequence's spikes
		// set both; the natural end_spike feature leaves them off).
		invulnerable := false
		if v, ok := cfg["crystal_invulnerable"]; ok {
			jsonUnmarshal(v, &invulnerable)
		}
		var beam *BlockPos
		if v, ok := cfg["crystal_beam_target"]; ok {
			var bt []int32
			if jsonUnmarshal(v, &bt); len(bt) == 3 {
				beam = &BlockPos{bt[0], bt[1], bt[2]}
			}
		}
		return func(c *featCtx, r Random, p BlockPos) bool {
			ss := spikes
			if len(ss) == 0 {
				ss = endfSpikesFor(c.g.RS.Seed)
			}
			for _, s := range ss {
				if s.x>>4 == p[0]>>4 && s.z>>4 == p[2]>>4 {
					yaw := s.placeWith(c.rg.MinY(), c.rg.at, func(q BlockPos, b world.Block) { c.rg.set(q, b) }, r)
					// WorldGenRegion.addFreshEntity files the crystal
					// with the chunk holding its position.
					if pc := c.rg.chunkAt(s.x, s.z); pc != nil {
						pc.Entities = append(pc.Entities, GenEntity{Type: "end_crystal",
							X: float64(s.x) + 0.5, Y: float64(s.height + 1), Z: float64(s.z) + 0.5, Yaw: yaw,
							Invulnerable: invulnerable, BeamTarget: beam})
					}
				}
			}
			return true
		}
	}
}

// endfChorusConnect is ChorusPlantBlock.getStateWithConnections: each
// face connects to chorus plant or flower, the bottom also to its
// support tag.
func endfChorusConnect(rg *Region, p BlockPos) world.Block {
	st := blockNamed("chorus_plant")
	for i, d := range dirOffsets {
		n := rg.at(offset(p, d[0], d[1], d[2]))
		on := nethIs(n, "chorus_plant", "chorus_flower") || (i == dirDown && blockInTag(n, "supports_chorus_plant"))
		v := "false"
		if on {
			v = "true"
		}
		st, _ = world.WithProperty(st, dirNameOf[i], v)
	}
	return st
}

// endfNeighboursEmpty is ChorusFlowerBlock.allNeighborsEmpty: every
// horizontal neighbour but the excepted side is air.
func endfNeighboursEmpty(rg *Region, p BlockPos, except int) bool {
	for _, d := range horizontalDirs {
		if d == except {
			continue
		}
		o := dirOffsets[d]
		if !isAir(rg.at(offset(p, o[0], o[1], o[2]))) {
			return false
		}
	}
	return true
}

// endfChorusPlant is ChorusFlowerBlock.generatePlant.
func endfChorusPlant(rg *Region, r Random, p BlockPos, spread int32) {
	rg.set(p, endfChorusConnect(rg, p))
	endfChorusGrow(rg, r, p, p, spread, 0)
}

// endfChorusGrow is growTreeRecursive: a stalk of nextInt(4)+1 cells
// (+1 at the root) while the sides stay clear, then up to nextInt(4)
// (+1 at the root) branches below depth 4 in random horizontal
// directions inside the spread square, each recursing; a stalk without
// branches is capped with an age-5 flower.
func endfChorusGrow(rg *Region, r Random, p, root BlockPos, spread, depth int32) {
	h := r.NextIntN(4) + 1
	if depth == 0 {
		h++
	}
	for i := int32(0); i < h; i++ {
		q := offset(p, 0, i+1, 0)
		if !endfNeighboursEmpty(rg, q, -1) {
			return
		}
		rg.set(q, endfChorusConnect(rg, q))
		b := offset(q, 0, -1, 0)
		rg.set(b, endfChorusConnect(rg, b))
	}
	grew := false
	if depth < 4 {
		n := r.NextIntN(4)
		if depth == 0 {
			n++
		}
		for j := int32(0); j < n; j++ {
			dir := horizontalDirs[r.NextIntN(4)]
			o := dirOffsets[dir]
			q := offset(p, o[0], h+o[1], o[2])
			if absI32(q[0]-root[0]) >= spread || absI32(q[2]-root[2]) >= spread {
				continue
			}
			if !isAir(rg.at(q)) || !isAir(rg.at(offset(q, 0, -1, 0))) || !endfNeighboursEmpty(rg, q, oppositeDir(dir)) {
				continue
			}
			grew = true
			rg.set(q, endfChorusConnect(rg, q))
			back := offset(q, -o[0], -o[1], -o[2])
			rg.set(back, endfChorusConnect(rg, back))
			endfChorusGrow(rg, r, q, root, spread, depth+1)
		}
	}
	if !grew {
		fl, _ := world.WithProperty(blockNamed("chorus_flower"), "age", "5")
		rg.set(offset(p, 0, h, 0), fl)
	}
}

// endfGateway is EndGatewayFeature.place: the 3x5x3 box around the
// origin (betweenClosed order) — the gateway at the centre, air on its
// layer, bedrock caps two above and below, a bedrock cross on the
// remaining layers and air in their corners. The configured exit is
// written into the gateway's block entity by the feature builder.
func endfGateway(rg *Region, o BlockPos) bool {
	gw := blockNamed("end_gateway")
	bedrock := blockNamed("bedrock")
	for z := o[2] - 1; z <= o[2]+1; z++ {
		for y := o[1] - 2; y <= o[1]+2; y++ {
			for x := o[0] - 1; x <= o[0]+1; x++ {
				q := BlockPos{x, y, z}
				sx, sy, sz := x == o[0], y == o[1], z == o[2]
				cap := absI32(y-o[1]) == 2
				switch {
				case sx && sy && sz:
					rg.set(q, gw)
				case sy:
					rg.set(q, world.Air)
				case cap && sx && sz:
					rg.set(q, bedrock)
				case (sx || sz) && !cap:
					rg.set(q, bedrock)
				default:
					rg.set(q, world.Air)
				}
			}
		}
	}
	return true
}

// endfIsland is EndIslandFeature.place: layers of end stone discs from
// radius nextInt(3)+4 shrinking by nextInt(2)+0.5 per layer down while
// the radius exceeds 0.5; a disc holds x²+z² <= (r+1)².
func endfIsland(rg *Region, r Random, o BlockPos) bool {
	stone := blockNamed("end_stone")
	f := float32(r.NextIntN(3)) + 4
	for y := int32(0); f > 0.5; y-- {
		lo, hi := floorI(float64(-f)), mthCeil(float64(f))
		lim := float32(f+1) * float32(f+1)
		for x := lo; x <= hi; x++ {
			for z := lo; z <= hi; z++ {
				if float32(x*x+z*z) <= lim {
					rg.set(offset(o, x, y, z), stone)
				}
			}
		}
		f = f - (float32(r.NextIntN(2)) + 0.5)
	}
	return true
}

// endfPlatform is EndPlatformFeature / createEndPlatform without block
// drops: a 5x5 obsidian floor one below the origin and three layers of
// air above (z, then x, then y), each cell written only when it is not
// already that block.
func endfPlatform(rg *Region, o BlockPos) {
	obsidian := blockNamed("obsidian")
	for dz := int32(-2); dz <= 2; dz++ {
		for dx := int32(-2); dx <= 2; dx++ {
			for dy := int32(-1); dy < 3; dy++ {
				q := offset(o, dx, dy, dz)
				want, name := world.Air, "air"
				if dy == -1 {
					want, name = obsidian, "obsidian"
				}
				if !isBlock(rg.at(q), name) {
					rg.set(q, want)
				}
			}
		}
	}
}

type endfSpike struct {
	x, z, radius, height int32
	guarded              bool
}

var endfSpikeCache sync.Map // seed -> []endfSpike

// endfSpikesFor is EndSpikeFeature.getSpikesForLevel: the level seed's
// first legacy nextLong masked to 16 bits seeds a shuffle of 0..9 that
// sizes the ten spikes on the radius-42 ring (radius 2+k/3, height
// 76+3k, k 1 and 2 caged).
func endfSpikesFor(seed int64) []endfSpike {
	if v, ok := endfSpikeCache.Load(seed); ok {
		return v.([]endfSpike)
	}
	key := NewLegacy(seed).NextLong() & 0xFFFF
	sr := NewLegacy(key)
	order := make([]int32, 10)
	for i := range order {
		order[i] = int32(i)
	}
	for i := int32(len(order)); i > 1; i-- {
		j := sr.NextIntN(i)
		order[i-1], order[j] = order[j], order[i-1]
	}
	out := make([]endfSpike, 10)
	for i := 0; i < 10; i++ {
		a := float64(2.0 * float64(-3.141592653589793+float64(0.3141592653589793*float64(i))))
		k := order[i]
		out[i] = endfSpike{
			x:       floorI(float64(42.0 * javaCos(a))),
			z:       floorI(float64(42.0 * javaSin(a))),
			radius:  2 + k/3,
			height:  76 + k*3,
			guarded: k == 1 || k == 2,
		}
	}
	endfSpikeCache.Store(seed, out)
	return out
}

// place is EndSpikeFeature.placeSpike: the column box from the floor to
// height+10 (betweenClosed order) turns obsidian within dx²+dz² <=
// r²+1 below the height and air elsewhere above y 65; caged spikes get
// the 5x5x4 iron-bar cage with panes joined along its walls. The end
// crystal (one nextFloat for its yaw) stands on bedrock with fire at
// its feet.
func (s endfSpike) placeWith(minY int32, at func(BlockPos) world.Block, set func(BlockPos, world.Block), r Random) float32 {
	obsidian := blockNamed("obsidian")
	lim := s.radius*s.radius + 1
	for z := s.z - s.radius; z <= s.z+s.radius; z++ {
		for y := minY; y <= s.height+10; y++ {
			for x := s.x - s.radius; x <= s.x+s.radius; x++ {
				dx, dz := x-s.x, z-s.z
				if float64(dx*dx+dz*dz) <= float64(lim) && y < s.height {
					set(BlockPos{x, y, z}, obsidian)
				} else if y > 65 {
					set(BlockPos{x, y, z}, world.Air)
				}
			}
		}
	}
	if s.guarded {
		bars := blockNamed("iron_bars")
		tf := func(b bool) string {
			if b {
				return "true"
			}
			return "false"
		}
		for dx := int32(-2); dx <= 2; dx++ {
			for dz := int32(-2); dz <= 2; dz++ {
				for dy := int32(0); dy <= 3; dy++ {
					ex, ez, top := absI32(dx) == 2, absI32(dz) == 2, dy == 3
					if !ex && !ez && !top {
						continue
					}
					alongZ := dx == -2 || dx == 2 || top
					alongX := dz == -2 || dz == 2 || top
					st := bars
					st, _ = world.WithProperty(st, "north", tf(alongZ && dz != -2))
					st, _ = world.WithProperty(st, "south", tf(alongZ && dz != 2))
					st, _ = world.WithProperty(st, "west", tf(alongX && dx != -2))
					st, _ = world.WithProperty(st, "east", tf(alongX && dx != 2))
					set(BlockPos{s.x + dx, s.height + dy, s.z + dz}, st)
				}
			}
		}
	}
	yaw := r.NextFloat() * 360
	feet := BlockPos{s.x, s.height + 1, s.z}
	set(offset(feet, 0, -1, 0), blockNamed("bedrock"))
	set(feet, endfFireAt(at, feet))
	return yaw
}

// GenEntity is an entity a feature added to the chunk holding its
// position (WorldGenRegion.addFreshEntity); the server spawns it when
// that chunk is handed out. Only the end spikes' crystals use it.
type GenEntity struct {
	Type         string
	X, Y, Z      float64
	Yaw          float32
	Invulnerable bool
	BeamTarget   *BlockPos
}

// EndSpike is one of EndSpikeFeature.getSpikesForLevel's spikes.
type EndSpike struct {
	X, Z, Radius, Height int32
	Guarded              bool
}

// EndSpikes is EndSpikeFeature.getSpikesForLevel for a level seed.
func EndSpikes(seed int64) []EndSpike {
	ss := endfSpikesFor(seed)
	out := make([]EndSpike, len(ss))
	for i, s := range ss {
		out[i] = EndSpike{X: s.x, Z: s.z, Radius: s.radius, Height: s.height, Guarded: s.guarded}
	}
	return out
}

// PlaceEndSpike is EndSpikeFeature.placeSpike against any block
// reader/writer (the respawn sequence rebuilds spikes in the live
// level); it returns the crystal's rolled yaw - the crystal itself is
// the caller's to add at (x+0.5, height+1, z+0.5).
func PlaceEndSpike(sp EndSpike, minY int32, at func(BlockPos) world.Block, set func(BlockPos, world.Block), r Random) float32 {
	return endfSpike{x: sp.X, z: sp.Z, radius: sp.Radius, height: sp.Height, guarded: sp.Guarded}.placeWith(minY, at, set, r)
}

// endfFireAt is BaseFireBlock.getState: soul fire over its base blocks,
// otherwise plain fire, facing flammable neighbours only when the floor
// is neither flammable nor sturdy.
func endfFireAt(at func(BlockPos) world.Block, p BlockPos) world.Block {
	below := at(offset(p, 0, -1, 0))
	if blockInTag(below, "soul_fire_base_blocks") {
		return blockNamed("soul_fire")
	}
	// The spike crystal's fire always stands on the bedrock just placed
	// (sturdy, not flammable): the plain default state. Fire facing
	// flammable neighbours over a weak floor is not reached here.
	return blockNamed("fire")
}
