package worldgen

import (
	"fmt"
	"strings"

	"mc-go/internal/world"
)

// Woodland mansions (WoodlandMansionStructure + WoodlandMansionPieces),
// measured from the 26.2 bytecode:
//
//   - findGenerationPoint: a random rotation, the lowest WORLD_SURFACE_WG
//     corner of a 5 x 5 box (toward the rotation) at the chunk's block
//     (7, 7); no mansion below y 60.
//   - MansionGrid: an 11 x 11 grid seeded with the entrance, random-walk
//     corridors (recursiveCorridor), edge cleanup, then rooms identified
//     on two floors (shuffled room cells grown to 2x2, 1x2 or 1x1 with a
//     door toward a corridor) and a third floor grown from a 1x2 room
//     with stairs.
//   - MansionPiecePlacer: outer walls traversed per floor, roofs,
//     corridor floors and carpets, inner walls and doors and the rooms'
//     templates (woodland_mansion/*) drawn from the floor collections.
//   - pieces are TemplateStructurePieces (structure blocks ignored,
//     entities ignored); data markers make chests (facing by marker)
//     with the woodland_mansion loot table and turn the mob markers to
//     air (the evokers, vindicators and allays themselves are not
//     spawned; the allay count's region-random draw is made, the mobs'
//     finalizeSpawn draws are not).
//   - afterPlace: cobblestone filled down from under every non-air cell
//     of the pieces' bottom layer through air and liquids.

const (
	mgCorridor   = 1
	mgRoom       = 2
	mgStartRoom  = 3
	mgTestRoom   = 4
	mgBlocked    = 5
	mgRoom1x1    = 65536
	mgRoom1x2    = 131072
	mgRoom2x2    = 262144
	mgOriginFlag = 1048576
	mgDoorFlag   = 2097152
	mgStairsFlag = 4194304
	mgCorrFlag   = 8388608
	mgTypeMask   = 983040
	mgIDMask     = 65535
)

type simpleGrid struct {
	grid          [][]int32
	width, height int32
	outside       int32
}

func newSimpleGrid(w, h, outside int32) *simpleGrid {
	g := &simpleGrid{width: w, height: h, outside: outside, grid: make([][]int32, w)}
	for i := range g.grid {
		g.grid[i] = make([]int32, h)
	}
	return g
}

func (g *simpleGrid) set(x, y, v int32) {
	if x >= 0 && x < g.width && y >= 0 && y < g.height {
		g.grid[x][y] = v
	}
}

func (g *simpleGrid) setBox(x0, y0, x1, y1, v int32) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			g.set(x, y, v)
		}
	}
}

func (g *simpleGrid) get(x, y int32) int32 {
	if x >= 0 && x < g.width && y >= 0 && y < g.height {
		return g.grid[x][y]
	}
	return g.outside
}

func (g *simpleGrid) setif(x, y, if_, v int32) {
	if g.get(x, y) == if_ {
		g.set(x, y, v)
	}
}

func (g *simpleGrid) edgesTo(x, y, v int32) bool {
	return g.get(x-1, y) == v || g.get(x+1, y) == v || g.get(x, y+1) == v || g.get(x, y-1) == v
}

func mgIsHouse(g *simpleGrid, x, y int32) bool {
	v := g.get(x, y)
	return v == 1 || v == 2 || v == 3 || v == 4
}

func ccw(d int) int { return clockwise(clockwise(clockwise(d))) }

type mansionGrid struct {
	r                 Random
	base, third       *simpleGrid
	rooms             [3]*simpleGrid
	entranceX, entryY int32
}

func newMansionGrid(r Random) *mansionGrid {
	m := &mansionGrid{r: r, entranceX: 7, entryY: 4}
	ex, ey := m.entranceX, m.entryY
	b := newSimpleGrid(11, 11, 5)
	m.base = b
	b.setBox(ex, ey, ex+1, ey+1, 3)
	b.setBox(ex-1, ey, ex-1, ey+1, 2)
	b.setBox(ex+2, ey-2, ex+3, ey+3, 5)
	b.setBox(ex+1, ey-2, ex+1, ey-1, 1)
	b.setBox(ex+1, ey+2, ex+1, ey+3, 1)
	b.set(ex-1, ey-1, 1)
	b.set(ex-1, ey+2, 1)
	b.setBox(0, 0, 11, 1, 5)
	b.setBox(0, 9, 11, 11, 5)
	m.recursiveCorridor(b, ex, ey-2, dirWest, 6)
	m.recursiveCorridor(b, ex, ey+3, dirWest, 6)
	m.recursiveCorridor(b, ex-2, ey-1, dirWest, 3)
	m.recursiveCorridor(b, ex-2, ey+2, dirWest, 3)
	for mgCleanEdges(b) {
	}
	for i := range m.rooms {
		m.rooms[i] = newSimpleGrid(11, 11, 5)
	}
	m.identifyRooms(b, m.rooms[0])
	m.identifyRooms(b, m.rooms[1])
	m.rooms[0].setBox(ex+1, ey, ex+1, ey+1, mgCorrFlag)
	m.rooms[1].setBox(ex+1, ey, ex+1, ey+1, mgCorrFlag)
	m.third = newSimpleGrid(b.width, b.height, 5)
	m.setupThirdFloor()
	m.identifyRooms(m.third, m.rooms[2])
	return m
}

