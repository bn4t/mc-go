package worldgen

import (
	"encoding/json"
	"strconv"

	"mc-go/internal/world"
)

// decoCtx is TreeDecorator.Context: the logs, leaves and roots lists
// (copied from their sets, then stably sorted by y) and the decoration
// setter.
type decoCtx struct {
	g                   *Generator
	rg                  *Region
	r                   Random
	set                 func(BlockPos, world.Block)
	logs, leaves, roots []BlockPos
}

func newDecoCtx(g *Generator, rg *Region, r Random, set func(BlockPos, world.Block), logs, leaves, roots []BlockPos) *decoCtx {
	return &decoCtx{g: g, rg: rg, r: r, set: set, logs: sortByY(logs), leaves: sortByY(leaves), roots: sortByY(roots)}
}

func (c *decoCtx) isAir(p BlockPos) bool { return isAirAt(c.rg, p) }

// placeVine is Context.placeVine: a vine with one face set.
func (c *decoCtx) placeVine(p BlockPos, face string) {
	c.set(p, withProp(blockNamed("vine"), face, "true"))
}

// lowestTrunkOrRoot is TreeFeature.getLowestTrunkOrRootOfTree.
func (c *decoCtx) lowestTrunkOrRoot() []BlockPos {
	var out []BlockPos
	switch {
	case len(c.roots) == 0:
		out = append(out, c.logs...)
	case len(c.logs) > 0 && c.roots[0][1] == c.logs[0][1]:
		out = append(out, c.logs...)
		out = append(out, c.roots...)
	default:
		out = append(out, c.roots...)
	}
	return out
}

// treeDecorator is a TreeDecorator.
type treeDecorator interface{ place(c *decoCtx) }

func parseDecorators(g *Generator, raw json.RawMessage) []treeDecorator {
	var list []obj
	jsonUnmarshal(raw, &list)
	var out []treeDecorator
	for _, o := range list {
		out = append(out, parseDecorator(g, o))
	}
	return out
}

func parseDecorator(g *Generator, o obj) treeDecorator {
	switch t := trimNS(o.str("type")); t {
	case "beehive":
		return beehiveDeco{parseF32(o["probability"])}
	case "place_on_ground":
		d := placeOnGroundDeco{tries: 128, radius: 2, height: 1, provider: g.compileStateProvider(o["block_state_provider"])}
		if _, ok := o["tries"]; ok {
			d.tries = o.i32("tries")
		}
		if _, ok := o["radius"]; ok {
			d.radius = o.i32("radius")
		}
		if _, ok := o["height"]; ok {
			d.height = o.i32("height")
		}
		return d
	case "leave_vine":
		return leaveVineDeco{parseF32(o["probability"])}
	case "trunk_vine":
		return trunkVineDeco{}
	case "cocoa":
		return cocoaDeco{parseF32(o["probability"])}
	case "alter_ground":
		return alterGroundDeco{g.compileStateProvider(o["provider"])}
	case "attached_to_leaves":
		return attachedToLeavesDeco{
			prob: parseF32(o["probability"]), exXZ: o.i32("exclusion_radius_xz"), exY: o.i32("exclusion_radius_y"),
			provider: g.compileStateProvider(o["block_provider"]), required: o.i32("required_empty_blocks"),
			dirs: parseDirList(o["directions"]),
		}
	case "attached_to_logs":
		return attachedToLogsDeco{prob: parseF32(o["probability"]), provider: g.compileStateProvider(o["block_provider"]), dirs: parseDirList(o["directions"])}
	case "creaking_heart":
		return creakingHeartDeco{parseF32(o["probability"])}
	case "pale_moss":
		return paleMossDeco{leaves: parseF32(o["leaves_probability"]), trunk: parseF32(o["trunk_probability"]), ground: parseF32(o["ground_probability"])}
	default:
		panic("worldgen: unknown tree decorator " + t)
	}
}

// shuffle is Util.shuffle.
func shuffle(l []BlockPos, r Random) {
	for i := int32(len(l)); i > 1; i-- {
		j := r.NextIntN(i)
		l[i-1], l[j] = l[j], l[i-1]
	}
}

func shuffledCopy(l []BlockPos, r Random) []BlockPos {
	out := append([]BlockPos(nil), l...)
	shuffle(out, r)
	return out
}

