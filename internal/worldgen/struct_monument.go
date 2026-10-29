package worldgen

import (
	"sync"

	"mc-go/internal/world"
)

// Ocean monuments (OceanMonumentStructure, OceanMonumentPieces): every
// biome within 29 blocks of the chunk's (9, sea level, 9) must be a
// monument surrounding; the start is one building piece ("omb", 58 x
// 23 x 58 at y 39 facing a random direction) whose rooms live inside it
// and are not saved: the building's constructor lays out a 5x5x3 room
// grid (connections, the source and a random core, random openings
// closed while every room still reaches the source), fills it with
// entry, core, double and simple rooms, two wings and the penthouse;
// a loaded start regenerates the building from the chunk seed
// (regeneratePiecesAfterLoad). The geometry below is the measured box
// layout of each part. Elder guardians are entities and not spawned.

var (
	omGray  = sync.OnceValue(func() world.Block { return blockNamed("prismarine") })
	omLight = sync.OnceValue(func() world.Block { return blockNamed("prismarine_bricks") })
	omBlack = sync.OnceValue(func() world.Block { return blockNamed("dark_prismarine") })
	omLamp  = sync.OnceValue(func() world.Block { return blockNamed("sea_lantern") })
	omWater = sync.OnceValue(func() world.Block { return blockNamed("water") })
)

// omRoom is OceanMonumentPieces.RoomDefinition.
type omRoom struct {
	index    int
	conn     [6]*omRoom
	open     [6]bool
	claimed  bool
	isSource bool
	scan     int
}

func (r *omRoom) setConnection(d int, o *omRoom) {
	r.conn[d] = o
	o.conn[oppositeDir(d)] = r
}

func (r *omRoom) updateOpenings() {
	for i := range r.conn {
		r.open[i] = r.conn[i] != nil
	}
}

func (r *omRoom) findSource(scan int) bool {
	if r.isSource {
		return true
	}
	r.scan = scan
	for i, c := range r.conn {
		if c != nil && r.open[i] && c.scan != scan && c.findSource(scan) {
			return true
		}
	}
	return false
}

func (r *omRoom) isSpecial() bool { return r.index >= 75 }

func (r *omRoom) countOpenings() int {
	n := 0
	for _, o := range r.open {
		if o {
			n++
		}
	}
	return n
}

func omRoomIndex(x, y, z int) int { return y*25 + z*5 + x }

// Monument part kinds.
const (
	omBuilding = iota
	omEntry
	omCore
	omDoubleX
	omDoubleXY
	omDoubleY
	omDoubleYZ
	omDoubleZ
	omSimple
	omSimpleTop
	omWing
	omPenthouse
)

// omPiece is an OceanMonumentPiece of any kind.
type omPiece struct {
	vpBase
	kind     int
	room     *omRoom
	design   int32
	children []*omPiece
}

func init() {
	structureTypes["ocean_monument"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		return func(c *GenContext) (*GenerationStub, bool) {
			g := c.G
			x, z := c.ChunkX*16+9, c.ChunkZ*16+9
			// BiomeSource.getBiomesWithin(x, sea level, z, 29).
			y := g.RS.settings.seaLevel
			want := biomeTagSet("required_ocean_monument_surrounding", g)
			for qz := quartFromBlock(z - 29); qz <= quartFromBlock(z+29); qz++ {
				for qx := quartFromBlock(x - 29); qx <= quartFromBlock(x+29); qx++ {
					for qy := quartFromBlock(y - 29); qy <= quartFromBlock(y+29); qy++ {
						if !want[g.Biomes.Biome(g.RS, nil, qx, qy, qz)] {
							return nil, false
						}
					}
				}
			}
			return onTopOfChunkCenter(c, HMOceanFloorWG, func(add func(StructurePiece)) {
				dir := horizontalPlane[c.Random.NextIntN(4)]
				add(newMonumentBuilding(c.Random, c.ChunkX*16-29, c.ChunkZ*16-29, dir))
			}), true
		}
	}
	pieceLoaders["omb"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		b, err := loadVPBase(t)
		if err != nil {
			return nil, err
		}
		return &omPiece{vpBase: b, kind: omBuilding}, nil
	}
	startLoadHooks["ocean_monument"] = func(g *Generator, s *StructureStart) {
		// OceanMonumentStructure.regeneratePiecesAfterLoad.
		if len(s.Pieces) == 0 {
			return
		}
		r := NewLegacy(0)
		setLargeFeatureSeed(r, g.RS.Seed, s.ChunkX, s.ChunkZ)
		p := s.Pieces[0]
		bb := p.BoundingBox()
		dir := horizontalPlane[r.NextIntN(4)]
		if o := p.Orientation(); o >= 0 {
			dir = dirFrom2D(o)
		}
		s.Pieces = []StructurePiece{newMonumentBuilding(r, bb.MinX, bb.MinZ, dir)}
	}
}

var biomeTagCache sync.Map

// biomeTagSet resolves a biome tag to its biome names (cached).
func biomeTagSet(tag string, g *Generator) map[string]bool {
	if v, ok := biomeTagCache.Load(tag); ok {
		return v.(map[string]bool)
	}
	m := biomeHolderSet(g.RS.data, []byte(`"#minecraft:`+tag+`"`))
	biomeTagCache.Store(tag, m)
	return m
}

// omRoomBox is OceanMonumentPiece.makeBoundingBox for a room of the
// grid: the room's (w, h, d) cells at its grid position in the
// building's local frame.
func omRoomBox(dir int, r *omRoom, w, h, d int32) BoundingBox {
	i := int32(r.index)
	x, z, y := i%5, i/5%5, i/25
	b := makeBoundingBox(0, 0, 0, dir, w*8, h*4, d*8)
	switch dir {
	case dirNorth:
		return b.Moved(x*8, y*4, -(z+d)*8+1)
	case dirSouth:
		return b.Moved(x*8, y*4, z*8)
	case dirWest:
		return b.Moved(-(z+d)*8+1, y*4, x*8)
	}
	return b.Moved(z*8, y*4, x*8)
}

func newOMRoom(kind, dir int, r *omRoom, w, h, d int32) *omPiece {
	p := &omPiece{kind: kind, room: r}
	p.typ = "om"
	p.depth = 1
	p.bb = omRoomBox(dir, r, w, h, d)
	p.setOrientation(dir)
	return p
}

func newOMBoxPiece(kind, dir int, bb BoundingBox) *omPiece {
	p := &omPiece{kind: kind}
	p.typ = "om"
	p.depth = 1
	p.bb = bb
	p.setOrientation(dir)
	return p
}

// newMonumentBuilding is the MonumentBuilding constructor.
func newMonumentBuilding(r Random, x, z int32, dir int) *omPiece {
	p := &omPiece{kind: omBuilding}
	p.typ = "omb"
	p.bb = makeBoundingBox(x, 39, z, dir, 58, 23, 58)
	p.setOrientation(dir)
	source, core, list := omRoomGraph(r)
	source.claimed = true
	p.children = append(p.children, newOMRoom(omEntry, dir, source, 1, 1, 1))
	p.children = append(p.children, newOMRoom(omCore, dir, core, 2, 2, 2))
	for _, room := range list {
		if room.claimed || room.isSpecial() {
			continue
		}
		if c := omFit(dir, room, r); c != nil {
			p.children = append(p.children, c)
		}
	}
	d := p.worldPos(9, 0, 22)
	for _, c := range p.children {
		c.bb = c.bb.Moved(d[0], d[1], d[2])
	}
	box := func(a, b BlockPos) BoundingBox { return bbFrom(boxCorners(a, b)) }
	left := box(p.worldPos(1, 1, 1), p.worldPos(23, 8, 21))
	right := box(p.worldPos(34, 1, 1), p.worldPos(56, 8, 21))
	top := box(p.worldPos(22, 13, 22), p.worldPos(35, 17, 35))
	k := r.NextInt()
	w := newOMBoxPiece(omWing, dir, left)
	w.design = k & 1
	k++
	p.children = append(p.children, w)
	w = newOMBoxPiece(omWing, dir, right)
	w.design = k & 1
	p.children = append(p.children, w)
	p.children = append(p.children, newOMBoxPiece(omPenthouse, dir, top))
	return p
}