func (m *mansionGrid) isRoomID(floor int, x, y, id int32) bool {
	return m.rooms[floor].get(x, y)&mgIDMask == id
}

func (m *mansionGrid) get1x2RoomDirection(x, y int32, floor int, id int32) int {
	for _, d := range horizontalDirs {
		if m.isRoomID(floor, x+stepX(d), y+stepZ(d), id) {
			return d
		}
	}
	return -1
}

func (m *mansionGrid) recursiveCorridor(g *simpleGrid, x, y int32, d int, depth int) {
	if depth <= 0 {
		return
	}
	g.set(x, y, 1)
	g.setif(x+stepX(d), y+stepZ(d), 0, 1)
	for i := 0; i < 8; i++ {
		nd := dirFrom2D(m.r.NextIntN(4))
		if nd == oppositeDir(d) || nd == dirEast && m.r.NextBool() {
			continue
		}
		nx, ny := x+stepX(d), y+stepZ(d)
		if g.get(nx+stepX(nd), ny+stepZ(nd)) == 0 && g.get(nx+stepX(nd)*2, ny+stepZ(nd)*2) == 0 {
			m.recursiveCorridor(g, x+stepX(d)+stepX(nd), y+stepZ(d)+stepZ(nd), nd, depth-1)
			break
		}
	}
	cw, cc := clockwise(d), ccw(d)
	g.setif(x+stepX(cw), y+stepZ(cw), 0, 2)
	g.setif(x+stepX(cc), y+stepZ(cc), 0, 2)
	g.setif(x+stepX(d)+stepX(cw), y+stepZ(d)+stepZ(cw), 0, 2)
	g.setif(x+stepX(d)+stepX(cc), y+stepZ(d)+stepZ(cc), 0, 2)
	g.setif(x+stepX(d)*2, y+stepZ(d)*2, 0, 2)
	g.setif(x+stepX(cw)*2, y+stepZ(cw)*2, 0, 2)
	g.setif(x+stepX(cc)*2, y+stepZ(cc)*2, 0, 2)
}

func mgCleanEdges(g *simpleGrid) bool {
	changed := false
	cnt := func(ps ...[2]int32) int {
		n := 0
		for _, p := range ps {
			if mgIsHouse(g, p[0], p[1]) {
				n++
			}
		}
		return n
	}
	for y := int32(0); y < g.height; y++ {
		for x := int32(0); x < g.width; x++ {
			if g.get(x, y) != 0 {
				continue
			}
			n := cnt([2]int32{x + 1, y}, [2]int32{x - 1, y}, [2]int32{x, y + 1}, [2]int32{x, y - 1})
			if n >= 3 {
				g.set(x, y, 2)
				changed = true
			} else if n == 2 {
				if cnt([2]int32{x + 1, y + 1}, [2]int32{x - 1, y + 1}, [2]int32{x + 1, y - 1}, [2]int32{x - 1, y - 1}) <= 1 {
					g.set(x, y, 2)
					changed = true
				}
			}
		}
	}
	return changed
}

func (m *mansionGrid) setupThirdFloor() {
	r1 := m.rooms[1]
	var cand [][2]int32
	for y := int32(0); y < m.third.height; y++ {
		for x := int32(0); x < m.third.width; x++ {
			v := r1.get(x, y)
			if v&mgTypeMask == mgRoom1x2 && v&mgDoorFlag == mgDoorFlag {
				cand = append(cand, [2]int32{x, y})
			}
		}
	}
	if len(cand) == 0 {
		m.third.setBox(0, 0, m.third.width, m.third.height, 5)
		return
	}
	p := cand[m.r.NextIntN(int32(len(cand)))]
	v := r1.get(p[0], p[1])
	r1.set(p[0], p[1], v|mgStairsFlag)
	d := m.get1x2RoomDirection(p[0], p[1], 1, v&mgIDMask)
	x6, y7 := p[0]+stepX(d), p[1]+stepZ(d)
	for y := int32(0); y < m.third.height; y++ {
		for x := int32(0); x < m.third.width; x++ {
			switch {
			case !mgIsHouse(m.base, x, y):
				m.third.set(x, y, 5)
			case x == p[0] && y == p[1]:
				m.third.set(x, y, 3)
			case x == x6 && y == y7:
				m.third.set(x, y, 3)
				m.rooms[2].set(x, y, mgCorrFlag)
			}
		}
	}
	var dirs []int
	for _, hd := range horizontalDirs {
		if m.third.get(x6+stepX(hd), y7+stepZ(hd)) == 0 {
			dirs = append(dirs, hd)
		}
	}
	if len(dirs) == 0 {
		m.third.setBox(0, 0, m.third.width, m.third.height, 5)
		r1.set(p[0], p[1], v)
		return
	}
	nd := dirs[m.r.NextIntN(int32(len(dirs)))]
	m.recursiveCorridor(m.third, x6+stepX(nd), y7+stepZ(nd), nd, 4)
	for mgCleanEdges(m.third) {
	}
}

