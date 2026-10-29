package worldgen

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"strings"

	"mc-go/internal/world"
)

// ProtoChunk is one chunk column mid-generation: block states, the
// quart biome cells and the worldgen heightmaps vanilla maintains while
// it writes blocks.
type ProtoChunk struct {
	CX, CZ       int32
	MinY, Height int32
	Blocks       []world.Block // ((y-MinY)*16+z)*16+x
	Biomes       []string      // ((qy-minQY)*4+qz)*4+qx
	// worldSurface[z*16+x] is WORLD_SURFACE_WG's first free y (highest
	// non-air + 1, MinY for an empty column).
	worldSurface [256]int32
	oceanFloor   [256]int32
	// The final heightmaps, primed when features start
	// (Heightmap.primeHeightmaps) and maintained from then on.
	primed                             bool
	motionBlocking, motionNoLeaves     [256]int32
	oceanFloorFinal, worldSurfaceFinal [256]int32
	FluidMarks                         [][3]int32
	// PostProcess holds positions setBlock marked for post-processing
	// (BlockState.getPostProcessPos).
	PostProcess [][3]int32
	// BlockEntityData holds block entity NBT placed with blocks
	// (structure templates), by absolute position.
	BlockEntityData map[[3]int32]map[string]any
	// Entities are the entities features added to this chunk (the
	// end spikes' crystals), spawned when the chunk is provided.
	Entities []GenEntity
}

func (p *ProtoChunk) idx(x, y, z int32) int32 { return ((y-p.MinY)*16+z)*16 + x }

// Get returns the state at local x/z, absolute y (air outside).
func (p *ProtoChunk) Get(x, y, z int32) world.Block {
	if y < p.MinY || y >= p.MinY+p.Height {
		return world.Air
	}
	return p.Blocks[p.idx(x, y, z)]
}

// Set writes a state and maintains the worldgen heightmaps the way
// Heightmap.update does: raising on a qualifying block above the top,
// rescanning down when the top cell stops qualifying.
func (p *ProtoChunk) Set(x, y, z int32, b world.Block) {
	if y < p.MinY || y >= p.MinY+p.Height {
		return
	}
	p.Blocks[p.idx(x, y, z)] = b
	// ProtoChunk.setBlockState updates the persisted status's
	// heightmapsAfter: WORLDGEN through SURFACE (so carvers still move
	// the _WG maps), FINAL from CARVERS on — feature writes leave the
	// _WG maps frozen.
	if !p.primed {
		p.updateHeight(&p.worldSurface, x, y, z, b, notAir)
		p.updateHeight(&p.oceanFloor, x, y, z, b, blocksMotion)
	} else {
		p.updateHeight(&p.motionBlocking, x, y, z, b, motionBlocking)
		p.updateHeight(&p.motionNoLeaves, x, y, z, b, motionBlockingNoLeaves)
		p.updateHeight(&p.oceanFloorFinal, x, y, z, b, blocksMotion)
		p.updateHeight(&p.worldSurfaceFinal, x, y, z, b, notAir)
	}
}

// primeHeightmaps is Heightmap.primeHeightmaps for the four final maps.
func (p *ProtoChunk) primeHeightmaps() {
	if p.primed {
		return
	}
	p.primed = true
	maps := []struct {
		hm   *[256]int32
		pred func(world.Block) bool
	}{{&p.motionBlocking, motionBlocking}, {&p.motionNoLeaves, motionBlockingNoLeaves},
		{&p.oceanFloorFinal, blocksMotion}, {&p.worldSurfaceFinal, notAir}}
	for _, m := range maps {
		for z := int32(0); z < 16; z++ {
			for x := int32(0); x < 16; x++ {
				h := p.MinY
				for y := p.MinY + p.Height - 1; y >= p.MinY; y-- {
					if m.pred(p.Blocks[p.idx(x, y, z)]) {
						h = y + 1
						break
					}
				}
				m.hm[z*16+x] = h
			}
		}
	}
}

// Heightmap types.
const (
	HMWorldSurfaceWG = iota
	HMWorldSurface
	HMOceanFloorWG
	HMOceanFloor
	HMMotionBlocking
	HMMotionBlockingNoLeaves
)

// HeightAt is ChunkAccess.getHeight(type, x, z): the top qualifying y
// (MinY-1 for none).
func (p *ProtoChunk) HeightAt(t int, x, z int32) int32 {
	i := z*16 + x
	switch t {
	case HMWorldSurfaceWG:
		return p.worldSurface[i] - 1
	case HMOceanFloorWG:
		return p.oceanFloor[i] - 1
	case HMWorldSurface:
		return p.worldSurfaceFinal[i] - 1
	case HMOceanFloor:
		return p.oceanFloorFinal[i] - 1
	case HMMotionBlocking:
		return p.motionBlocking[i] - 1
	default:
		return p.motionNoLeaves[i] - 1
	}
}

func motionBlocking(b world.Block) bool { return blocksMotionState(b) || hasFluid(b) }

func motionBlockingNoLeaves(b world.Block) bool {
	return (blocksMotionState(b) || hasFluid(b)) && !isLeaves(b)
}

func (p *ProtoChunk) updateHeight(hm *[256]int32, x, y, z int32, b world.Block, pred func(world.Block) bool) {
	i := z*16 + x
	top := hm[i]
	if y <= top-2 {
		return
	}
	if pred(b) {
		if y >= top {
			hm[i] = y + 1
		}
		return
	}
	if top-1 == y {
		for yy := y - 1; yy >= p.MinY; yy-- {
			if pred(p.Blocks[p.idx(x, yy, z)]) {
				hm[i] = yy + 1
				return
			}
		}
		hm[i] = p.MinY
	}
}