// omFit is the room fitters in order (double XY, YZ, Z, X, Y, simple
// top, simple): the first that fits claims its rooms.
func omFit(dir int, room *omRoom, r Random) *omPiece {
	e, n, u := dirEast, dirNorth, dirUp
	free := func(rm *omRoom, d int) bool { return rm.open[d] && !rm.conn[d].claimed }
	switch {
	case free(room, e) && free(room, u) && free(room.conn[e], u):
		room.claimed = true
		room.conn[e].claimed = true
		room.conn[u].claimed = true
		room.conn[e].conn[u].claimed = true
		return newOMRoom(omDoubleXY, dir, room, 2, 2, 1)
	case free(room, n) && free(room, u) && free(room.conn[n], u):
		room.claimed = true
		room.conn[n].claimed = true
		room.conn[u].claimed = true
		room.conn[n].conn[u].claimed = true
		return newOMRoom(omDoubleYZ, dir, room, 1, 2, 2)
	case free(room, n):
		a := room
		if !room.open[n] || room.conn[n].claimed {
			a = room.conn[dirSouth]
		}
		a.claimed = true
		a.conn[n].claimed = true
		return newOMRoom(omDoubleZ, dir, a, 1, 1, 2)
	case free(room, e):
		room.claimed = true
		room.conn[e].claimed = true
		return newOMRoom(omDoubleX, dir, room, 2, 1, 1)
	case free(room, u):
		room.claimed = true
		room.conn[u].claimed = true
		return newOMRoom(omDoubleY, dir, room, 1, 2, 1)
	case !room.open[dirWest] && !room.open[dirEast] && !room.open[dirNorth] && !room.open[dirSouth] && !room.open[dirUp]:
		room.claimed = true
		return newOMRoom(omSimpleTop, dir, room, 1, 1, 1)
	}
	room.claimed = true
	p := newOMRoom(omSimple, dir, room, 1, 1, 1)
	p.design = r.NextIntN(3)
	return p
}

var omDirections = [6]int{dirDown, dirUp, dirNorth, dirSouth, dirWest, dirEast}

// omRoomGraph is MonumentBuilding.generateRoomGraph.
func omRoomGraph(r Random) (source, core *omRoom, list []*omRoom) {
	var rooms [75]*omRoom
	mk := func(x, y, z int) {
		i := omRoomIndex(x, y, z)
		rooms[i] = &omRoom{index: i}
	}
	for x := 0; x < 5; x++ {
		for z := 0; z < 4; z++ {
			mk(x, 0, z)
		}
	}
	for x := 0; x < 5; x++ {
		for z := 0; z < 4; z++ {
			mk(x, 1, z)
		}
	}
	for x := 1; x < 4; x++ {
		for z := 0; z < 2; z++ {
			mk(x, 2, z)
		}
	}
	source = rooms[omRoomIndex(2, 0, 0)]
	for x := 0; x < 5; x++ {
		for z := 0; z < 5; z++ {
			for y := 0; y < 3; y++ {
				room := rooms[omRoomIndex(x, y, z)]
				if room == nil {
					continue
				}
				for _, d := range omDirections {
					o := dirOffsets[d]
					nx, ny, nz := x+int(o[0]), y+int(o[1]), z+int(o[2])
					if nx < 0 || nx >= 5 || nz < 0 || nz >= 5 || ny < 0 || ny >= 3 {
						continue
					}
					other := rooms[omRoomIndex(nx, ny, nz)]
					if other == nil {
						continue
					}
					if nz == z {
						room.setConnection(d, other)
					} else {
						room.setConnection(oppositeDir(d), other)
					}
				}
			}
		}
	}
	pent := &omRoom{index: 1003}
	lw := &omRoom{index: 1001}
	rw := &omRoom{index: 1002}
	rooms[omRoomIndex(2, 2, 0)].setConnection(dirUp, pent)
	rooms[omRoomIndex(0, 1, 0)].setConnection(dirSouth, lw)
	rooms[omRoomIndex(4, 1, 0)].setConnection(dirSouth, rw)
	pent.claimed, lw.claimed, rw.claimed = true, true, true
	source.isSource = true
	core = rooms[omRoomIndex(int(r.NextIntN(4)), 0, 2)]
	core.claimed = true
	ce, cn, cu := core.conn[dirEast], core.conn[dirNorth], core.conn[dirUp]
	ce.claimed = true
	cn.claimed = true
	ce.conn[dirNorth].claimed = true
	cu.claimed = true
	ce.conn[dirUp].claimed = true
	cn.conn[dirUp].claimed = true
	ce.conn[dirNorth].conn[dirUp].claimed = true
	for _, room := range rooms {
		if room != nil {
			room.updateOpenings()
			list = append(list, room)
		}
	}
	pent.updateOpenings()
	for i := int32(len(list)); i > 1; i-- {
		j := r.NextIntN(i)
		list[i-1], list[j] = list[j], list[i-1]
	}
	scan := 1
	for _, room := range list {
		closed, tries := 0, 0
		for closed < 2 && tries < 5 {
			tries++
			d := int(r.NextIntN(6))
			if !room.open[d] {
				continue
			}
			o := oppositeDir(d)
			room.open[d] = false
			room.conn[d].open[o] = false
			ok := room.findSource(scan)
			scan++
			if ok {
				ok = room.conn[d].findSource(scan)
				scan++
			}
			if ok {
				closed++
			} else {
				room.open[d] = true
				room.conn[d].open[o] = true
			}
		}
	}
	list = append(list, pent, lw, rw)
	return source, core, list
}

// omCtx carries one postProcess call.
type omCtx struct {
	p *omPiece
	c *PieceContext
}

func (x omCtx) box(x0, y0, z0, x1, y1, z1 int32, st world.Block) {
	x.p.generateBox(x.c, x.c.ChunkBox, x0, y0, z0, x1, y1, z1, st, st, false)
}

func (x omCtx) L(x0, y0, z0, x1, y1, z1 int32) { x.box(x0, y0, z0, x1, y1, z1, omLight()) }
func (x omCtx) G(x0, y0, z0, x1, y1, z1 int32) { x.box(x0, y0, z0, x1, y1, z1, omGray()) }
func (x omCtx) B(x0, y0, z0, x1, y1, z1 int32) { x.box(x0, y0, z0, x1, y1, z1, omBlack()) }

func (x omCtx) set(st world.Block, px, py, pz int32) {
	x.p.placeBlock(x.c, st, px, py, pz, x.c.ChunkBox)
}

// water is OceanMonumentPiece.generateWaterBox: everything but ice and
// water becomes water, air at or above sea level.
func (x omCtx) water(x0, y0, z0, x1, y1, z1 int32) {
	sea := x.c.Region.seaLevel()
	for y := y0; y <= y1; y++ {
		for i := x0; i <= x1; i++ {
			for k := z0; k <= z1; k++ {
				st := x.p.getBlock(x.c, i, y, k, x.c.ChunkBox)
				n, _ := world.StateOwner(st)
				if n == "ice" || n == "packed_ice" || n == "blue_ice" || n == "water" {
					continue
				}
				if x.p.worldY(y) >= sea && st != omWater() {
					x.set(world.Air, i, y, k)
				} else {
					x.set(omWater(), i, y, k)
				}
			}
		}
	}
}