func (m *mansionGrid) identifyRooms(g, rooms *simpleGrid) {
	var cells [][2]int32
	for y := int32(0); y < g.height; y++ {
		for x := int32(0); x < g.width; x++ {
			if g.get(x, y) == 2 {
				cells = append(cells, [2]int32{x, y})
			}
		}
	}
	for i := int32(len(cells)); i > 1; i-- {
		j := m.r.NextIntN(i)
		cells[i-1], cells[j] = cells[j], cells[i-1]
	}
	id := int32(10)
	for _, c := range cells {
		x, y := c[0], c[1]
		if rooms.get(x, y) != 0 {
			continue
		}
		x0, x1, y0, y1 := x, x, y, y
		typ := int32(mgRoom1x1)
		free := func(px, py int32) bool { return rooms.get(px, py) == 0 && g.get(px, py) == 2 }
		switch {
		case rooms.get(x+1, y) == 0 && rooms.get(x, y+1) == 0 && rooms.get(x+1, y+1) == 0 && g.get(x+1, y) == 2 && g.get(x, y+1) == 2 && g.get(x+1, y+1) == 2:
			x1++
			y1++
			typ = mgRoom2x2
		case rooms.get(x-1, y) == 0 && rooms.get(x, y+1) == 0 && rooms.get(x-1, y+1) == 0 && g.get(x-1, y) == 2 && g.get(x, y+1) == 2 && g.get(x-1, y+1) == 2:
			x0--
			y1++
			typ = mgRoom2x2
		case rooms.get(x-1, y) == 0 && rooms.get(x, y-1) == 0 && rooms.get(x-1, y-1) == 0 && g.get(x-1, y) == 2 && g.get(x, y-1) == 2 && g.get(x-1, y-1) == 2:
			x0--
			y0--
			typ = mgRoom2x2
		case free(x+1, y):
			x1++
			typ = mgRoom1x2
		case free(x, y+1):
			y1++
			typ = mgRoom1x2
		case free(x-1, y):
			x0--
			typ = mgRoom1x2
		case free(x, y-1):
			y0--
			typ = mgRoom1x2
		}
		dx := x1
		if m.r.NextBool() {
			dx = x0
		}
		dy := y1
		if m.r.NextBool() {
			dy = y0
		}
		other := func(v, a, b int32) int32 {
			if v == a {
				return b
			}
			return a
		}
		door := int32(mgDoorFlag)
		if !g.edgesTo(dx, dy, 1) {
			dx = other(dx, x0, x1)
			dy = other(dy, y0, y1)
			if !g.edgesTo(dx, dy, 1) {
				dy = other(dy, y0, y1)
				if !g.edgesTo(dx, dy, 1) {
					dx = other(dx, x0, x1)
					dy = other(dy, y0, y1)
					if !g.edgesTo(dx, dy, 1) {
						door = 0
						dx, dy = x0, y0
					}
				}
			}
		}
		for yy := y0; yy <= y1; yy++ {
			for xx := x0; xx <= x1; xx++ {
				if xx == dx && yy == dy {
					rooms.set(xx, yy, mgOriginFlag|door|typ|id)
				} else {
					rooms.set(xx, yy, typ|id)
				}
			}
		}
		id++
	}
}

// Rotation helpers (Rotation.rotate(Direction), getRotated,
// BlockPos.rotate, BlockPos.relative).
func rotDir(rot, d int) int {
	for i := 0; i < rot; i++ {
		d = clockwise(d)
	}
	return d
}

func rotAdd(a, b int) int { return (a + b) % 4 }

func relative(p BlockPos, d int, n int32) BlockPos {
	o := dirOffsets[d]
	return BlockPos{p[0] + o[0]*n, p[1] + o[1]*n, p[2] + o[2]*n}
}

func rotatePos(p BlockPos, rot int) BlockPos {
	switch rot {
	case rotCW90:
		return BlockPos{-p[2], p[1], p[0]}
	case rotCW180:
		return BlockPos{-p[0], p[1], -p[2]}
	case rotCCW90:
		return BlockPos{p[2], p[1], -p[0]}
	}
	return p
}

// zeroPositionSized is StructureTemplate.getZeroPositionWithTransform
// with explicit sizes.
func zeroPositionSized(p BlockPos, mirror, rot int, sx, sz int32) BlockPos {
	sx, sz = sx-1, sz-1
	a, b := int32(0), int32(0)
	if mirror == mirFrontBack {
		a = sx
	}
	if mirror == mirLeftRight {
		b = sz
	}
	switch rot {
	case rotCW90:
		return offset(p, sz-b, 0, a)
	case rotCW180:
		return offset(p, sx-a, 0, sz-b)
	case rotCCW90:
		return offset(p, b, 0, sx-a)
	}
	return offset(p, a, 0, b)
}

// mansionRooms is a floor's FloorRoomCollection.
type mansionRooms struct{ floor int }