// beehiveDeco is BeehiveDecorator: a south-facing bee nest beside a
// log just under the canopy (or low on a leafless trunk).
type beehiveDeco struct{ prob float32 }

func (d beehiveDeco) place(c *decoCtx) {
	if len(c.logs) == 0 {
		return
	}
	r := c.r
	if r.NextFloat() >= d.prob {
		return
	}
	var y int32
	if len(c.leaves) > 0 {
		y = max(c.leaves[0][1]-1, c.logs[0][1]+1)
	} else {
		y = min(c.logs[0][1]+1+r.NextIntN(3), c.logs[len(c.logs)-1][1])
	}
	var cand []BlockPos
	for _, p := range c.logs {
		if p[1] == y {
			// SPAWN_DIRECTIONS: the horizontal plane without north.
			for _, dd := range []int{dirEast, dirSouth, dirWest} {
				cand = append(cand, relDir(p, dd, 1))
			}
		}
	}
	if len(cand) == 0 {
		return
	}
	shuffle(cand, r)
	for _, p := range cand {
		if c.isAir(p) && c.isAir(relDir(p, dirSouth, 1)) {
			c.set(p, withProp(blockNamed("bee_nest"), "facing", "south"))
			// The nest's block entity exists once the write landed
			// (WorldGenRegion keeps a DUMMY tag), and stocking it draws
			// 2-3 bees with a nextInt(599) each.
			if c.rg.canWrite(p) && p[1] >= c.rg.MinY() && p[1] <= c.rg.MaxY() {
				n := 2 + r.NextIntN(2)
				for i := int32(0); i < n; i++ {
					r.NextIntN(599)
				}
			}
			return
		}
	}
}

// placeOnGroundDeco is PlaceOnGroundDecorator (leaf litter).
type placeOnGroundDeco struct {
	tries, radius, height int32
	provider              stateProvider
}

func (d placeOnGroundDeco) place(c *decoCtx) {
	list := c.lowestTrunkOrRoot()
	if len(list) == 0 {
		return
	}
	f := list[0]
	y := f[1]
	x0, x1, z0, z1 := f[0], f[0], f[2], f[2]
	for _, p := range list {
		if p[1] == y {
			x0, x1 = min(x0, p[0]), max(x1, p[0])
			z0, z1 = min(z0, p[2]), max(z1, p[2])
		}
	}
	r := c.r
	bx0, bx1 := x0-d.radius, x1+d.radius
	by0, by1 := y-d.height, y+d.height
	bz0, bz1 := z0-d.radius, z1+d.radius
	for i := int32(0); i < d.tries; i++ {
		x := r.NextIntN(bx1-bx0+1) + bx0
		yy := r.NextIntN(by1-by0+1) + by0
		z := r.NextIntN(bz1-bz0+1) + bz0
		d.attempt(c, BlockPos{x, yy, z})
	}
}

func (d placeOnGroundDeco) attempt(c *decoCtx, p BlockPos) {
	up := offset(p, 0, 1, 0)
	ab := blockAt(c.rg, up)
	if !(isAir(ab) || isBlock(ab, "vine")) {
		return
	}
	if props(blockAt(c.rg, p))&(1<<bpSolidRender) == 0 {
		return
	}
	if treeHeightAt(c.rg, HMMotionBlockingNoLeaves, p[0], p[2]) > up[1] {
		return
	}
	b := d.provider.getState(c.rg, c.r, up)
	treeTrace(c.rg, "litter %v", up)
	c.set(up, b)
}

// treeHeightAt is WorldGenLevel.getHeight for a final heightmap,
// priming the chunk's maps on first use the way ProtoChunk.getHeight
// does.
func treeHeightAt(rg *Region, t int, x, z int32) int32 {
	if ch := rg.chunkAt(x, z); ch != nil {
		ch.primeHeightmaps()
	}
	return rg.Height(t, x, z)
}

// leaveVineDeco is LeaveVineDecorator.
type leaveVineDeco struct{ prob float32 }