// fillOnly is generateBoxOnFillOnly: only water cells take st.
func (x omCtx) fillOnly(x0, y0, z0, x1, y1, z1 int32, st world.Block) {
	for y := y0; y <= y1; y++ {
		for i := x0; i <= x1; i++ {
			for k := z0; k <= z1; k++ {
				if x.p.getBlock(x.c, i, y, k, x.c.ChunkBox) == omWater() {
					x.set(st, i, y, k)
				}
			}
		}
	}
}

// floor is generateDefaultFloor.
func (x omCtx) floor(fx, fz int32, open bool) {
	if !open {
		x.G(fx, 0, fz, fx+7, 0, fz+7)
		return
	}
	x.G(fx, 0, fz, fx+2, 0, fz+7)
	x.G(fx+5, 0, fz, fx+7, 0, fz+7)
	x.G(fx+3, 0, fz, fx+4, 0, fz+2)
	x.G(fx+3, 0, fz+5, fx+4, 0, fz+7)
	x.L(fx+3, 0, fz+2, fx+4, 0, fz+2)
	x.L(fx+3, 0, fz+5, fx+4, 0, fz+5)
	x.L(fx+2, 0, fz+3, fx+2, 0, fz+4)
	x.L(fx+5, 0, fz+3, fx+5, 0, fz+4)
}

// chunkIntersects is OceanMonumentPiece.chunkIntersects.
func (x omCtx) chunkIntersects(x0, z0, x1, z1 int32) bool {
	ax, az := x.p.worldX(x0, z0), x.p.worldZ(x0, z0)
	bx, bz := x.p.worldX(x1, z1), x.p.worldZ(x1, z1)
	return x.c.ChunkBox.IntersectsXZ(min(ax, bx), min(az, bz), max(ax, bx), max(az, bz))
}

// SaveData: monument pieces save nothing of their own.
func (p *omPiece) SaveData(t map[string]any) {}

// PostProcess draws the part.
func (p *omPiece) PostProcess(c *PieceContext) {
	x := omCtx{p, c}
	switch p.kind {
	case omBuilding:
		p.building(x)
	case omEntry:
		p.entry(x)
	case omCore:
		p.core(x)
	case omDoubleX:
		p.doubleX(x)
	case omDoubleXY:
		p.doubleXY(x)
	case omDoubleY:
		p.doubleY(x)
	case omDoubleYZ:
		p.doubleYZ(x)
	case omDoubleZ:
		p.doubleZ(x)
	case omSimple:
		p.simple(x)
	case omSimpleTop:
		p.simpleTop(x)
	case omWing:
		p.wing(x)
	case omPenthouse:
		p.penthouse(x)
	}
}

func (p *omPiece) building(x omCtx) {
	c := x.c
	top := max(c.Region.seaLevel(), 64) - p.bb.MinY
	x.water(0, 0, 0, 58, top, 58)
	p.buildWing(x, false, 0)
	p.buildWing(x, true, 33)
	p.entranceArchs(x)
	p.entranceWall(x)
	p.roofPiece(x)
	p.lowerWall(x)
	p.middleWall(x)
	p.upperWall(x)
	for i := int32(0); i < 7; i++ {
		for j := int32(0); j < 7; {
			if j == 0 && i == 3 {
				j = 6
			}
			bx, bz := i*9, j*9
			for a := int32(0); a < 4; a++ {
				for b := int32(0); b < 4; b++ {
					x.set(omLight(), bx+a, 0, bz+b)
					p.fillColumnDown(c, omLight(), bx+a, -1, bz+b, c.ChunkBox)
				}
			}
			if i == 0 || i == 6 {
				j++
			} else {
				j += 6
			}
		}
	}
	for i := int32(0); i < 5; i++ {
		x.water(-1-i, i*2, -1-i, -1-i, 23, 58+i)
		x.water(58+i, i*2, -1-i, 58+i, 23, 58+i)
		x.water(-i, i*2, -1-i, 57+i, 23, -1-i)
		x.water(-i, i*2, 58+i, 57+i, 23, 58+i)
	}
	for _, ch := range p.children {
		if ch.bb.Intersects(c.ChunkBox) {
			ch.PostProcess(c)
		}
	}
}

func (p *omPiece) buildWing(x omCtx, flipped bool, wx int32) {
	if !x.chunkIntersects(wx, 0, wx+23, 20) {
		return
	}
	x.G(wx, 0, 0, wx+24, 0, 20)
	x.water(wx, 1, 0, wx+24, 10, 20)
	for i := int32(0); i < 4; i++ {
		x.L(wx+i, i+1, i, wx+i, i+1, 20)
		x.L(wx+i+7, i+5, i+7, wx+i+7, i+5, 20)
		x.L(wx+17-i, i+5, i+7, wx+17-i, i+5, 20)
		x.L(wx+24-i, i+1, i, wx+24-i, i+1, 20)
		x.L(wx+i+1, i+1, i, wx+23-i, i+1, i)
		x.L(wx+i+8, i+5, i+7, wx+16-i, i+5, i+7)
	}
	x.G(wx+4, 4, 4, wx+6, 4, 20)
	x.G(wx+7, 4, 4, wx+17, 4, 6)
	x.G(wx+18, 4, 4, wx+20, 4, 20)
	x.G(wx+11, 8, 11, wx+13, 8, 20)
	x.set(omLight(), wx+12, 9, 12)
	x.set(omLight(), wx+12, 9, 15)
	x.set(omLight(), wx+12, 9, 18)
	a, b := wx+5, wx+19
	if flipped {
		a, b = wx+19, wx+5
	}
	for z := int32(20); z >= 5; z -= 3 {
		x.set(omLight(), a, 5, z)
	}
	for z := int32(19); z >= 7; z -= 3 {
		x.set(omLight(), b, 5, z)
	}
	for i := int32(0); i < 4; i++ {
		px := wx + 17 - i*3
		if flipped {
			px = wx + 24 - (17 - i*3)
		}
		x.set(omLight(), px, 5, 5)
	}
	x.set(omLight(), b, 5, 5)
	x.G(wx+11, 1, 12, wx+13, 7, 12)
	x.G(wx+12, 1, 11, wx+12, 7, 13)
}

func (p *omPiece) entranceArchs(x omCtx) {
	if !x.chunkIntersects(22, 5, 35, 17) {
		return
	}
	x.water(25, 0, 0, 32, 8, 20)
	for i := int32(0); i < 4; i++ {
		z := 5 + i*4
		x.L(24, 2, z, 24, 4, z)
		x.L(22, 4, z, 23, 4, z)
		x.set(omLight(), 25, 5, z)
		x.set(omLight(), 26, 6, z)
		x.set(omLamp(), 26, 5, z)
		x.L(33, 2, z, 33, 4, z)
		x.L(34, 4, z, 35, 4, z)
		x.set(omLight(), 32, 5, z)
		x.set(omLight(), 31, 6, z)
		x.set(omLamp(), 31, 5, z)
		x.G(27, 6, z, 30, 6, z)
	}
}