func (f mansionRooms) get1x1(r Random) string {
	if f.floor == 0 {
		return fmt.Sprint("1x1_a", r.NextIntN(5)+1)
	}
	return fmt.Sprint("1x1_b", r.NextIntN(5)+1)
}
func (f mansionRooms) get1x1Secret(r Random) string { return fmt.Sprint("1x1_as", r.NextIntN(4)+1) }
func (f mansionRooms) get1x2Side(r Random, stairs bool) string {
	if f.floor == 0 {
		return fmt.Sprint("1x2_a", r.NextIntN(9)+1)
	}
	if stairs {
		return "1x2_c_stairs"
	}
	return fmt.Sprint("1x2_c", r.NextIntN(4)+1)
}
func (f mansionRooms) get1x2Front(r Random, stairs bool) string {
	if f.floor == 0 {
		return fmt.Sprint("1x2_b", r.NextIntN(5)+1)
	}
	if stairs {
		return "1x2_d_stairs"
	}
	return fmt.Sprint("1x2_d", r.NextIntN(5)+1)
}
func (f mansionRooms) get1x2Secret(r Random) string {
	if f.floor == 0 {
		return fmt.Sprint("1x2_s", r.NextIntN(2)+1)
	}
	return fmt.Sprint("1x2_se", r.NextIntN(1)+1)
}
func (f mansionRooms) get2x2(r Random) string {
	if f.floor == 0 {
		return fmt.Sprint("2x2_a", r.NextIntN(4)+1)
	}
	return fmt.Sprint("2x2_b", r.NextIntN(5)+1)
}

type mansionPlacement struct {
	pos      BlockPos
	rot      int
	wallType string
}

type mansionPlacer struct {
	r              Random
	startX, startY int32
	out            []StructurePiece
}

func (m *mansionPlacer) add(name string, pos BlockPos, rot, mirror int) {
	m.out = append(m.out, newMansionPiece(name, pos, rot, mirror))
}

func (m *mansionPlacer) traverseWallPiece(d *mansionPlacement) {
	m.add(d.wallType, relative(d.pos, rotDir(d.rot, dirEast), 7), d.rot, mirNone)
	d.pos = relative(d.pos, rotDir(d.rot, dirSouth), 8)
}

func (m *mansionPlacer) traverseTurn(d *mansionPlacement) {
	d.pos = relative(d.pos, rotDir(d.rot, dirSouth), -1)
	m.add("wall_corner", d.pos, d.rot, mirNone)
	d.pos = relative(d.pos, rotDir(d.rot, dirSouth), -7)
	d.pos = relative(d.pos, rotDir(d.rot, dirWest), -6)
	d.rot = rotAdd(d.rot, rotCW90)
}

func (m *mansionPlacer) traverseInnerTurn(d *mansionPlacement) {
	d.pos = relative(d.pos, rotDir(d.rot, dirSouth), 6)
	d.pos = relative(d.pos, rotDir(d.rot, dirEast), 8)
	d.rot = rotAdd(d.rot, rotCCW90)
}

func (m *mansionPlacer) traverseOuterWalls(d *mansionPlacement, g *simpleGrid, dir int, sx, sy, ex, ey int32) {
	x, y := sx, sy
	start := dir
	for {
		if !mgIsHouse(g, x+stepX(dir), y+stepZ(dir)) {
			m.traverseTurn(d)
			dir = clockwise(dir)
			if x != ex || y != ey || start != dir {
				m.traverseWallPiece(d)
			}
		} else if mgIsHouse(g, x+stepX(dir), y+stepZ(dir)) && mgIsHouse(g, x+stepX(dir)+stepX(ccw(dir)), y+stepZ(dir)+stepZ(ccw(dir))) {
			m.traverseInnerTurn(d)
			x += stepX(dir)
			y += stepZ(dir)
			dir = ccw(dir)
		} else {
			x += stepX(dir)
			y += stepZ(dir)
			if x != ex || y != ey || start != dir {
				m.traverseWallPiece(d)
			}
		}
		if x == ex && y == ey && start == dir {
			return
		}
	}
}

func (m *mansionPlacer) cell(base BlockPos, rot int, x, y, xo int32) BlockPos {
	p := relative(base, rotDir(rot, dirSouth), 8+(y-m.startY)*8)
	return relative(p, rotDir(rot, dirEast), xo+(x-m.startX)*8)
}