// WorldSurfaceWG is ChunkAccess.getHeight(WORLD_SURFACE_WG): highest
// non-air y (MinY-1 for an empty column).
func (p *ProtoChunk) WorldSurfaceWG(x, z int32) int32 { return p.worldSurface[z*16+x] - 1 }

// OceanFloorWG is getHeight(OCEAN_FLOOR_WG).
func (p *ProtoChunk) OceanFloorWG(x, z int32) int32 { return p.oceanFloor[z*16+x] - 1 }

func notAir(b world.Block) bool { return !isAir(b) }

// blocksMotion is the MATERIAL_MOTION_BLOCKING predicate.
func blocksMotion(b world.Block) bool { return blocksMotionState(b) }

func newProto(cx, cz, minY, height int32) *ProtoChunk {
	p := &ProtoChunk{CX: cx, CZ: cz, MinY: minY, Height: height, Blocks: make([]world.Block, 16*16*height)}
	for i := range p.worldSurface {
		p.worldSurface[i] = minY
		p.oceanFloor[i] = minY
		p.motionBlocking[i] = minY
		p.motionNoLeaves[i] = minY
		p.oceanFloorFinal[i] = minY
		p.worldSurfaceFinal[i] = minY
	}
	return p
}

// stateFromJSON resolves a {"Name": ..., "Properties": {...}} block.
func stateFromJSON(o obj) world.Block {
	name := strings.TrimPrefix(o.str("Name"), "minecraft:")
	var props map[string]string
	if raw, ok := o["Properties"]; ok {
		jsonUnmarshal(raw, &props)
	}
	b, ok := world.BlockStateOf(name, props)
	if !ok {
		panic("worldgen: unknown block state " + name)
	}
	return b
}

func blockNamed(name string) world.Block {
	b, ok := world.BlockStateOf(name, nil)
	if !ok {
		panic("worldgen: unknown block " + name)
	}
	return b
}

// obfuscateSeed is BiomeManager.obfuscateSeed: SHA-256 of the
// little-endian seed, first eight bytes little-endian.
func obfuscateSeed(seed int64) int64 {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(seed))
	s := sha256.Sum256(b[:])
	return int64(binary.LittleEndian.Uint64(s[:8]))
}

func lcgNext(l, salt int64) int64 {
	l = l*(l*6364136223846793005+1442695040888963407) + salt
	return l
}

func fiddle(l int64) float64 {
	d := float64(((l>>24)%1024+1024)%1024) / 1024
	return float64((d - 0.5) * 0.9)
}

func fiddledDistance(seed int64, x, y, z int32, fx, fy, fz float64) float64 {
	l := seed
	l = lcgNext(l, int64(x))
	l = lcgNext(l, int64(y))
	l = lcgNext(l, int64(z))
	l = lcgNext(l, int64(x))
	l = lcgNext(l, int64(y))
	l = lcgNext(l, int64(z))
	d := fiddle(l)
	l = lcgNext(l, seed)
	e := fiddle(l)
	l = lcgNext(l, seed)
	f := fiddle(l)
	a := fz + f
	b := fy + e
	c := fx + d
	return float64(a*a) + float64(b*b) + float64(c*c)
}

// zoomBiome is BiomeManager.getBiome: the voronoi-jittered choice among
// the eight surrounding quart cells.
func zoomBiome(zoomSeed int64, x, y, z int32, quart func(qx, qy, qz int32) string) string {
	i := x - 2
	j := y - 2
	k := z - 2
	l := i >> 2
	m := j >> 2
	n := k >> 2
	d := float64(i&3) / 4
	e := float64(j&3) / 4
	f := float64(k&3) / 4
	best := 0
	bestD := 0.0
	for o := 0; o < 8; o++ {
		ax, ay, az := o&4 == 0, o&2 == 0, o&1 == 0
		px, py, pz := l, m, n
		fx, fy, fz := d, e, f
		if !ax {
			px++
			fx = d - 1
		}
		if !ay {
			py++
			fy = e - 1
		}
		if !az {
			pz++
			fz = f - 1
		}
		g := fiddledDistance(zoomSeed, px, py, pz, fx, fy, fz)
		if o == 0 || bestD > g {
			best, bestD = o, g
		}
	}
	px, py, pz := l, m, n
	if best&4 != 0 {
		px++
	}
	if best&2 != 0 {
		py++
	}
	if best&1 != 0 {
		pz++
	}
	return quart(px, py, pz)
}

// PostProcessFluids returns the post-processing marks (aquifer, carver
// and feature marks, ProtoChunk.markPosForPostProcessing order: per
// section, then in marking order) whose current state holds a fluid —
// the cells LevelChunk.postProcessGeneration hands a FluidState.tick.
func (p *ProtoChunk) PostProcessFluids() [][3]int32 {
	all := append(append([][3]int32(nil), p.FluidMarks...), p.PostProcess...)
	sort.SliceStable(all, func(i, j int) bool { return floorDiv(all[i][1], 16) < floorDiv(all[j][1], 16) })
	var out [][3]int32
	for _, m := range all {
		if hasFluid(p.Get(m[0]&15, m[1], m[2]&15)) {
			out = append(out, m)
		}
	}
	return out
}