func (p *omPiece) entranceWall(x omCtx) {
	if !x.chunkIntersects(15, 20, 42, 21) {
		return
	}
	x.G(15, 0, 21, 42, 0, 21)
	x.water(26, 1, 21, 31, 3, 21)
	x.G(21, 12, 21, 36, 12, 21)
	x.G(17, 11, 21, 40, 11, 21)
	x.G(16, 10, 21, 41, 10, 21)
	x.G(15, 7, 21, 42, 9, 21)
	x.G(16, 6, 21, 41, 6, 21)
	x.G(17, 5, 21, 40, 5, 21)
	x.G(21, 4, 21, 36, 4, 21)
	x.G(22, 3, 21, 26, 3, 21)
	x.G(31, 3, 21, 35, 3, 21)
	x.G(23, 2, 21, 25, 2, 21)
	x.G(32, 2, 21, 34, 2, 21)
	x.L(28, 4, 20, 29, 4, 21)
	x.set(omLight(), 27, 3, 21)
	x.set(omLight(), 30, 3, 21)
	x.set(omLight(), 26, 2, 21)
	x.set(omLight(), 31, 2, 21)
	x.set(omLight(), 25, 1, 21)
	x.set(omLight(), 32, 1, 21)
	for i := int32(0); i < 7; i++ {
		x.set(omBlack(), 28-i, 6+i, 21)
		x.set(omBlack(), 29+i, 6+i, 21)
	}
	for i := int32(0); i < 4; i++ {
		x.set(omBlack(), 28-i, 9+i, 21)
		x.set(omBlack(), 29+i, 9+i, 21)
	}
	x.set(omBlack(), 28, 12, 21)
	x.set(omBlack(), 29, 12, 21)
	for i := int32(0); i < 3; i++ {
		x.set(omBlack(), 22-i*2, 8, 21)
		x.set(omBlack(), 22-i*2, 9, 21)
		x.set(omBlack(), 35+i*2, 8, 21)
		x.set(omBlack(), 35+i*2, 9, 21)
	}
	x.water(15, 13, 21, 42, 15, 21)
	x.water(15, 1, 21, 15, 6, 21)
	x.water(16, 1, 21, 16, 5, 21)
	x.water(17, 1, 21, 20, 4, 21)
	x.water(21, 1, 21, 21, 3, 21)
	x.water(22, 1, 21, 22, 2, 21)
	x.water(23, 1, 21, 24, 1, 21)
	x.water(42, 1, 21, 42, 6, 21)
	x.water(41, 1, 21, 41, 5, 21)
	x.water(37, 1, 21, 40, 4, 21)
	x.water(36, 1, 21, 36, 3, 21)
	x.water(33, 1, 21, 34, 1, 21)
	x.water(35, 1, 21, 35, 2, 21)
}

func (p *omPiece) roofPiece(x omCtx) {
	if !x.chunkIntersects(21, 21, 36, 36) {
		return
	}
	x.G(21, 0, 22, 36, 0, 36)
	x.water(21, 1, 22, 36, 23, 36)
	for i := int32(0); i < 4; i++ {
		x.L(21+i, 13+i, 21+i, 36-i, 13+i, 21+i)
		x.L(21+i, 13+i, 36-i, 36-i, 13+i, 36-i)
		x.L(21+i, 13+i, 22+i, 21+i, 13+i, 35-i)
		x.L(36-i, 13+i, 22+i, 36-i, 13+i, 35-i)
	}
	x.G(25, 16, 25, 32, 16, 32)
	x.L(25, 17, 25, 25, 19, 25)
	x.L(32, 17, 25, 32, 19, 25)
	x.L(25, 17, 32, 25, 19, 32)
	x.L(32, 17, 32, 32, 19, 32)
	x.set(omLight(), 26, 20, 26)
	x.set(omLight(), 27, 21, 27)
	x.set(omLamp(), 27, 20, 27)
	x.set(omLight(), 26, 20, 31)
	x.set(omLight(), 27, 21, 30)
	x.set(omLamp(), 27, 20, 30)
	x.set(omLight(), 31, 20, 31)
	x.set(omLight(), 30, 21, 30)
	x.set(omLamp(), 30, 20, 30)
	x.set(omLight(), 31, 20, 26)
	x.set(omLight(), 30, 21, 27)
	x.set(omLamp(), 30, 20, 27)
	x.G(28, 21, 27, 29, 21, 27)
	x.G(27, 21, 28, 27, 21, 29)
	x.G(28, 21, 30, 29, 21, 30)
	x.G(30, 21, 28, 30, 21, 29)
}

func (p *omPiece) lowerWall(x omCtx) {
	if x.chunkIntersects(0, 21, 6, 58) {
		x.G(0, 0, 21, 6, 0, 57)
		x.water(0, 1, 21, 6, 7, 57)
		x.G(4, 4, 21, 6, 4, 53)
		for i := int32(0); i < 4; i++ {
			x.L(i, i+1, 21, i, i+1, 57-i)
		}
		for z := int32(23); z < 53; z += 3 {
			x.set(omLight(), 5, 5, z)
		}
		x.set(omLight(), 5, 5, 52)
		for i := int32(0); i < 4; i++ {
			x.L(i, i+1, 21, i, i+1, 57-i)
		}
		x.G(4, 1, 52, 6, 3, 52)
		x.G(5, 1, 51, 5, 3, 53)
	}
	if x.chunkIntersects(51, 21, 58, 58) {
		x.G(51, 0, 21, 57, 0, 57)
		x.water(51, 1, 21, 57, 7, 57)
		x.G(51, 4, 21, 53, 4, 53)
		for i := int32(0); i < 4; i++ {
			x.L(57-i, i+1, 21, 57-i, i+1, 57-i)
		}
		for z := int32(23); z < 53; z += 3 {
			x.set(omLight(), 52, 5, z)
		}
		x.set(omLight(), 52, 5, 52)
		x.G(51, 1, 52, 53, 3, 52)
		x.G(52, 1, 51, 52, 3, 53)
	}
	if x.chunkIntersects(0, 51, 57, 57) {
		x.G(7, 0, 51, 50, 0, 57)
		x.water(7, 1, 51, 50, 10, 57)
		for i := int32(0); i < 4; i++ {
			x.L(i+1, i+1, 57-i, 56-i, i+1, 57-i)
		}
	}
}

func (p *omPiece) middleWall(x omCtx) {
	if x.chunkIntersects(7, 21, 13, 50) {
		x.G(7, 0, 21, 13, 0, 50)
		x.water(7, 1, 21, 13, 10, 50)
		x.G(11, 8, 21, 13, 8, 53)
		for i := int32(0); i < 4; i++ {
			x.L(i+7, i+5, 21, i+7, i+5, 54)
		}
		for z := int32(21); z <= 45; z += 3 {
			x.set(omLight(), 12, 9, z)
		}
	}
	if x.chunkIntersects(44, 21, 50, 54) {
		x.G(44, 0, 21, 50, 0, 50)
		x.water(44, 1, 21, 50, 10, 50)
		x.G(44, 8, 21, 46, 8, 53)
		for i := int32(0); i < 4; i++ {
			x.L(50-i, i+5, 21, 50-i, i+5, 54)
		}
		for z := int32(21); z <= 45; z += 3 {
			x.set(omLight(), 45, 9, z)
		}
	}
	if x.chunkIntersects(8, 44, 49, 54) {
		x.G(14, 0, 44, 43, 0, 50)
		x.water(14, 1, 44, 43, 10, 50)
		for i := int32(12); i <= 45; i += 3 {
			x.set(omLight(), i, 9, 45)
			x.set(omLight(), i, 9, 52)
			if i == 12 || i == 18 || i == 24 || i == 33 || i == 39 || i == 45 {
				x.set(omLight(), i, 9, 47)
				x.set(omLight(), i, 9, 50)
				x.set(omLight(), i, 10, 45)
				x.set(omLight(), i, 10, 46)
				x.set(omLight(), i, 10, 51)
				x.set(omLight(), i, 10, 52)
				x.set(omLight(), i, 11, 47)
				x.set(omLight(), i, 11, 50)
				x.set(omLight(), i, 12, 48)
				x.set(omLight(), i, 12, 49)
			}
		}
		for i := int32(0); i < 3; i++ {
			x.G(8+i, 5+i, 54, 49-i, 5+i, 54)
		}
		x.L(11, 8, 54, 46, 8, 54)
		x.G(14, 8, 44, 43, 8, 53)
	}
}