func (m *mansionPlacer) createRoof(pos BlockPos, rot int, g, above *simpleGrid) {
	R := func(d int) int { return rotDir(rot, d) }
	for y := int32(0); y < g.height; y++ {
		for x := int32(0); x < g.width; x++ {
			p := m.cell(pos, rot, x, y, 0)
			covered := above != nil && mgIsHouse(above, x, y)
			if !mgIsHouse(g, x, y) || covered {
				continue
			}
			m.add("roof", offset(p, 0, 3, 0), rot, mirNone)
			if !mgIsHouse(g, x+1, y) {
				m.add("roof_front", relative(p, R(dirEast), 6), rot, mirNone)
			}
			if !mgIsHouse(g, x-1, y) {
				q := relative(relative(p, R(dirEast), 0), R(dirSouth), 7)
				m.add("roof_front", q, rotAdd(rot, rotCW180), mirNone)
			}
			if !mgIsHouse(g, x, y-1) {
				m.add("roof_front", relative(p, R(dirWest), 1), rotAdd(rot, rotCCW90), mirNone)
			}
			if !mgIsHouse(g, x, y+1) {
				q := relative(relative(p, R(dirEast), 6), R(dirSouth), 6)
				m.add("roof_front", q, rotAdd(rot, rotCW90), mirNone)
			}
		}
	}
	if above != nil {
		for y := int32(0); y < g.height; y++ {
			for x := int32(0); x < g.width; x++ {
				p := m.cell(pos, rot, x, y, 0)
				covered := mgIsHouse(above, x, y)
				if !mgIsHouse(g, x, y) || !covered {
					continue
				}
				if !mgIsHouse(g, x+1, y) {
					m.add("small_wall", relative(p, R(dirEast), 7), rot, mirNone)
				}
				if !mgIsHouse(g, x-1, y) {
					q := relative(relative(p, R(dirWest), 1), R(dirSouth), 6)
					m.add("small_wall", q, rotAdd(rot, rotCW180), mirNone)
				}
				if !mgIsHouse(g, x, y-1) {
					q := relative(relative(p, R(dirWest), 0), R(dirNorth), 1)
					m.add("small_wall", q, rotAdd(rot, rotCCW90), mirNone)
				}
				if !mgIsHouse(g, x, y+1) {
					q := relative(relative(p, R(dirEast), 6), R(dirSouth), 7)
					m.add("small_wall", q, rotAdd(rot, rotCW90), mirNone)
				}
				if !mgIsHouse(g, x+1, y) {
					if !mgIsHouse(g, x, y-1) {
						q := relative(relative(p, R(dirEast), 7), R(dirNorth), 2)
						m.add("small_wall_corner", q, rot, mirNone)
					}
					if !mgIsHouse(g, x, y+1) {
						q := relative(relative(p, R(dirEast), 8), R(dirSouth), 7)
						m.add("small_wall_corner", q, rotAdd(rot, rotCW90), mirNone)
					}
				}
				if !mgIsHouse(g, x-1, y) {
					if !mgIsHouse(g, x, y-1) {
						q := relative(relative(p, R(dirWest), 2), R(dirNorth), 1)
						m.add("small_wall_corner", q, rotAdd(rot, rotCCW90), mirNone)
					}
					if !mgIsHouse(g, x, y+1) {
						q := relative(relative(p, R(dirWest), 1), R(dirSouth), 8)
						m.add("small_wall_corner", q, rotAdd(rot, rotCW180), mirNone)
					}
				}
			}
		}
	}
	for y := int32(0); y < g.height; y++ {
		for x := int32(0); x < g.width; x++ {
			p := m.cell(pos, rot, x, y, 0)
			covered := above != nil && mgIsHouse(above, x, y)
			if !mgIsHouse(g, x, y) || covered {
				continue
			}
			if !mgIsHouse(g, x+1, y) {
				q := relative(p, R(dirEast), 6)
				if !mgIsHouse(g, x, y+1) {
					m.add("roof_corner", relative(q, R(dirSouth), 6), rot, mirNone)
				} else if mgIsHouse(g, x+1, y+1) {
					m.add("roof_inner_corner", relative(q, R(dirSouth), 5), rot, mirNone)
				}
				if !mgIsHouse(g, x, y-1) {
					m.add("roof_corner", q, rotAdd(rot, rotCCW90), mirNone)
				} else if mgIsHouse(g, x+1, y-1) {
					q2 := relative(relative(p, R(dirEast), 9), R(dirNorth), 2)
					m.add("roof_inner_corner", q2, rotAdd(rot, rotCW90), mirNone)
				}
			}
			if !mgIsHouse(g, x-1, y) {
				q := relative(relative(p, R(dirEast), 0), R(dirSouth), 0)
				if !mgIsHouse(g, x, y+1) {
					m.add("roof_corner", relative(q, R(dirSouth), 6), rotAdd(rot, rotCW90), mirNone)
				} else if mgIsHouse(g, x-1, y+1) {
					q2 := relative(relative(q, R(dirSouth), 8), R(dirWest), 3)
					m.add("roof_inner_corner", q2, rotAdd(rot, rotCCW90), mirNone)
				}
				if !mgIsHouse(g, x, y-1) {
					m.add("roof_corner", q, rotAdd(rot, rotCW180), mirNone)
				} else if mgIsHouse(g, x-1, y-1) {
					m.add("roof_inner_corner", relative(q, R(dirSouth), 1), rotAdd(rot, rotCW180), mirNone)
				}
			}
		}
	}
}

func (m *mansionPlacer) addRoom1x1(pos BlockPos, rot, d int, rc mansionRooms) {
	r := rotNone
	name := rc.get1x1(m.r)
	switch d {
	case dirEast:
	case dirNorth:
		r = rotAdd(r, rotCCW90)
	case dirWest:
		r = rotAdd(r, rotCW180)
	case dirSouth:
		r = rotAdd(r, rotCW90)
	default:
		name = rc.get1x1Secret(m.r)
	}
	z := zeroPositionSized(BlockPos{1, 0, 0}, mirNone, r, 7, 7)
	r = rotAdd(r, rot)
	z = rotatePos(z, rot)
	m.add(name, offset(pos, z[0], 0, z[2]), r, mirNone)
}