func (d leaveVineDeco) place(c *decoCtx) {
	r := c.r
	for _, p := range c.leaves {
		if r.NextFloat() < d.prob {
			if q := relDir(p, dirWest, 1); c.isAir(q) {
				addHangingVine(c, q, "east")
			}
		}
		if r.NextFloat() < d.prob {
			if q := relDir(p, dirEast, 1); c.isAir(q) {
				addHangingVine(c, q, "west")
			}
		}
		if r.NextFloat() < d.prob {
			if q := relDir(p, dirNorth, 1); c.isAir(q) {
				addHangingVine(c, q, "south")
			}
		}
		if r.NextFloat() < d.prob {
			if q := relDir(p, dirSouth, 1); c.isAir(q) {
				addHangingVine(c, q, "north")
			}
		}
	}
}

func addHangingVine(c *decoCtx, p BlockPos, face string) {
	c.placeVine(p, face)
	p = offset(p, 0, -1, 0)
	for i := 4; c.isAir(p) && i > 0; i-- {
		c.placeVine(p, face)
		p = offset(p, 0, -1, 0)
	}
}

// trunkVineDeco is TrunkVineDecorator.
type trunkVineDeco struct{}

func (trunkVineDeco) place(c *decoCtx) {
	r := c.r
	for _, p := range c.logs {
		if r.NextIntN(3) > 0 {
			if q := relDir(p, dirWest, 1); c.isAir(q) {
				c.placeVine(q, "east")
			}
		}
		if r.NextIntN(3) > 0 {
			if q := relDir(p, dirEast, 1); c.isAir(q) {
				c.placeVine(q, "west")
			}
		}
		if r.NextIntN(3) > 0 {
			if q := relDir(p, dirNorth, 1); c.isAir(q) {
				c.placeVine(q, "south")
			}
		}
		if r.NextIntN(3) > 0 {
			if q := relDir(p, dirSouth, 1); c.isAir(q) {
				c.placeVine(q, "north")
			}
		}
	}
}

// cocoaDeco is CocoaDecorator: pods on the lowest three log layers.
type cocoaDeco struct{ prob float32 }

var dirName = [6]string{"down", "up", "north", "south", "west", "east"}

func (d cocoaDeco) place(c *decoCtx) {
	r := c.r
	if r.NextFloat() >= d.prob {
		return
	}
	if len(c.logs) == 0 {
		return
	}
	y0 := c.logs[0][1]
	for _, p := range c.logs {
		if p[1]-y0 > 2 {
			continue
		}
		for _, dd := range horizontalDirs {
			if r.NextFloat() <= 0.25 {
				op := oppositeDir(dd)
				q := offset(p, stepX(op), 0, stepZ(op))
				if c.isAir(q) {
					b := withProp(blockNamed("cocoa"), "age", strconv.Itoa(int(r.NextIntN(3))))
					c.set(q, withProp(b, "facing", dirName[dd]))
				}
			}
		}
	}
}

// alterGroundDeco is AlterGroundDecorator (podzol under mega spruce).
type alterGroundDeco struct{ provider stateProvider }

func (d alterGroundDeco) place(c *decoCtx) {
	list := c.lowestTrunkOrRoot()
	if len(list) == 0 {
		return
	}
	y := list[0][1]
	for _, p := range list {
		if p[1] != y {
			continue
		}
		d.circle(c, offset(p, -1, 0, -1))
		d.circle(c, offset(p, 2, 0, -1))
		d.circle(c, offset(p, -1, 0, 2))
		d.circle(c, offset(p, 2, 0, 2))
		for i := 0; i < 5; i++ {
			k := c.r.NextIntN(64)
			a, b := k%8, k/8
			if a == 0 || a == 7 || b == 0 || b == 7 {
				d.circle(c, offset(p, -3+a, 0, -3+b))
			}
		}
	}
}

func (d alterGroundDeco) circle(c *decoCtx, p BlockPos) {
	for dx := int32(-2); dx <= 2; dx++ {
		for dz := int32(-2); dz <= 2; dz++ {
			if absI(dx) != 2 || absI(dz) != 2 {
				d.at(c, offset(p, dx, 0, dz))
			}
		}
	}
}

func (d alterGroundDeco) at(c *decoCtx, p BlockPos) {
	for dy := int32(2); dy >= -3; dy-- {
		q := offset(p, 0, dy, 0)
		if b, ok := d.provider(c.rg, c.r, q); ok {
			c.set(q, b)
			return
		}
		if !c.isAir(q) && dy < 0 {
			return
		}
	}
}