func (p *omPiece) upperWall(x omCtx) {
	if x.chunkIntersects(14, 21, 20, 43) {
		x.G(14, 0, 21, 20, 0, 43)
		x.water(14, 1, 22, 20, 14, 43)
		x.G(18, 12, 22, 20, 12, 39)
		x.L(18, 12, 21, 20, 12, 21)
		for i := int32(0); i < 4; i++ {
			x.L(i+14, i+9, 21, i+14, i+9, 43-i)
		}
		for z := int32(23); z <= 39; z += 3 {
			x.set(omLight(), 19, 13, z)
		}
	}
	if x.chunkIntersects(37, 21, 43, 43) {
		x.G(37, 0, 21, 43, 0, 43)
		x.water(37, 1, 22, 43, 14, 43)
		x.G(37, 12, 22, 39, 12, 39)
		x.L(37, 12, 21, 39, 12, 21)
		for i := int32(0); i < 4; i++ {
			x.L(43-i, i+9, 21, 43-i, i+9, 43-i)
		}
		for z := int32(23); z <= 39; z += 3 {
			x.set(omLight(), 38, 13, z)
		}
	}
	if x.chunkIntersects(15, 37, 42, 43) {
		x.G(21, 0, 37, 36, 0, 43)
		x.water(21, 1, 37, 36, 14, 43)
		x.G(21, 12, 37, 36, 12, 39)
		for i := int32(0); i < 4; i++ {
			x.L(15+i, i+9, 43-i, 42-i, i+9, 43-i)
		}
		for i := int32(21); i <= 36; i += 3 {
			x.set(omLight(), i, 13, 38)
		}
	}
}

func (p *omPiece) entry(x omCtx) {
	x.L(0, 3, 0, 2, 3, 7)
	x.L(5, 3, 0, 7, 3, 7)
	x.L(0, 2, 0, 1, 2, 7)
	x.L(6, 2, 0, 7, 2, 7)
	x.L(0, 1, 0, 0, 1, 7)
	x.L(7, 1, 0, 7, 1, 7)
	x.L(0, 1, 7, 7, 3, 7)
	x.L(1, 1, 0, 2, 3, 0)
	x.L(5, 1, 0, 6, 3, 0)
	r := p.room
	if r.open[dirNorth] {
		x.water(3, 1, 7, 4, 2, 7)
	}
	if r.open[dirWest] {
		x.water(0, 1, 3, 1, 2, 4)
	}
	if r.open[dirEast] {
		x.water(6, 1, 3, 7, 2, 4)
	}
}

func (p *omPiece) core(x omCtx) {
	x.fillOnly(1, 8, 0, 14, 8, 14, omGray())
	x.L(0, 7, 0, 0, 7, 15)
	x.L(15, 7, 0, 15, 7, 15)
	x.L(1, 7, 0, 15, 7, 0)
	x.L(1, 7, 15, 14, 7, 15)
	for y := int32(1); y <= 6; y++ {
		st := omLight()
		if y == 2 || y == 6 {
			st = omGray()
		}
		for i := int32(0); i <= 15; i += 15 {
			x.box(i, y, 0, i, y, 1, st)
			x.box(i, y, 6, i, y, 9, st)
			x.box(i, y, 14, i, y, 15, st)
		}
		x.box(1, y, 0, 1, y, 0, st)
		x.box(6, y, 0, 9, y, 0, st)
		x.box(14, y, 0, 14, y, 0, st)
		x.box(1, y, 15, 14, y, 15, st)
	}
	x.B(6, 3, 6, 9, 6, 9)
	x.box(7, 4, 7, 8, 5, 8, blockNamed("gold_block"))
	for y := int32(3); y <= 6; y += 3 {
		for i := int32(6); i <= 9; i += 3 {
			x.set(omLamp(), i, y, 6)
			x.set(omLamp(), i, y, 9)
		}
	}
	x.L(5, 1, 6, 5, 2, 6)
	x.L(5, 1, 9, 5, 2, 9)
	x.L(10, 1, 6, 10, 2, 6)
	x.L(10, 1, 9, 10, 2, 9)
	x.L(6, 1, 5, 6, 2, 5)
	x.L(9, 1, 5, 9, 2, 5)
	x.L(6, 1, 10, 6, 2, 10)
	x.L(9, 1, 10, 9, 2, 10)
	x.L(5, 2, 5, 5, 6, 5)
	x.L(5, 2, 10, 5, 6, 10)
	x.L(10, 2, 5, 10, 6, 5)
	x.L(10, 2, 10, 10, 6, 10)
	x.L(5, 7, 1, 5, 7, 6)
	x.L(10, 7, 1, 10, 7, 6)
	x.L(5, 7, 9, 5, 7, 14)
	x.L(10, 7, 9, 10, 7, 14)
	x.L(1, 7, 5, 6, 7, 5)
	x.L(1, 7, 10, 6, 7, 10)
	x.L(9, 7, 5, 14, 7, 5)
	x.L(9, 7, 10, 14, 7, 10)
	x.L(2, 1, 2, 2, 1, 3)
	x.L(3, 1, 2, 3, 1, 2)
	x.L(13, 1, 2, 13, 1, 3)
	x.L(12, 1, 2, 12, 1, 2)
	x.L(2, 1, 12, 2, 1, 13)
	x.L(3, 1, 13, 3, 1, 13)
	x.L(13, 1, 12, 13, 1, 13)
	x.L(12, 1, 13, 12, 1, 13)
}

func (p *omPiece) floorLevel() bool { return p.room.index/25 > 0 }

func (p *omPiece) doubleX(x omCtx) {
	b, a := p.room.conn[dirEast], p.room
	if p.floorLevel() {
		x.floor(8, 0, b.open[dirDown])
		x.floor(0, 0, a.open[dirDown])
	}
	if a.conn[dirUp] == nil {
		x.fillOnly(1, 4, 1, 7, 4, 6, omGray())
	}
	if b.conn[dirUp] == nil {
		x.fillOnly(8, 4, 1, 14, 4, 6, omGray())
	}
	x.L(0, 3, 0, 0, 3, 7)
	x.L(15, 3, 0, 15, 3, 7)
	x.L(1, 3, 0, 15, 3, 0)
	x.L(1, 3, 7, 14, 3, 7)
	x.G(0, 2, 0, 0, 2, 7)
	x.G(15, 2, 0, 15, 2, 7)
	x.G(1, 2, 0, 15, 2, 0)
	x.G(1, 2, 7, 14, 2, 7)
	x.L(0, 1, 0, 0, 1, 7)
	x.L(15, 1, 0, 15, 1, 7)
	x.L(1, 1, 0, 15, 1, 0)
	x.L(1, 1, 7, 14, 1, 7)
	x.L(5, 1, 0, 10, 1, 4)
	x.G(6, 2, 0, 9, 2, 3)
	x.L(5, 3, 0, 10, 3, 4)
	x.set(omLamp(), 6, 2, 3)
	x.set(omLamp(), 9, 2, 3)
	if a.open[dirSouth] {
		x.water(3, 1, 0, 4, 2, 0)
	}
	if a.open[dirNorth] {
		x.water(3, 1, 7, 4, 2, 7)
	}
	if a.open[dirWest] {
		x.water(0, 1, 3, 0, 2, 4)
	}
	if b.open[dirSouth] {
		x.water(11, 1, 0, 12, 2, 0)
	}
	if b.open[dirNorth] {
		x.water(11, 1, 7, 12, 2, 7)
	}
	if b.open[dirEast] {
		x.water(15, 1, 3, 15, 2, 4)
	}
}