func (m *mansionPlacer) addRoom1x2(pos BlockPos, rot, d, door int, rc mansionRooms, stairs bool) {
	R := func(dd int) int { return rotDir(rot, dd) }
	switch {
	case door == dirEast && d == dirSouth:
		m.add(rc.get1x2Side(m.r, stairs), relative(pos, R(dirEast), 1), rot, mirNone)
	case door == dirEast && d == dirNorth:
		m.add(rc.get1x2Side(m.r, stairs), relative(relative(pos, R(dirEast), 1), R(dirSouth), 6), rot, mirLeftRight)
	case door == dirWest && d == dirNorth:
		m.add(rc.get1x2Side(m.r, stairs), relative(relative(pos, R(dirEast), 7), R(dirSouth), 6), rotAdd(rot, rotCW180), mirNone)
	case door == dirWest && d == dirSouth:
		m.add(rc.get1x2Side(m.r, stairs), relative(pos, R(dirEast), 7), rot, mirFrontBack)
	case door == dirSouth && d == dirEast:
		m.add(rc.get1x2Side(m.r, stairs), relative(pos, R(dirEast), 1), rotAdd(rot, rotCW90), mirLeftRight)
	case door == dirSouth && d == dirWest:
		m.add(rc.get1x2Side(m.r, stairs), relative(pos, R(dirEast), 7), rotAdd(rot, rotCW90), mirNone)
	case door == dirNorth && d == dirWest:
		m.add(rc.get1x2Side(m.r, stairs), relative(relative(pos, R(dirEast), 7), R(dirSouth), 6), rotAdd(rot, rotCW90), mirFrontBack)
	case door == dirNorth && d == dirEast:
		m.add(rc.get1x2Side(m.r, stairs), relative(relative(pos, R(dirEast), 1), R(dirSouth), 6), rotAdd(rot, rotCCW90), mirNone)
	case door == dirSouth && d == dirNorth:
		m.add(rc.get1x2Front(m.r, stairs), relative(relative(pos, R(dirEast), 1), R(dirNorth), 8), rot, mirNone)
	case door == dirNorth && d == dirSouth:
		m.add(rc.get1x2Front(m.r, stairs), relative(relative(pos, R(dirEast), 7), R(dirSouth), 14), rotAdd(rot, rotCW180), mirNone)
	case door == dirWest && d == dirEast:
		m.add(rc.get1x2Front(m.r, stairs), relative(pos, R(dirEast), 15), rotAdd(rot, rotCW90), mirNone)
	case door == dirEast && d == dirWest:
		m.add(rc.get1x2Front(m.r, stairs), relative(relative(pos, R(dirWest), 7), R(dirSouth), 6), rotAdd(rot, rotCCW90), mirNone)
	case door == dirUp && d == dirEast:
		m.add(rc.get1x2Secret(m.r), relative(pos, R(dirEast), 15), rotAdd(rot, rotCW90), mirNone)
	case door == dirUp && d == dirSouth:
		m.add(rc.get1x2Secret(m.r), relative(relative(pos, R(dirEast), 1), R(dirNorth), 0), rot, mirNone)
	}
}

func (m *mansionPlacer) addRoom2x2(pos BlockPos, rot, d, door int, rc mansionRooms) {
	ox, oz := int32(0), int32(0)
	r := rot
	mir := mirNone
	switch {
	case door == dirEast && d == dirSouth:
		ox = -7
	case door == dirEast && d == dirNorth:
		ox, oz, mir = -7, 6, mirLeftRight
	case door == dirNorth && d == dirEast:
		ox, oz, r = 1, 14, rotAdd(rot, rotCCW90)
	case door == dirNorth && d == dirWest:
		ox, oz, r, mir = 7, 14, rotAdd(rot, rotCCW90), mirLeftRight
	case door == dirSouth && d == dirWest:
		ox, oz, r = 7, -8, rotAdd(rot, rotCW90)
	case door == dirSouth && d == dirEast:
		ox, oz, r, mir = 1, -8, rotAdd(rot, rotCW90), mirLeftRight
	case door == dirWest && d == dirNorth:
		ox, oz, r = 15, 6, rotAdd(rot, rotCW180)
	case door == dirWest && d == dirSouth:
		ox, mir = 15, mirFrontBack
	}
	p := relative(relative(pos, rotDir(rot, dirEast), ox), rotDir(rot, dirSouth), oz)
	m.add(rc.get2x2(m.r), p, r, mir)
}