// attachedToLeavesDeco is AttachedToLeavesDecorator (hanging
// propagules).
type attachedToLeavesDeco struct {
	prob      float32
	exXZ, exY int32
	provider  stateProvider
	required  int32
	dirs      []int
}

func (d attachedToLeavesDeco) place(c *decoCtx) {
	r := c.r
	excl := map[BlockPos]bool{}
	for _, p := range shuffledCopy(c.leaves, r) {
		dir := d.dirs[r.NextIntN(int32(len(d.dirs)))]
		q := relDir(p, dir, 1)
		if excl[q] {
			continue
		}
		if r.NextFloat() < d.prob && d.hasEmpty(c, p, dir) {
			for x := q[0] - d.exXZ; x <= q[0]+d.exXZ; x++ {
				for y := q[1] - d.exY; y <= q[1]+d.exY; y++ {
					for z := q[2] - d.exXZ; z <= q[2]+d.exXZ; z++ {
						excl[BlockPos{x, y, z}] = true
					}
				}
			}
			c.set(q, d.provider.getState(c.rg, r, q))
		}
	}
}

func (d attachedToLeavesDeco) hasEmpty(c *decoCtx, p BlockPos, dir int) bool {
	for i := int32(1); i <= d.required; i++ {
		if !c.isAir(relDir(p, dir, i)) {
			return false
		}
	}
	return true
}

// attachedToLogsDeco is AttachedToLogsDecorator (fallen-log mushrooms).
type attachedToLogsDeco struct {
	prob     float32
	provider stateProvider
	dirs     []int
}

func (d attachedToLogsDeco) place(c *decoCtx) {
	r := c.r
	for _, p := range shuffledCopy(c.logs, r) {
		dir := d.dirs[r.NextIntN(int32(len(d.dirs)))]
		q := relDir(p, dir, 1)
		if r.NextFloat() <= d.prob && c.isAir(q) {
			c.set(q, d.provider.getState(c.rg, r, q))
		}
	}
}

// creakingHeartDeco is CreakingHeartDecorator: a dormant natural heart
// replaces a log enclosed by logs on all six sides.
type creakingHeartDeco struct{ prob float32 }

func (d creakingHeartDeco) place(c *decoCtx) {
	if len(c.logs) == 0 {
		return
	}
	r := c.r
	if r.NextFloat() >= d.prob {
		return
	}
	list := append([]BlockPos(nil), c.logs...)
	shuffle(list, r)
	for _, p := range list {
		ok := true
		for dd := 0; dd < 6; dd++ {
			if !stateInTag(blockAt(c.rg, relDir(p, dd, 1)), "logs") {
				ok = false
				break
			}
		}
		if ok {
			b := withProp(blockNamed("creaking_heart"), "creaking_heart_state", "dormant")
			c.set(p, withProp(b, "natural", "true"))
			return
		}
	}
}

// paleMossDeco is PaleMossDecorator: a pale moss patch at the lowest
// log (first minimum in shuffled order), then hanging moss under logs
// and leaves.
type paleMossDeco struct{ leaves, trunk, ground float32 }

func (d paleMossDeco) place(c *decoCtx) {
	r := c.r
	sh := shuffledCopy(c.logs, r)
	if len(sh) == 0 {
		return
	}
	low := sh[0]
	for _, p := range sh[1:] {
		if p[1] < low[1] {
			low = p
		}
	}
	if r.NextFloat() < d.ground {
		cf := c.g.configured("pale_moss_patch")
		cf.place(&featCtx{g: c.g, rg: c.rg}, r, offset(low, 0, 1, 0))
	}
	for _, p := range c.logs {
		if r.NextFloat() < d.trunk {
			if q := offset(p, 0, -1, 0); c.isAir(q) {
				addMossHanger(c, q)
			}
		}
	}
	for _, p := range c.leaves {
		if r.NextFloat() < d.leaves {
			if q := offset(p, 0, -1, 0); c.isAir(q) {
				addMossHanger(c, q)
			}
		}
	}
}

func addMossHanger(c *decoCtx, p BlockPos) {
	moss := blockNamed("pale_hanging_moss")
	for c.isAir(offset(p, 0, -1, 0)) && !(float64(c.r.NextFloat()) < 0.5) {
		c.set(p, withProp(moss, "tip", "false"))
		p = offset(p, 0, -1, 0)
	}
	c.set(p, withProp(moss, "tip", "true"))
}