func (p *omPiece) doubleXY(x omCtx) {
	b, a := p.room.conn[dirEast], p.room
	au, bu := a.conn[dirUp], b.conn[dirUp]
	if p.floorLevel() {
		x.floor(8, 0, b.open[dirDown])
		x.floor(0, 0, a.open[dirDown])
	}
	if au.conn[dirUp] == nil {
		x.fillOnly(1, 8, 1, 7, 8, 6, omGray())
	}
	if bu.conn[dirUp] == nil {
		x.fillOnly(8, 8, 1, 14, 8, 6, omGray())
	}
	for y := int32(1); y <= 7; y++ {
		st := omLight()
		if y == 2 || y == 6 {
			st = omGray()
		}
		x.box(0, y, 0, 0, y, 7, st)
		x.box(15, y, 0, 15, y, 7, st)
		x.box(1, y, 0, 15, y, 0, st)
		x.box(1, y, 7, 14, y, 7, st)
	}
	x.L(2, 1, 3, 2, 7, 4)
	x.L(3, 1, 2, 4, 7, 2)
	x.L(3, 1, 5, 4, 7, 5)
	x.L(13, 1, 3, 13, 7, 4)
	x.L(11, 1, 2, 12, 7, 2)
	x.L(11, 1, 5, 12, 7, 5)
	x.L(5, 1, 3, 5, 3, 4)
	x.L(10, 1, 3, 10, 3, 4)
	x.L(5, 7, 2, 10, 7, 5)
	x.L(5, 5, 2, 5, 7, 2)
	x.L(10, 5, 2, 10, 7, 2)
	x.L(5, 5, 5, 5, 7, 5)
	x.L(10, 5, 5, 10, 7, 5)
	x.set(omLight(), 6, 6, 2)
	x.set(omLight(), 9, 6, 2)
	x.set(omLight(), 6, 6, 5)
	x.set(omLight(), 9, 6, 5)
	x.L(5, 4, 3, 6, 4, 4)
	x.L(9, 4, 3, 10, 4, 4)
	x.set(omLamp(), 5, 4, 2)
	x.set(omLamp(), 5, 4, 5)
	x.set(omLamp(), 10, 4, 2)
	x.set(omLamp(), 10, 4, 5)
	if a.open[dirSouth] {
		x.water(3, 1, 0, 4, 2, 0)
	}
	if a.open[dirNorth] {
		x.water(3, 1, 7, 4, 2, 7)
	}
	if a.open[dirWest] {
		x.water(0, 1, 3, 0, 2, 4)
	}
	if b.open[dirSouth] {
		x.water(11, 1, 0, 12, 2, 0)
	}
	if b.open[dirNorth] {
		x.water(11, 1, 7, 12, 2, 7)
	}
	if b.open[dirEast] {
		x.water(15, 1, 3, 15, 2, 4)
	}
	if au.open[dirSouth] {
		x.water(3, 5, 0, 4, 6, 0)
	}
	if au.open[dirNorth] {
		x.water(3, 5, 7, 4, 6, 7)
	}
	if au.open[dirWest] {
		x.water(0, 5, 3, 0, 6, 4)
	}
	if bu.open[dirSouth] {
		x.water(11, 5, 0, 12, 6, 0)
	}
	if bu.open[dirNorth] {
		x.water(11, 5, 7, 12, 6, 7)
	}
	if bu.open[dirEast] {
		x.water(15, 5, 3, 15, 6, 4)
	}
}

func (p *omPiece) doubleY(x omCtx) {
	if p.floorLevel() {
		x.floor(0, 0, p.room.open[dirDown])
	}
	up := p.room.conn[dirUp]
	if up.conn[dirUp] == nil {
		x.fillOnly(1, 8, 1, 6, 8, 6, omGray())
	}
	x.L(0, 4, 0, 0, 4, 7)
	x.L(7, 4, 0, 7, 4, 7)
	x.L(1, 4, 0, 6, 4, 0)
	x.L(1, 4, 7, 6, 4, 7)
	x.L(2, 4, 1, 2, 4, 2)
	x.L(1, 4, 2, 1, 4, 2)
	x.L(5, 4, 1, 5, 4, 2)
	x.L(6, 4, 2, 6, 4, 2)
	x.L(2, 4, 5, 2, 4, 6)
	x.L(1, 4, 5, 1, 4, 5)
	x.L(5, 4, 5, 5, 4, 6)
	x.L(6, 4, 5, 6, 4, 5)
	r := p.room
	for y := int32(1); y <= 5; y += 4 {
		for _, z := range [2]int32{0, 7} {
			d := dirSouth
			if z == 7 {
				d = dirNorth
			}
			if r.open[d] {
				x.L(2, y, z, 2, y+2, z)
				x.L(5, y, z, 5, y+2, z)
				x.L(3, y+2, z, 4, y+2, z)
			} else {
				x.L(0, y, z, 7, y+2, z)
				x.G(0, y+1, z, 7, y+1, z)
			}
		}
		for _, i := range [2]int32{0, 7} {
			d := dirWest
			if i == 7 {
				d = dirEast
			}
			if r.open[d] {
				x.L(i, y, 2, i, y+2, 2)
				x.L(i, y, 5, i, y+2, 5)
				x.L(i, y+2, 3, i, y+2, 4)
			} else {
				x.L(i, y, 0, i, y+2, 7)
				x.G(i, y+1, 0, i, y+1, 7)
			}
		}
		r = up
	}
}

func (p *omPiece) doubleYZ(x omCtx) {
	b, a := p.room.conn[dirNorth], p.room
	bu, au := b.conn[dirUp], a.conn[dirUp]
	if p.floorLevel() {
		x.floor(0, 8, b.open[dirDown])
		x.floor(0, 0, a.open[dirDown])
	}
	if au.conn[dirUp] == nil {
		x.fillOnly(1, 8, 1, 6, 8, 7, omGray())
	}
	if bu.conn[dirUp] == nil {
		x.fillOnly(1, 8, 8, 6, 8, 14, omGray())
	}
	for y := int32(1); y <= 7; y++ {
		st := omLight()
		if y == 2 || y == 6 {
			st = omGray()
		}
		x.box(0, y, 0, 0, y, 15, st)
		x.box(7, y, 0, 7, y, 15, st)
		x.box(1, y, 0, 6, y, 0, st)
		x.box(1, y, 15, 6, y, 15, st)
	}
	for y := int32(1); y <= 7; y++ {
		st := omBlack()
		if y == 2 || y == 6 {
			st = omLamp()
		}
		x.box(3, y, 7, 4, y, 8, st)
	}
	if a.open[dirSouth] {
		x.water(3, 1, 0, 4, 2, 0)
	}
	if a.open[dirEast] {
		x.water(7, 1, 3, 7, 2, 4)
	}
	if a.open[dirWest] {
		x.water(0, 1, 3, 0, 2, 4)
	}
	if b.open[dirNorth] {
		x.water(3, 1, 15, 4, 2, 15)
	}
	if b.open[dirWest] {
		x.water(0, 1, 11, 0, 2, 12)
	}
	if b.open[dirEast] {
		x.water(7, 1, 11, 7, 2, 12)
	}
	if au.open[dirSouth] {
		x.water(3, 5, 0, 4, 6, 0)
	}
	if au.open[dirEast] {
		x.water(7, 5, 3, 7, 6, 4)
		x.L(5, 4, 2, 6, 4, 5)
		x.L(6, 1, 2, 6, 3, 2)
		x.L(6, 1, 5, 6, 3, 5)
	}
	if au.open[dirWest] {
		x.water(0, 5, 3, 0, 6, 4)
		x.L(1, 4, 2, 2, 4, 5)
		x.L(1, 1, 2, 1, 3, 2)
		x.L(1, 1, 5, 1, 3, 5)
	}
	if bu.open[dirNorth] {
		x.water(3, 5, 15, 4, 6, 15)
	}
	if bu.open[dirWest] {
		x.water(0, 5, 11, 0, 6, 12)
		x.L(1, 4, 10, 2, 4, 13)
		x.L(1, 1, 10, 1, 3, 10)
		x.L(1, 1, 13, 1, 3, 13)
	}
	if bu.open[dirEast] {
		x.water(7, 5, 11, 7, 6, 12)
		x.L(5, 4, 10, 6, 4, 13)
		x.L(6, 1, 10, 6, 3, 10)
		x.L(6, 1, 13, 6, 3, 13)
	}
}