func (m *mansionPlacer) createMansion(pos BlockPos, rot int, g *mansionGrid) {
	d5 := &mansionPlacement{pos: pos, rot: rot, wallType: "wall_flat"}
	d6 := &mansionPlacement{}
	m.add("entrance", relative(d5.pos, rotDir(d5.rot, dirWest), 9), d5.rot, mirNone)
	d5.pos = relative(d5.pos, rotDir(d5.rot, dirSouth), 16)
	d6.pos, d6.rot, d6.wallType = offset(d5.pos, 0, 8, 0), d5.rot, "wall_window"
	base, third := g.base, g.third
	m.startX, m.startY = g.entranceX+1, g.entryY+1
	ex, ey := g.entranceX+1, g.entryY
	m.traverseOuterWalls(d5, base, dirSouth, m.startX, m.startY, ex, ey)
	m.traverseOuterWalls(d6, base, dirSouth, m.startX, m.startY, ex, ey)
	d11 := &mansionPlacement{pos: offset(d5.pos, 0, 19, 0), rot: d5.rot, wallType: "wall_window"}
	found := false
	for y := int32(0); y < third.height && !found; y++ {
		for x := third.width - 1; x >= 0 && !found; x-- {
			if mgIsHouse(third, x, y) {
				d11.pos = relative(d11.pos, rotDir(rot, dirSouth), 8+(y-m.startY)*8)
				d11.pos = relative(d11.pos, rotDir(rot, dirEast), (x-m.startX)*8)
				m.traverseWallPiece(d11)
				m.traverseOuterWalls(d11, third, dirSouth, x, y, x, y)
				found = true
			}
		}
	}
	m.createRoof(offset(pos, 0, 16, 0), rot, base, third)
	m.createRoof(offset(pos, 0, 27, 0), rot, third, nil)
	R := func(d int) int { return rotDir(rot, d) }
	var dirs []int
	for floor := 0; floor < 3; floor++ {
		up := int32(8 * floor)
		if floor == 2 {
			up += 3
		}
		fp := offset(pos, 0, up, 0)
		rooms := g.rooms[floor]
		grid := base
		if floor == 2 {
			grid = third
		}
		carpetS, carpetW := "carpet_south_2", "carpet_west_2"
		wall, doorT := "indoors_wall_2", "indoors_door_2"
		if floor == 0 {
			carpetS, carpetW = "carpet_south_1", "carpet_west_1"
			wall, doorT = "indoors_wall_1", "indoors_door_1"
		}
		corr := func(x, y int32) bool { return grid.get(x, y) == 1 || rooms.get(x, y)&mgCorrFlag == mgCorrFlag }
		for y := int32(0); y < grid.height; y++ {
			for x := int32(0); x < grid.width; x++ {
				if grid.get(x, y) != 1 {
					continue
				}
				p := m.cell(fp, rot, x, y, 0)
				m.add("corridor_floor", p, rot, mirNone)
				if corr(x, y-1) {
					m.add("carpet_north", offset(relative(p, R(dirEast), 1), 0, 1, 0), rot, mirNone)
				}
				if corr(x+1, y) {
					m.add("carpet_east", offset(relative(relative(p, R(dirSouth), 1), R(dirEast), 5), 0, 1, 0), rot, mirNone)
				}
				if corr(x, y+1) {
					m.add(carpetS, relative(relative(p, R(dirSouth), 5), R(dirWest), 1), rot, mirNone)
				}
				if corr(x-1, y) {
					m.add(carpetW, relative(relative(p, R(dirWest), 1), R(dirNorth), 1), rot, mirNone)
				}
			}
		}
		rc := mansionRooms{floor: floor}
		for y := int32(0); y < grid.height; y++ {
			for x := int32(0); x < grid.width; x++ {
				stairsTop := floor == 2 && grid.get(x, y) == 3
				if grid.get(x, y) != 2 && !stairsTop {
					continue
				}
				v := rooms.get(x, y)
				typ := v & mgTypeMask
				id := v & mgIDMask
				stairsTop = stairsTop && v&mgCorrFlag == mgCorrFlag
				dirs = dirs[:0]
				if v&mgDoorFlag == mgDoorFlag {
					for _, hd := range horizontalDirs {
						if grid.get(x+stepX(hd), y+stepZ(hd)) == 1 {
							dirs = append(dirs, hd)
						}
					}
				}
				door := -1
				if len(dirs) > 0 {
					door = dirs[m.r.NextIntN(int32(len(dirs)))]
				} else if v&mgOriginFlag == mgOriginFlag {
					door = dirUp
				}
				p := m.cell(fp, rot, x, y, -1)
				pick := func(d int) string {
					if door == d {
						return doorT
					}
					return wall
				}
				if mgIsHouse(grid, x-1, y) && !g.isRoomID(floor, x-1, y, id) {
					m.add(pick(dirWest), p, rot, mirNone)
				}
				if grid.get(x+1, y) == 1 && !stairsTop {
					m.add(pick(dirEast), relative(p, R(dirEast), 8), rot, mirNone)
				}
				if mgIsHouse(grid, x, y+1) && !g.isRoomID(floor, x, y+1, id) {
					q := relative(relative(p, R(dirSouth), 7), R(dirEast), 7)
					m.add(pick(dirSouth), q, rotAdd(rot, rotCW90), mirNone)
				}
				if grid.get(x, y-1) == 1 && !stairsTop {
					q := relative(relative(p, R(dirNorth), 1), R(dirEast), 7)
					m.add(pick(dirNorth), q, rotAdd(rot, rotCW90), mirNone)
				}
				switch {
				case typ == mgRoom1x1:
					m.addRoom1x1(p, rot, door, rc)
				case typ == mgRoom1x2 && door >= 0:
					d := g.get1x2RoomDirection(x, y, floor, id)
					m.addRoom1x2(p, rot, d, door, rc, v&mgStairsFlag == mgStairsFlag)
				case typ == mgRoom2x2 && door >= 0 && door != dirUp:
					d := clockwise(door)
					if !g.isRoomID(floor, x+stepX(d), y+stepZ(d), id) {
						d = oppositeDir(d)
					}
					m.addRoom2x2(p, rot, d, door, rc)
				case typ == mgRoom2x2 && door == dirUp:
					m.add("2x2_s1", relative(p, R(dirEast), 1), rot, mirNone)
				}
			}
		}
	}
}

