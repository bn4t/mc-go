package server

import (
	"math"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Nether portal destinations, measured from the 26.2 jar
// (NetherPortalBlock.getPortalDestination/getExitPortal, PortalForcer,
// BlockUtil.getLargestRectangleAround, PortalShape.getRelativePosition):
//
//   - the exit point is the traveller's position times the
//     teleportation scale (source/destination coordinate scale), y
//     unscaled, clamped into the world border;
//   - an existing portal block within a square of 16 (into the nether)
//     or 128 (into the overworld) columns wins, nearest by squared
//     block distance, then lowest y; its frame is the largest rectangle
//     of identical portal states around it (21 x 21 limits);
//   - otherwise a portal is built (createPortal below);
//   - the traveller keeps its relative place in the entry portal
//     (across, up, and depth) and turns 90 degrees when the two
//     portals' axes differ.

// portalRect is BlockUtil.FoundRectangle: the low corner and the
// sizes along the portal's axis and up.
type portalRect struct {
	min          world.Pos
	width, height int
}

// portalAxisX reports a nether_portal state's axis (true for x);
// ok is false for anything else.
func portalAxisX(b world.Block) (axisX, ok bool) {
	if !isNetherPortal(b) {
		return false, false
	}
	a, _ := world.StateProperty(b, "axis")
	return a != "z", true
}

// logicalHeight is the dimension type's logical_height.
func logicalHeight(d *dimension) int {
	if d.name == dimNether {
		return 128
	}
	return d.height
}

// portalTopY is min(maxY, minY + logicalHeight - 1).
func portalTopY(d *dimension) int {
	return min(d.minY+d.height-1, d.minY+logicalHeight(d)-1)
}

func axisStep(axisX bool) (int, int) {
	if axisX {
		return 1, 0 // EAST
	}
	return 0, 1 // SOUTH
}

// findClosestPortal is PortalForcer.findClosestPortalPosition over the
// portal blocks in the destination: generated or loaded chunks in the
// square, and edited cells.
func (s *Server) findClosestPortal(d *dimension, exit world.Pos, toNether bool) (world.Pos, bool) {
	r := 128
	if toNether {
		r = 16
	}
	var best world.Pos
	bestD := math.Inf(1)
	found := false
	consider := func(p world.Pos) {
		if p.X < exit.X-r || p.X > exit.X+r || p.Z < exit.Z-r || p.Z > exit.Z+r {
			return
		}
		if _, ok := portalAxisX(d.w.GetBlock(p)); !ok {
			return
		}
		dx, dy, dz := float64(p.X-exit.X), float64(p.Y-exit.Y), float64(p.Z-exit.Z)
		dist := dx*dx + dy*dy + dz*dz
		if !found || dist < bestD || dist == bestD && p.Y < best.Y {
			best, bestD, found = p, dist, true
		}
	}
	if d.w.Provided() {
		for cx := (exit.X - r) >> 4; cx <= (exit.X+r)>>4; cx++ {
			for cz := (exit.Z - r) >> 4; cz <= (exit.Z+r)>>4; cz++ {
				c := d.w.PeekBaseChunk(cx, cz)
				if c == nil {
					continue
				}
				for si, sec := range c.Sections {
					for i, b := range sec {
						if isNetherPortal(b) {
							consider(world.Pos{X: cx*16 + i&15, Y: c.MinY + si*16 + i>>8, Z: cz*16 + i>>4&15})
						}
					}
				}
			}
		}
	}
	for p, b := range d.w.Snapshot() {
		if isNetherPortal(b) {
			consider(p)
		}
	}
	return best, found
}

// largestPortalRect is BlockUtil.getLargestRectangleAround with both
// limits 21 and the predicate "same state as at center".
func largestPortalRect(w *world.World, center world.Pos, axisX bool) portalRect {
	state := w.GetBlock(center)
	sx, sz := axisStep(axisX)
	match := func(p world.Pos) bool { return w.GetBlock(p) == state }
	// limit counts consecutive matches stepping from p.
	limit := func(p world.Pos, dx, dy, dz, max int) int {
		n := 0
		for n < max {
			p = world.Pos{X: p.X + dx, Y: p.Y + dy, Z: p.Z + dz}
			if !match(p) {
				break
			}
			n++
		}
		return n
	}
	const lim = 21
	neg := limit(center, -sx, 0, -sz, lim)
	pos := limit(center, sx, 0, sz, lim)
	type bounds struct{ down, up int }
	b := make([]bounds, neg+1+pos)
	b[neg] = bounds{limit(center, 0, -1, 0, lim), limit(center, 0, 1, 0, lim)}
	below := b[neg].down
	for m := 1; m <= neg; m++ {
		prev := b[neg-m+1]
		c := world.Pos{X: center.X - sx*m, Y: center.Y, Z: center.Z - sz*m}
		b[neg-m] = bounds{limit(c, 0, -1, 0, prev.down), limit(c, 0, 1, 0, prev.up)}
	}
	for m := 1; m <= pos; m++ {
		prev := b[neg+m-1]
		c := world.Pos{X: center.X + sx*m, Y: center.Y, Z: center.Z + sz*m}
		b[neg+m] = bounds{limit(c, 0, -1, 0, prev.down), limit(c, 0, 1, 0, prev.up)}
	}
	start, row, bw, bh := 0, 0, 0, 0
	heights := make([]int, len(b))
	for r := below; r >= 0; r-- {
		for i, bb := range b {
			lo, hi := below-bb.down, below+bb.up
			if r >= lo && r <= hi {
				heights[i] = hi + 1 - r
			} else {
				heights[i] = 0
			}
		}
		lo, hi, h := maxHistogramRect(heights)
		if w := 1 + hi - lo; w*h > bw*bh {
			start, row, bw, bh = lo, r, w, h
		}
	}
	return portalRect{
		min:    world.Pos{X: center.X + sx*(start-neg), Y: center.Y + row - below, Z: center.Z + sz*(start-neg)},
		width:  bw,
		height: bh,
	}
}

// maxHistogramRect is BlockUtil.getMaxRectangleLocation: the widest
// largest-area run under a histogram (stack scan, strict improvement
// keeps the first), as inclusive index bounds plus its height.
func maxHistogramRect(h []int) (lo, hi, height int) {
	i, j, k := 0, 0, 0
	stack := []int{0}
	for l := 1; l <= len(h); l++ {
		m := 0
		if l < len(h) {
			m = h[l]
		}
		for len(stack) > 0 {
			n := h[stack[len(stack)-1]]
			if m >= n {
				stack = append(stack, l)
				break
			}
			stack = stack[:len(stack)-1]
			o := 0
			if len(stack) > 0 {
				o = stack[len(stack)-1] + 1
			}
			if n*(l-o) > k*(j-i) {
				j, i, k = l, o, n
			}
		}
		if len(stack) == 0 {
			stack = append(stack, l)
		}
	}
	return i, j - 1, k
}

// motionBlockingHeight is getHeight(MOTION_BLOCKING): the top
// motion-blocking cell plus one, minY for an empty column.
func motionBlockingHeight(d *dimension, x, z int) int {
	col := make([]world.Block, d.height)
	d.w.FillBox(world.Pos{X: x, Y: d.minY, Z: z}, world.Pos{X: x, Y: d.minY + d.height - 1, Z: z}, col)
	for i := len(col) - 1; i >= 0; i-- {
		if worldgen.MotionBlocking(col[i]) {
			return d.minY + i + 1
		}
	}
	return d.minY
}

func portalCanReplace(d *dimension, p world.Pos) bool {
	b := d.w.GetBlock(p)
	return worldgen.CanBeReplaced(b) && !worldgen.HasFluid(b)
}

// portalCanHostFrame is PortalForcer.canHostFrame: a 4x5 slab (offset
// sideways by off) whose bottom row is solid and the rest replaceable.
func portalCanHostFrame(d *dimension, p world.Pos, axisX bool, off int) bool {
	sx, sz := axisStep(axisX)
	cx, cz := -sz, sx // clockwise of the positive axis direction
	for i := -1; i < 3; i++ {
		for j := -1; j < 4; j++ {
			q := world.Pos{X: p.X + sx*i + cx*off, Y: p.Y + j, Z: p.Z + sz*i + cz*off}
			if j < 0 {
				if !worldgen.IsSolid(d.w.GetBlock(q)) {
					return false
				}
			} else if !portalCanReplace(d, q) {
				return false
			}
		}
	}
	return true
}

// spiralAround is BlockPos.spiralAround(center, radius, EAST, SOUTH):
// the center, then each ring starting one step east of the previous
// ring's end and running south, west, north, east.
func spiralAround(cx, cz, radius int, visit func(x, z int)) {
	visit(cx, cz)
	x, z := cx, cz
	for k := 1; k <= radius; k++ {
		x++
		visit(x, z)
		for _, leg := range [4]struct{ dx, dz, n int }{{0, 1, 2*k - 1}, {-1, 0, 2 * k}, {0, -1, 2 * k}, {1, 0, 2 * k}} {
			for i := 0; i < leg.n; i++ {
				x, z = x+leg.dx, z+leg.dz
				visit(x, z)
			}
		}
	}
}

// createPortal is PortalForcer.createPortal: the nearest spot (spiral
// order, squared distance) where a portal with a free column of three
// on both sides fits, else where the frame alone fits, else a forced
// spot at y clamped into [max(minY+1, 70), top-9] with an obsidian
// ledge cleared around it; then the frame and the 2x3 portal.
func (s *Server) createPortal(d *dimension, exit world.Pos, axisX bool) (portalRect, bool) {
	sx, sz := axisStep(axisX)
	top := portalTopY(d)
	bestD, altD := -1.0, -1.0
	var best, alt world.Pos
	spiralAround(exit.X, exit.Z, 16, func(x, z int) {
		j := min(top, motionBlockingHeight(d, x, z))
		for k := j; k >= d.minY; k-- {
			p := world.Pos{X: x, Y: k, Z: z}
			if !portalCanReplace(d, p) {
				continue
			}
			l := k
			for k > d.minY && portalCanReplace(d, world.Pos{X: x, Y: k - 1, Z: z}) {
				k--
			}
			if k+4 > top {
				continue
			}
			if m := l - k; m > 0 && m < 3 {
				continue
			}
			p.Y = k
			if !portalCanHostFrame(d, p, axisX, 0) {
				continue
			}
			dx, dy, dz := float64(p.X-exit.X), float64(p.Y-exit.Y), float64(p.Z-exit.Z)
			e := dx*dx + dy*dy + dz*dz
			if portalCanHostFrame(d, p, axisX, -1) && portalCanHostFrame(d, p, axisX, 1) && (bestD == -1 || bestD > e) {
				bestD, best = e, p
			}
			if bestD == -1 && (altD == -1 || altD > e) {
				altD, alt = e, p
			}
		}
	})
	if bestD == -1 && altD != -1 {
		best, bestD = alt, altD
	}
	obsidian := world.BlockStateMust("obsidian")
	put := func(p world.Pos, b world.Block) { s.setNaturalBlock(d, p, b) }
	if bestD == -1 {
		lo := max(d.minY+1, 70)
		hi := top - 9
		if hi < lo {
			return portalRect{}, false
		}
		best = world.Pos{X: exit.X - sx, Y: min(max(exit.Y, lo), hi), Z: exit.Z - sz}
		cx, cz := -sz, sx
		for i := -1; i < 2; i++ {
			for j := 0; j < 2; j++ {
				for k := -1; k < 3; k++ {
					b := world.Air
					if k < 0 {
						b = obsidian
					}
					put(world.Pos{X: best.X + j*sx + i*cx, Y: best.Y + k, Z: best.Z + j*sz + i*cz}, b)
				}
			}
		}
	}
	for i := -1; i < 3; i++ {
		for j := -1; j < 4; j++ {
			if i == -1 || i == 2 || j == -1 || j == 3 {
				put(world.Pos{X: best.X + i*sx, Y: best.Y + j, Z: best.Z + i*sz}, obsidian)
			}
		}
	}
	axis := "x"
	if !axisX {
		axis = "z"
	}
	portal, _ := world.BlockStateOf("nether_portal", map[string]string{"axis": axis})
	for i := 0; i < 2; i++ {
		for j := 0; j < 3; j++ {
			put(world.Pos{X: best.X + i*sx, Y: best.Y + j, Z: best.Z + i*sz}, portal)
		}
	}
	return portalRect{min: best, width: 2, height: 3}, true
}

// portalRelative is PortalShape.getRelativePosition: across the portal
// (0..1 of the room the body leaves), up (0..1), and depth from the
// portal plane's centre.
func portalRelative(r portalRect, axisX bool, x, y, z, w, h float64) (float64, float64, float64) {
	along, across := x, z
	minAlong, minAcross := float64(r.min.X), float64(r.min.Z)
	if !axisX {
		along, across = z, x
		minAlong, minAcross = float64(r.min.Z), float64(r.min.X)
	}
	rx := 0.5
	if room := float64(r.width) - w; room > 0 {
		rx = clamp01((along - (minAlong + w/2)) / room)
	}
	ry := 0.0
	if room := float64(r.height) - h; room > 0 {
		ry = clamp01((y - float64(r.min.Y)) / room)
	}
	return rx, ry, across - (minAcross + 0.5)
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// portalExit resolves a portal trip from cur (standing in the portal
// block at feet, body at x,y,z) into dst: the arrival position and the
// yaw turn (0 or 90).
func (s *Server) portalExit(cur, dst *dimension, feet world.Pos, x, y, z float64, sneaking bool) (float64, float64, float64, float32, bool) {
	scale := cur.coordScale / dst.coordScale
	const border = 29999984
	clampB := func(v float64) int {
		return int(math.Floor(math.Max(-border, math.Min(border-1e-5, v))))
	}
	exit := world.Pos{X: clampB(x * scale), Y: int(math.Floor(y)), Z: clampB(z * scale)}
	entryAxisX, entryIsPortal := portalAxisX(cur.w.GetBlock(feet))
	var rect portalRect
	if p, ok := s.findClosestPortal(dst, exit, dst.name == dimNether); ok {
		ax, _ := portalAxisX(dst.w.GetBlock(p))
		rect = largestPortalRect(dst.w, p, ax)
	} else {
		ax := true
		if entryIsPortal {
			ax = entryAxisX
		}
		r, ok := s.createPortal(dst, exit, ax)
		if !ok {
			return 0, 0, 0, 0, false
		}
		rect = r
	}
	// The player's standing (or crouching) box.
	w, h := 0.6, 1.8
	if sneaking {
		h = 1.5
	}
	srcAxisX, rx, ry, rz := true, 0.5, 0.0, 0.0
	if entryIsPortal {
		srcAxisX = entryAxisX
		rx, ry, rz = portalRelative(largestPortalRect(cur.w, feet, srcAxisX), srcAxisX, x, y, z, w, h)
	}
	dstAxisX, ok := portalAxisX(dst.w.GetBlock(rect.min))
	if !ok {
		dstAxisX = true
	}
	along := w/2 + (float64(rect.width)-w)*rx
	up := (float64(rect.height) - h) * ry
	depth := 0.5 + rz
	ax, az := float64(rect.min.X)+depth, float64(rect.min.Z)+along
	if dstAxisX {
		ax, az = float64(rect.min.X)+along, float64(rect.min.Z)+depth
	}
	var turn float32
	if srcAxisX != dstAxisX {
		turn = 90
	}
	return ax, float64(rect.min.Y) + up, az, turn, true
}