func (p *omPiece) doubleZ(x omCtx) {
	b, a := p.room.conn[dirNorth], p.room
	if p.floorLevel() {
		x.floor(0, 8, b.open[dirDown])
		x.floor(0, 0, a.open[dirDown])
	}
	if a.conn[dirUp] == nil {
		x.fillOnly(1, 4, 1, 6, 4, 7, omGray())
	}
	if b.conn[dirUp] == nil {
		x.fillOnly(1, 4, 8, 6, 4, 14, omGray())
	}
	x.L(0, 3, 0, 0, 3, 15)
	x.L(7, 3, 0, 7, 3, 15)
	x.L(1, 3, 0, 7, 3, 0)
	x.L(1, 3, 15, 6, 3, 15)
	x.G(0, 2, 0, 0, 2, 15)
	x.G(7, 2, 0, 7, 2, 15)
	x.G(1, 2, 0, 7, 2, 0)
	x.G(1, 2, 15, 6, 2, 15)
	x.L(0, 1, 0, 0, 1, 15)
	x.L(7, 1, 0, 7, 1, 15)
	x.L(1, 1, 0, 7, 1, 0)
	x.L(1, 1, 15, 6, 1, 15)
	x.L(1, 1, 1, 1, 1, 2)
	x.L(6, 1, 1, 6, 1, 2)
	x.L(1, 3, 1, 1, 3, 2)
	x.L(6, 3, 1, 6, 3, 2)
	x.L(1, 1, 13, 1, 1, 14)
	x.L(6, 1, 13, 6, 1, 14)
	x.L(1, 3, 13, 1, 3, 14)
	x.L(6, 3, 13, 6, 3, 14)
	x.L(2, 1, 6, 2, 3, 6)
	x.L(5, 1, 6, 5, 3, 6)
	x.L(2, 1, 9, 2, 3, 9)
	x.L(5, 1, 9, 5, 3, 9)
	x.L(3, 2, 6, 4, 2, 6)
	x.L(3, 2, 9, 4, 2, 9)
	x.L(2, 2, 7, 2, 2, 8)
	x.L(5, 2, 7, 5, 2, 8)
	x.set(omLamp(), 2, 2, 5)
	x.set(omLamp(), 5, 2, 5)
	x.set(omLamp(), 2, 2, 10)
	x.set(omLamp(), 5, 2, 10)
	x.set(omLight(), 2, 3, 5)
	x.set(omLight(), 5, 3, 5)
	x.set(omLight(), 2, 3, 10)
	x.set(omLight(), 5, 3, 10)
	if a.open[dirSouth] {
		x.water(3, 1, 0, 4, 2, 0)
	}
	if a.open[dirEast] {
		x.water(7, 1, 3, 7, 2, 4)
	}
	if a.open[dirWest] {
		x.water(0, 1, 3, 0, 2, 4)
	}
	if b.open[dirNorth] {
		x.water(3, 1, 15, 4, 2, 15)
	}
	if b.open[dirWest] {
		x.water(0, 1, 11, 0, 2, 12)
	}
	if b.open[dirEast] {
		x.water(7, 1, 11, 7, 2, 12)
	}
}

func (p *omPiece) simple(x omCtx) {
	r, rnd := p.room, x.c.Random
	if p.floorLevel() {
		x.floor(0, 0, r.open[dirDown])
	}
	if r.conn[dirUp] == nil {
		x.fillOnly(1, 4, 1, 6, 4, 6, omGray())
	}
	pillar := p.design != 0 && rnd.NextBool() && !r.open[dirDown] && !r.open[dirUp] && r.countOpenings() > 1
	switch p.design {
	case 0:
		x.L(0, 1, 0, 2, 1, 2)
		x.L(0, 3, 0, 2, 3, 2)
		x.G(0, 2, 0, 0, 2, 2)
		x.G(1, 2, 0, 2, 2, 0)
		x.set(omLamp(), 1, 2, 1)
		x.L(5, 1, 0, 7, 1, 2)
		x.L(5, 3, 0, 7, 3, 2)
		x.G(7, 2, 0, 7, 2, 2)
		x.G(5, 2, 0, 6, 2, 0)
		x.set(omLamp(), 6, 2, 1)
		x.L(0, 1, 5, 2, 1, 7)
		x.L(0, 3, 5, 2, 3, 7)
		x.G(0, 2, 5, 0, 2, 7)
		x.G(1, 2, 7, 2, 2, 7)
		x.set(omLamp(), 1, 2, 6)
		x.L(5, 1, 5, 7, 1, 7)
		x.L(5, 3, 5, 7, 3, 7)
		x.G(7, 2, 5, 7, 2, 7)
		x.G(5, 2, 7, 6, 2, 7)
		x.set(omLamp(), 6, 2, 6)
		if r.open[dirSouth] {
			x.L(3, 3, 0, 4, 3, 0)
		} else {
			x.L(3, 3, 0, 4, 3, 1)
			x.G(3, 2, 0, 4, 2, 0)
			x.L(3, 1, 0, 4, 1, 1)
		}
		if r.open[dirNorth] {
			x.L(3, 3, 7, 4, 3, 7)
		} else {
			x.L(3, 3, 6, 4, 3, 7)
			x.G(3, 2, 7, 4, 2, 7)
			x.L(3, 1, 6, 4, 1, 7)
		}
		if r.open[dirWest] {
			x.L(0, 3, 3, 0, 3, 4)
		} else {
			x.L(0, 3, 3, 1, 3, 4)
			x.G(0, 2, 3, 0, 2, 4)
			x.L(0, 1, 3, 1, 1, 4)
		}
		if r.open[dirEast] {
			x.L(7, 3, 3, 7, 3, 4)
		} else {
			x.L(6, 3, 3, 7, 3, 4)
			x.G(7, 2, 3, 7, 2, 4)
			x.L(6, 1, 3, 7, 1, 4)
		}
	case 1:
		x.L(2, 1, 2, 2, 3, 2)
		x.L(2, 1, 5, 2, 3, 5)
		x.L(5, 1, 5, 5, 3, 5)
		x.L(5, 1, 2, 5, 3, 2)
		x.set(omLamp(), 2, 2, 2)
		x.set(omLamp(), 2, 2, 5)
		x.set(omLamp(), 5, 2, 5)
		x.set(omLamp(), 5, 2, 2)
		x.L(0, 1, 0, 1, 3, 0)
		x.L(0, 1, 1, 0, 3, 1)
		x.L(0, 1, 7, 1, 3, 7)
		x.L(0, 1, 6, 0, 3, 6)
		x.L(6, 1, 7, 7, 3, 7)
		x.L(7, 1, 6, 7, 3, 6)
		x.L(6, 1, 0, 7, 3, 0)
		x.L(7, 1, 1, 7, 3, 1)
		x.set(omGray(), 1, 2, 0)
		x.set(omGray(), 0, 2, 1)
		x.set(omGray(), 1, 2, 7)
		x.set(omGray(), 0, 2, 6)
		x.set(omGray(), 6, 2, 7)
		x.set(omGray(), 7, 2, 6)
		x.set(omGray(), 6, 2, 0)
		x.set(omGray(), 7, 2, 1)
		if !r.open[dirSouth] {
			x.L(1, 3, 0, 6, 3, 0)
			x.G(1, 2, 0, 6, 2, 0)
			x.L(1, 1, 0, 6, 1, 0)
		}
		if !r.open[dirNorth] {
			x.L(1, 3, 7, 6, 3, 7)
			x.G(1, 2, 7, 6, 2, 7)
			x.L(1, 1, 7, 6, 1, 7)
		}
		if !r.open[dirWest] {
			x.L(0, 3, 1, 0, 3, 6)
			x.G(0, 2, 1, 0, 2, 6)
			x.L(0, 1, 1, 0, 1, 6)
		}
		if !r.open[dirEast] {
			x.L(7, 3, 1, 7, 3, 6)
			x.G(7, 2, 1, 7, 2, 6)
			x.L(7, 1, 1, 7, 1, 6)
		}
	case 2:
		p.plainWalls(x)
		if r.open[dirSouth] {
			x.water(3, 1, 0, 4, 2, 0)
		}
		if r.open[dirNorth] {
			x.water(3, 1, 7, 4, 2, 7)
		}
		if r.open[dirWest] {
			x.water(0, 1, 3, 0, 2, 4)
		}
		if r.open[dirEast] {
			x.water(7, 1, 3, 7, 2, 4)
		}
	}
	if pillar {
		x.L(3, 1, 3, 4, 1, 4)
		x.G(3, 2, 3, 4, 2, 4)
		x.L(3, 3, 3, 4, 3, 4)
	}
}