var mirrorEnumNames = [3]string{"NONE", "LEFT_RIGHT", "FRONT_BACK"}

type mansionPiece struct {
	legacyTemplatePiece
	rot, mirror int
}

func mansionSettings(rot, mirror int) func() *placeSettings {
	return func() *placeSettings {
		s := newPlaceSettings()
		s.ignoreEntities = true
		s.rotation, s.mirror = rot, mirror
		s.processors = append(s.processors, ignoreBlocksProc(map[string]bool{"structure_block": true}))
		return s
	}
}

func newMansionPiece(name string, pos BlockPos, rot, mirror int) *mansionPiece {
	p := &mansionPiece{rot: rot, mirror: mirror}
	p.legacyTemplatePiece.initTemplate("wmp", "woodland_mansion/"+name, mansionSettings(rot, mirror), pos)
	p.name = name
	p.marker = p.dataMarker
	return p
}

// dataMarker is WoodlandMansionPiece.handleDataMarker.
func (p *mansionPiece) dataMarker(c *PieceContext, meta string, pos BlockPos) {
	if strings.HasPrefix(meta, "Chest") {
		st := chestBlock()
		switch meta {
		case "ChestWest":
			st = withProp(st, "facing", dirNameOf[rotDir(p.rot, dirWest)])
		case "ChestEast":
			st = withProp(st, "facing", dirNameOf[rotDir(p.rot, dirEast)])
		case "ChestSouth":
			st = withProp(st, "facing", dirNameOf[rotDir(p.rot, dirSouth)])
		case "ChestNorth":
			st = withProp(st, "facing", dirNameOf[rotDir(p.rot, dirNorth)])
		}
		createChestAt(c, c.ChunkBox, c.Random, pos, "minecraft:chests/woodland_mansion", st)
		return
	}
	n := 0
	switch meta {
	case "Mage", "Warrior":
		n = 1
	case "Group of Allays":
		n = int(regionRandom(c.Region).NextIntN(3)) + 1
	default:
		return
	}
	for i := 0; i < n; i++ {
		c.Region.set(pos, world.Air)
	}
}

func (p *mansionPiece) SaveData(t map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.saveTemplate(t)
	t["Rot"] = rotationEnumNames[p.rot]
	t["Mi"] = mirrorEnumNames[p.mirror]
}

func (p *mansionPiece) PostProcess(c *PieceContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.placeTemplate(c)
}

// mansionAfterPlace is WoodlandMansionStructure.afterPlace.
func mansionAfterPlace(c *PieceContext, s *StructureStart) {
	bb := piecesBox(s.Pieces)
	y0 := bb.MinY
	minY := c.Region.MinY()
	cobble := blockNamed("cobblestone")
	cb := c.ChunkBox
	for x := cb.MinX; x <= cb.MaxX; x++ {
		for z := cb.MinZ; z <= cb.MaxZ; z++ {
			p := BlockPos{x, y0, z}
			if isAir(c.Region.at(p)) || !bb.IsInside(p) {
				continue
			}
			in := false
			for _, pc := range s.Pieces {
				if pc.BoundingBox().IsInside(p) {
					in = true
					break
				}
			}
			if !in {
				continue
			}
			for y := y0 - 1; y > minY; y-- {
				q := BlockPos{x, y, z}
				st := c.Region.at(q)
				if !isAir(st) && props(st)&(1<<bpLiquidBlock) == 0 {
					break
				}
				c.Region.set(q, cobble)
			}
		}
	}
}

func init() {
	structureTypes["woodland_mansion"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		d.afterPlace = mansionAfterPlace
		return func(c *GenContext) (*GenerationStub, bool) {
			r := c.Random
			rot := int(r.NextIntN(4))
			dx, dz := int32(5), int32(5)
			switch rot {
			case rotCW90:
				dx = -5
			case rotCW180:
				dx, dz = -5, -5
			case rotCCW90:
				dz = -5
			}
			x, z := c.ChunkX*16+7, c.ChunkZ*16+7
			y := lowestCornerY(g, x, z, dx, dz)
			if y < 60 {
				return nil, false
			}
			pos := BlockPos{x, y, z}
			return &GenerationStub{Pos: pos, Generate: func(add func(StructurePiece)) {
				grid := newMansionGrid(r)
				pl := &mansionPlacer{r: r}
				pl.createMansion(pos, rot, grid)
				for _, p := range pl.out {
					add(p)
				}
			}}, true
		}
	}
	pieceLoaders["wmp"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		p := &mansionPiece{}
		rs, _ := t["Rot"].(string)
		for i, n := range rotationEnumNames {
			if n == rs {
				p.rot = i
			}
		}
		ms, _ := t["Mi"].(string)
		for i, n := range mirrorEnumNames {
			if n == ms {
				p.mirror = i
			}
		}
		if err := p.loadTemplateBase("wmp", t, func(name string) func() *placeSettings { return mansionSettings(p.rot, p.mirror) }); err != nil {
			return nil, err
		}
		tn, _ := t["Template"].(string)
		p.name = tn
		p.tpl = loadTemplate("woodland_mansion/" + tn)
		p.Box = bbFrom(p.tpl.boundingBox(p.settings(), p.pos))
		p.marker = p.dataMarker
		return p, nil
	}
}