// plainWalls is the light / dark / light ring with dark door frames
// of the simple room's third design and the simple top room.
func (p *omPiece) plainWalls(x omCtx) {
	x.L(0, 1, 0, 0, 1, 7)
	x.L(7, 1, 0, 7, 1, 7)
	x.L(1, 1, 0, 6, 1, 0)
	x.L(1, 1, 7, 6, 1, 7)
	x.B(0, 2, 0, 0, 2, 7)
	x.B(7, 2, 0, 7, 2, 7)
	x.B(1, 2, 0, 6, 2, 0)
	x.B(1, 2, 7, 6, 2, 7)
	x.L(0, 3, 0, 0, 3, 7)
	x.L(7, 3, 0, 7, 3, 7)
	x.L(1, 3, 0, 6, 3, 0)
	x.L(1, 3, 7, 6, 3, 7)
	x.B(0, 1, 3, 0, 2, 4)
	x.B(7, 1, 3, 7, 2, 4)
	x.B(3, 1, 0, 4, 2, 0)
	x.B(3, 1, 7, 4, 2, 7)
}

func (p *omPiece) simpleTop(x omCtx) {
	r, rnd := p.room, x.c.Random
	if p.floorLevel() {
		x.floor(0, 0, r.open[dirDown])
	}
	if r.conn[dirUp] == nil {
		x.fillOnly(1, 4, 1, 6, 4, 6, omGray())
	}
	sponge := blockNamed("wet_sponge")
	for i := int32(1); i <= 6; i++ {
		for k := int32(1); k <= 6; k++ {
			if rnd.NextIntN(3) != 0 {
				y := int32(2)
				if rnd.NextIntN(4) != 0 {
					y = 3
				}
				x.box(i, y, k, i, 3, k, sponge)
			}
		}
	}
	p.plainWalls(x)
	if r.open[dirSouth] {
		x.water(3, 1, 0, 4, 2, 0)
	}
}

func (p *omPiece) wing(x omCtx) {
	if p.design == 0 {
		for i := int32(0); i < 4; i++ {
			x.L(10-i, 3-i, 20-i, 12+i, 3-i, 20)
		}
		x.L(7, 0, 6, 15, 0, 16)
		x.L(6, 0, 6, 6, 3, 20)
		x.L(16, 0, 6, 16, 3, 20)
		x.L(7, 1, 7, 7, 1, 20)
		x.L(15, 1, 7, 15, 1, 20)
		x.L(7, 1, 6, 9, 3, 6)
		x.L(13, 1, 6, 15, 3, 6)
		x.L(8, 1, 7, 9, 1, 7)
		x.L(13, 1, 7, 14, 1, 7)
		x.L(9, 0, 5, 13, 0, 5)
		x.B(10, 0, 7, 12, 0, 7)
		x.B(8, 0, 10, 8, 0, 12)
		x.B(14, 0, 10, 14, 0, 12)
		for z := int32(18); z >= 7; z -= 3 {
			x.set(omLamp(), 6, 3, z)
			x.set(omLamp(), 16, 3, z)
		}
		x.set(omLamp(), 10, 0, 10)
		x.set(omLamp(), 12, 0, 10)
		x.set(omLamp(), 10, 0, 12)
		x.set(omLamp(), 12, 0, 12)
		x.set(omLamp(), 8, 3, 6)
		x.set(omLamp(), 14, 3, 6)
		x.set(omLight(), 4, 2, 4)
		x.set(omLamp(), 4, 1, 4)
		x.set(omLight(), 4, 0, 4)
		x.set(omLight(), 18, 2, 4)
		x.set(omLamp(), 18, 1, 4)
		x.set(omLight(), 18, 0, 4)
		x.set(omLight(), 4, 2, 18)
		x.set(omLamp(), 4, 1, 18)
		x.set(omLight(), 4, 0, 18)
		x.set(omLight(), 18, 2, 18)
		x.set(omLamp(), 18, 1, 18)
		x.set(omLight(), 18, 0, 18)
		x.set(omLight(), 9, 7, 20)
		x.set(omLight(), 13, 7, 20)
		x.L(6, 0, 21, 7, 4, 21)
		x.L(15, 0, 21, 16, 4, 21)
		return
	}
	x.L(9, 3, 18, 13, 3, 20)
	x.L(9, 0, 18, 9, 2, 18)
	x.L(13, 0, 18, 13, 2, 18)
	for _, i := range [2]int32{9, 13} {
		x.set(omLight(), i, 6, 20)
		x.set(omLamp(), i, 5, 20)
		x.set(omLight(), i, 4, 20)
	}
	x.L(7, 3, 7, 15, 3, 14)
	for _, i := range [2]int32{10, 12} {
		x.L(i, 0, 10, i, 6, 10)
		x.L(i, 0, 12, i, 6, 12)
		x.set(omLamp(), i, 0, 10)
		x.set(omLamp(), i, 0, 12)
		x.set(omLamp(), i, 4, 10)
		x.set(omLamp(), i, 4, 12)
	}
	for _, i := range [2]int32{8, 14} {
		x.L(i, 0, 7, i, 2, 7)
		x.L(i, 0, 14, i, 2, 14)
	}
	x.B(8, 3, 8, 8, 3, 13)
	x.B(14, 3, 8, 14, 3, 13)
}

func (p *omPiece) penthouse(x omCtx) {
	x.L(2, -1, 2, 11, -1, 11)
	x.G(0, -1, 0, 1, -1, 11)
	x.G(12, -1, 0, 13, -1, 11)
	x.G(2, -1, 0, 11, -1, 1)
	x.G(2, -1, 12, 11, -1, 13)
	x.L(0, 0, 0, 0, 0, 13)
	x.L(13, 0, 0, 13, 0, 13)
	x.L(1, 0, 0, 12, 0, 0)
	x.L(1, 0, 13, 12, 0, 13)
	for i := int32(2); i <= 11; i += 3 {
		x.set(omLamp(), 0, 0, i)
		x.set(omLamp(), 13, 0, i)
		x.set(omLamp(), i, 0, 0)
	}
	x.L(2, 0, 3, 4, 0, 9)
	x.L(9, 0, 3, 11, 0, 9)
	x.L(4, 0, 9, 9, 0, 11)
	x.set(omLight(), 5, 0, 8)
	x.set(omLight(), 8, 0, 8)
	x.set(omLight(), 10, 0, 10)
	x.set(omLight(), 3, 0, 10)
	x.B(3, 0, 3, 3, 0, 7)
	x.B(10, 0, 3, 10, 0, 7)
	x.B(6, 0, 10, 7, 0, 10)
	for _, i := range [2]int32{3, 10} {
		for k := int32(2); k <= 8; k += 3 {
			x.L(i, 0, k, i, 2, k)
		}
	}
	x.L(5, 0, 10, 5, 2, 10)
	x.L(8, 0, 10, 8, 2, 10)
	x.B(6, -1, 7, 7, -1, 8)
	x.water(6, -1, 3, 7, -1, 4)
}
