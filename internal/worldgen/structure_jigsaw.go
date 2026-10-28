package worldgen

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"mc-go/internal/nbt"
)

// Jigsaw structures (villages, pillager outposts, bastions, ancient
// cities, trail ruins, trial chambers) grow from a start piece drawn
// from a template pool: each piece's jigsaw blocks, highest selection
// priority first, take a piece from their pool whose own jigsaw faces
// back and names what they target, turned so the two meet, so long as
// it fits the free space left. Pieces queue to grow by their jigsaw's
// placement priority, first in first out among equals, up to the
// structure's size in steps.

func init() {
	structureGenerators["minecraft:jigsaw"] = jigsawStructure
}

// poolElement is one entry of a template pool.
type poolElement struct {
	kind       string // single, list, feature or empty
	location   string // a single element's template
	elements   []*poolElement
	projection string // rigid or terrain_matching
	// tag is the element as the game saves it.
	tag map[string]any
}

// jigsawPool is a template pool: its elements repeated by weight, and
// the pool its jigsaws fall back to.
type jigsawPool struct {
	name      string
	templates []*poolElement
	fallback  string

	maxOnce sync.Once
	max     int32
}

var emptyPoolElement = &poolElement{kind: "empty", tag: map[string]any{"element_type": "minecraft:empty_pool_element"}}

var jigsawPools = sync.OnceValue(func() map[string]*jigsawPool {
	out := map[string]*jigsawPool{}
	for name, raw := range templatePoolData {
		m := raw.(map[string]any)
		p := &jigsawPool{name: name, fallback: m["fallback"].(string)}
		for _, e := range m["elements"].([]any) {
			em := e.(map[string]any)
			el := decodePoolElement(em["element"].(map[string]any))
			for i := 0; i < int(em["weight"].(float64)); i++ {
				p.templates = append(p.templates, el)
			}
		}
		out[name] = p
	}
	return out
})

func decodePoolElement(m map[string]any) *poolElement {
	e := &poolElement{tag: jsonTag(m).(map[string]any)}
	e.projection, _ = m["projection"].(string)
	switch m["element_type"] {
	case "minecraft:single_pool_element", "minecraft:legacy_single_pool_element":
		e.kind = "single"
		e.location = m["location"].(string)
	case "minecraft:list_pool_element":
		e.kind = "list"
		for _, s := range m["elements"].([]any) {
			e.elements = append(e.elements, decodePoolElement(s.(map[string]any)))
		}
	case "minecraft:feature_pool_element":
		e.kind = "feature"
	case "minecraft:empty_pool_element":
		return emptyPoolElement
	default:
		panic(fmt.Sprintf("worldgen: unknown pool element %v", m["element_type"]))
	}
	return e
}

// jsonTag turns a pool's JSON into the tag the game's codec writes for
// it: objects as compounds, arrays as lists, strings as strings.
func jsonTag(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range t {
			out[k] = jsonTag(e)
		}
		return out
	case []any:
		l := nbt.List{Elem: nbt.TagEnd}
		for _, e := range t {
			x := jsonTag(e)
			if _, ok := x.(map[string]any); ok {
				l.Elem = nbt.TagCompound
			} else {
				l.Elem = nbt.TagString
			}
			l.Items = append(l.Items, x)
		}
		return l
	case string:
		return t
	}
	panic(fmt.Sprintf("worldgen: no tag for pool JSON %T", v))
}

func (p *jigsawPool) shuffled(r Random) []*poolElement {
	out := append([]*poolElement(nil), p.templates...)
	javaShuffle(out, r)
	return out
}

// maxHeight is the tallest of the pool's templates.
func (p *jigsawPool) maxHeight() int32 {
	p.maxOnce.Do(func() {
		for _, e := range p.templates {
			if e != emptyPoolElement {
				p.max = max(p.max, e.box(Point{}, rotNone).spanY())
			}
		}
	})
	return p.max
}

// javaShuffle is the game's list shuffle: from the end, each slot swaps
// with a random slot at or before it.
func javaShuffle[T any](s []T, r Random) {
	for i := len(s); i > 1; i-- {
		j := r.NextIntn(int32(i))
		s[i-1], s[j] = s[j], s[i-1]
	}
}

// jigsawInfo is a jigsaw block as a placement sees it.
type jigsawInfo struct {
	pos                  Point
	front, top           int
	rollable             bool
	name, target, pool   string
	placement, selection int32
}

// The jigsaw block's orientation is "<front>_<top>".
var dirByName = map[string]int{"down": dirDown, "up": dirUp, "north": dirNorth, "south": dirSouth, "west": dirWest, "east": dirEast}

// rotateDir turns a horizontal direction clockwise by a rotation.
func rotateDir(d int, rot rotation) int {
	if d == dirUp || d == dirDown {
		return d
	}
	ring := [4]int{dirNorth, dirEast, dirSouth, dirWest}
	i := 0
	for ring[i] != d {
		i++
	}
	return ring[(i+int(rot))&3]
}

func oppositeDir(d int) int {
	switch d {
	case dirDown:
		return dirUp
	case dirUp:
		return dirDown
	case dirNorth:
		return dirSouth
	case dirSouth:
		return dirNorth
	case dirWest:
		return dirEast
	}
	return dirWest
}

func stepY(d int) int32 {
	switch d {
	case dirUp:
		return 1
	case dirDown:
		return -1
	}
	return 0
}

func tagString(m map[string]any, key, def string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return def
}

func tagInt(m map[string]any, key string) int32 {
	if v, ok := m[key].(int32); ok {
		return v
	}
	return 0
}

// templateJigsaws are a template's jigsaw blocks, placed at pos and
// turned, in palette order.
func templateJigsaws(name string, pos Point, rot rotation) []jigsawInfo {
	t := loadTemplate(name)
	if len(t.palettes) == 0 {
		return nil
	}
	all := t.jigsaws()
	raw := all[0]
	if len(all) > 1 {
		raw = all[NewLegacy(BlockSeed(pos.X, pos.Y, pos.Z)).NextIntn(int32(len(all)))]
	}
	out := make([]jigsawInfo, len(raw))
	for i, j := range raw {
		j.pos = transformPos(j.pos, mirrorNone, rot, Point{}).add(pos)
		j.front, j.top = rotateDir(j.front, rot), rotateDir(j.top, rot)
		out[i] = j
	}
	return out
}

// jigsaws are each palette's jigsaw blocks as the template holds them,
// in palette order.
func (t *structureTemplate) jigsaws() [][]jigsawInfo {
	t.jigsawOnce.Do(func() {
		for _, palette := range t.palettes {
			var list []jigsawInfo
			for _, b := range palette {
				if !b.state.is("jigsaw") {
					continue
				}
				front, top, _ := strings.Cut(b.state.prop("orientation"), "_")
				j := jigsawInfo{pos: b.pos, front: dirByName[front], top: dirByName[top],
					name: tagString(b.nbt, "name", "minecraft:empty"), target: tagString(b.nbt, "target", "minecraft:empty"),
					pool: tagString(b.nbt, "pool", "minecraft:empty"), placement: tagInt(b.nbt, "placement_priority"),
					selection: tagInt(b.nbt, "selection_priority")}
				switch tagString(b.nbt, "joint", "") {
				case "rollable":
					j.rollable = true
				case "aligned":
				default:
					// Unset, a jigsaw facing up or down may roll.
					j.rollable = j.front == dirUp || j.front == dirDown
				}
				list = append(list, j)
			}
			t.jigsawList = append(t.jigsawList, list)
		}
	})
	return t.jigsawList
}

// jigsaws are the element's jigsaw blocks placed at pos and turned,
// shuffled and then by selection priority, highest first.
func (e *poolElement) jigsaws(pos Point, rot rotation, r Random) []jigsawInfo {
	switch e.kind {
	case "single":
		js := templateJigsaws(e.location, pos, rot)
		javaShuffle(js, r)
		stableSortBy(js, func(a, b jigsawInfo) bool { return a.selection > b.selection })
		return js
	case "list":
		return e.elements[0].jigsaws(pos, rot, r)
	case "feature":
		return []jigsawInfo{{pos: pos, front: dirDown, top: dirSouth, rollable: true, name: "minecraft:bottom",
			target: "minecraft:empty", pool: "minecraft:empty"}}
	}
	return nil
}

func stableSortBy[T any](s []T, less func(a, b T) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// box is the element's bounding box placed at pos and turned.
func (e *poolElement) box(pos Point, rot rotation) boundingBox {
	switch e.kind {
	case "single":
		return loadTemplate(e.location).boundingBox(placeSettings{rot: rot}, pos)
	case "list":
		var out boundingBox
		first := true
		for _, s := range e.elements {
			if s == emptyPoolElement {
				continue
			}
			b := s.box(pos, rot)
			if first {
				out, first = b, false
			} else {
				out.encapsulate(b)
			}
		}
		return out
	case "feature":
		return boxAt(pos)
	}
	panic("worldgen: an empty pool element has no box")
}

// canAttach reports whether jigsaw b can take a's place: facing it,
// with the same top unless a may roll, and named what a targets.
func canAttach(a, b jigsawInfo) bool {
	return a.front == oppositeDir(b.front) && (a.rollable || a.top == b.top) && a.target == b.name
}

type jigsawJunction struct {
	x, groundY, z, deltaY int32
	projection            string
}

// jigsawPiece is a placed pool element.
type jigsawPiece struct {
	pieceBase
	element     *poolElement
	pos         Point
	groundDelta int32
	rot         rotation
	junctions   []jigsawJunction
	liquid      string
}

func (p *jigsawPiece) move(dx, dy, dz int32) {
	p.box = p.box.moved(dx, dy, dz)
	p.pos = p.pos.add(Point{dx, dy, dz})
}

func (p *jigsawPiece) saveTag(tag map[string]any) {
	tag["PosX"], tag["PosY"], tag["PosZ"] = p.pos.X, p.pos.Y, p.pos.Z
	tag["ground_level_delta"] = p.groundDelta
	tag["pool_element"] = p.element.tag
	tag["rotation"] = rotationNames[p.rot]
	junctions := nbt.List{Elem: nbt.TagCompound}
	for _, j := range p.junctions {
		junctions.Items = append(junctions.Items, map[string]any{"source_x": j.x, "source_ground_y": j.groundY,
			"source_z": j.z, "delta_y": j.deltaY, "dest_proj": j.projection})
	}
	tag["junctions"] = junctions
	if p.liquid != "apply_waterlogging" {
		tag["liquid_settings"] = p.liquid
	}
}

func newJigsawPiece(e *poolElement, pos Point, delta int32, rot rotation, box boundingBox, liquid string) *jigsawPiece {
	return &jigsawPiece{pieceBase: newPieceBase("minecraft:jigsaw", 0, box), element: e, pos: pos, groundDelta: delta, rot: rot, liquid: liquid}
}

// freeSpace is where pieces may still go: a box less the boxes taken
// from it.
type freeSpace struct {
	outer boundingBox
	taken []boundingBox
}

func (f *freeSpace) fits(b boundingBox) bool {
	o := f.outer
	if b.minX < o.minX || b.minY < o.minY || b.minZ < o.minZ || b.maxX > o.maxX || b.maxY > o.maxY || b.maxZ > o.maxZ {
		return false
	}
	for _, t := range f.taken {
		if t.intersects(b) {
			return false
		}
	}
	return true
}

// poolAliases redirect pools for one structure.
type poolAliases map[string]string

func (a poolAliases) lookup(pool string) string {
	if t, ok := a[pool]; ok {
		return t
	}
	return pool
}

// resolvePoolAliases draws a structure's pool aliases from a random
// seeded by the world seed and the start position.
func resolvePoolAliases(bindings []any, pos Point, seed int64) poolAliases {
	out := poolAliases{}
	if len(bindings) == 0 {
		return out
	}
	r := NewLegacy(seed).ForkPositional().At(pos.X, pos.Y, pos.Z)
	var resolve func(b map[string]any)
	resolve = func(b map[string]any) {
		switch b["type"] {
		case "minecraft:direct":
			out[b["alias"].(string)] = b["target"].(string)
		case "minecraft:random":
			out[b["alias"].(string)] = weightedPick(b["targets"].([]any), r).(string)
		case "minecraft:random_group":
			for _, g := range weightedPick(b["groups"].([]any), r).([]any) {
				resolve(g.(map[string]any))
			}
		default:
			panic(fmt.Sprintf("worldgen: unknown pool alias %v", b["type"]))
		}
	}
	for _, b := range bindings {
		resolve(b.(map[string]any))
	}
	return out
}

// weightedPick draws one of {data, weight} entries by weight.
func weightedPick(entries []any, r Random) any {
	total := int32(0)
	for _, e := range entries {
		total += int32(e.(map[string]any)["weight"].(float64))
	}
	n := r.NextIntn(total)
	for _, e := range entries {
		m := e.(map[string]any)
		if n -= int32(m["weight"].(float64)); n < 0 {
			return m["data"]
		}
	}
	panic("worldgen: weighted pick out of range")
}

// jigsawStructure finds the start piece's place: drawn from the start
// pool at the start height in the chunk's corner (anchored on a named
// jigsaw if the structure names one), centred on its box and, for most,
// lifted onto the terrain there.
func jigsawStructure(c *structureContext) (genStub, bool) {
	cfg := c.def.cfg
	r := c.random
	minY, height := c.genBounds()
	startY := decodeHeightProvider(cfg["start_height"]).sample(r, minY, height)
	start := Point{c.minX(), startY, c.minZ()}
	aliasList, _ := cfg["pool_aliases"].([]any)
	aliases := resolvePoolAliases(aliasList, start, c.seed)
	padBottom, padTop := int32(0), int32(0)
	switch v := cfg["dimension_padding"].(type) {
	case float64:
		padBottom, padTop = int32(v), int32(v)
	case map[string]any:
		padBottom, padTop = int32(cfgFloat(v, "bottom", 0)), int32(cfgFloat(v, "top", 0))
	}
	liquid := "apply_waterlogging"
	if l, ok := cfg["liquid_settings"].(string); ok {
		liquid = l
	}
	var maxH, maxV int32
	switch v := cfg["max_distance_from_center"].(type) {
	case float64:
		maxH, maxV = int32(v), int32(v)
	case map[string]any:
		maxH = int32(cfgFloat(v, "horizontal", 0))
		maxV = int32(cfgFloat(v, "vertical", float64(maxH)))
	}
	size := int32(cfgFloat(cfg, "size", 0))
	expansionHack := cfgBool(cfg, "use_expansion_hack")

	pools := jigsawPools()
	rot := randomRotation(r)
	startPool := cfg["start_pool"].(string)
	pool, ok := pools[aliases.lookup(startPool)]
	if !ok {
		pool = pools[startPool]
	}
	elem := emptyPoolElement
	if len(pool.templates) > 0 {
		elem = pool.templates[r.NextIntn(int32(len(pool.templates)))]
	}
	if elem == emptyPoolElement {
		return genStub{}, false
	}
	anchor := start
	if name, ok := cfg["start_jigsaw_name"].(string); ok {
		found := false
		for _, j := range elem.jigsaws(start, rot, r) {
			if j.name == name {
				anchor, found = j.pos, true
				break
			}
		}
		if !found {
			return genStub{}, false
		}
	}
	offset := Point{anchor.X - start.X, anchor.Y - start.Y, anchor.Z - start.Z}
	pos := Point{start.X - offset.X, start.Y - offset.Y, start.Z - offset.Z}
	piece := newJigsawPiece(elem, pos, 1, rot, elem.box(pos, rot), liquid)
	cx, cz := (piece.box.maxX+piece.box.minX)/2, (piece.box.maxZ+piece.box.minZ)/2
	y := pos.Y
	if hm, ok := cfg["project_start_to_heightmap"].(string); ok {
		heightmap := worldSurfaceWG
		if hm == "OCEAN_FLOOR_WG" {
			heightmap = oceanFloorWG
		}
		y = start.Y + c.g.baseHeight(cx, cz, heightmap)
	}
	piece.move(0, y-(piece.box.minY+piece.groundDelta), 0)
	levelMax := c.g.levelMinY + c.g.levelHeight - 1
	// Only a structure that sets a padding keeps its start clear of the
	// build limits.
	if cfg["dimension_padding"] != nil && (piece.box.minY < c.g.levelMinY+padBottom || piece.box.maxY > levelMax-padTop) {
		return genStub{}, false
	}
	stubY := y + offset.Y
	return genStub{Point{cx, stubY, cz}, func() []structurePiece {
		pieces := []structurePiece{piece}
		if size <= 0 {
			return nil
		}
		outer := boundingBox{cx - maxH, max(stubY-maxV, c.g.levelMinY+padBottom), cz - maxH,
			cx + maxH, min(stubY+maxV+1, levelMax+1-padTop) - 1, cz + maxH}
		p := &jigsawPlacer{c: c, pools: pools, aliases: aliases, maxDepth: size, liquid: liquid, expansionHack: expansionHack}
		p.queue.highest = math.MinInt32
		p.tryPlacingChildren(piece, &freeSpace{outer: outer, taken: []boundingBox{piece.box}}, 0)
		for {
			s, ok := p.queue.pop()
			if !ok {
				break
			}
			p.tryPlacingChildren(s.piece, s.free, s.depth)
		}
		for _, q := range p.pieces {
			pieces = append(pieces, q)
		}
		return pieces
	}}, true
}

type pieceState struct {
	piece *jigsawPiece
	free  *freeSpace
	depth int32
}

// pieceQueue serves the highest priority first, first in first out
// among equal priorities.
type pieceQueue struct {
	byPriority map[int32][]*pieceState
	highest    int32
}

func (q *pieceQueue) add(s *pieceState, priority int32) {
	if q.byPriority == nil {
		q.byPriority = map[int32][]*pieceState{}
	}
	q.byPriority[priority] = append(q.byPriority[priority], s)
	if priority >= q.highest {
		q.highest = priority
	}
}

func (q *pieceQueue) pop() (*pieceState, bool) {
	l := q.byPriority[q.highest]
	if len(l) == 0 {
		return nil, false
	}
	s := l[0]
	q.byPriority[q.highest] = l[1:]
	if len(l) == 1 {
		best := int32(math.MinInt32)
		for p, rest := range q.byPriority {
			if len(rest) > 0 && p > best {
				best = p
			}
		}
		q.highest = best
	}
	return s, true
}

type jigsawPlacer struct {
	c             *structureContext
	pools         map[string]*jigsawPool
	aliases       poolAliases
	maxDepth      int32
	liquid        string
	expansionHack bool
	queue         pieceQueue
	pieces        []*jigsawPiece
}

// tryPlacingChildren gives each of a piece's jigsaws the first piece
// from its pool, then its fallback pool, that fits.
func (p *jigsawPlacer) tryPlacingChildren(parent *jigsawPiece, free *freeSpace, depth int32) {
	c, r := p.c, p.c.random
	elem := parent.element
	rigid := elem.projection == "rigid"
	box := parent.box
	minY := box.minY
	var inner *freeSpace
	for _, jig := range elem.jigsaws(parent.pos, parent.rot, r) {
		at := jig.pos
		target := at.offset(jig.front)
		above := at.Y - minY
		surface := int32(math.MinInt32)
		ground := func() int32 {
			if surface == math.MinInt32 {
				surface = c.g.baseHeight(at.X, at.Z, worldSurfaceWG)
			}
			return surface
		}
		poolName := p.aliases.lookup(jig.pool)
		pool, ok := p.pools[poolName]
		if !ok || len(pool.templates) == 0 && poolName != "minecraft:empty" {
			continue
		}
		fallback, ok := p.pools[pool.fallback]
		if !ok || len(fallback.templates) == 0 && pool.fallback != "minecraft:empty" {
			continue
		}
		space := free
		if box.contains(target) {
			if inner == nil {
				inner = &freeSpace{outer: box}
			}
			space = inner
		}
		var candidates []*poolElement
		if depth != p.maxDepth {
			candidates = append(candidates, pool.shuffled(r)...)
		}
		candidates = append(candidates, fallback.shuffled(r)...)
	candidates:
		for _, cand := range candidates {
			if cand == emptyPoolElement {
				break
			}
			rots := []rotation{rotNone, rotCW90, rotCW180, rotCCW90}
			javaShuffle(rots, r)
			for _, rot := range rots {
				cjigs := cand.jigsaws(Point{}, rot, r)
				home := cand.box(Point{}, rot)
				expand := int32(0)
				if p.expansionHack && home.spanY() <= 16 {
					for _, cj := range cjigs {
						expand = max(expand, p.expansion(home, cj))
					}
				}
				for _, cj := range cjigs {
					if !canAttach(jig, cj) {
						continue
					}
					cpos := Point{target.X - cj.pos.X, target.Y - cj.pos.Y, target.Z - cj.pos.Z}
					cbox := cand.box(cpos, rot)
					crigid := cand.projection == "rigid"
					dy := above - cj.pos.Y + stepY(jig.front)
					var y int32
					if rigid && crigid {
						y = minY + dy
					} else {
						y = ground() - cj.pos.Y
					}
					shift := y - cbox.minY
					cbox = cbox.moved(0, shift, 0)
					cpos.Y += shift
					if expand > 0 {
						k := max(expand+1, cbox.maxY-cbox.minY)
						cbox.encapsulate(boxAt(Point{cbox.minX, cbox.minY + k, cbox.minZ}))
					}
					if !space.fits(cbox) {
						continue
					}
					space.taken = append(space.taken, cbox)
					parentDelta := parent.groundDelta
					childDelta := int32(1)
					if crigid {
						childDelta = parentDelta - dy
					}
					child := newJigsawPiece(cand, cpos, childDelta, rot, cbox, p.liquid)
					var groundY int32
					switch {
					case rigid:
						groundY = minY + above
					case crigid:
						groundY = y + cj.pos.Y
					default:
						groundY = ground() + dy/2
					}
					parent.junctions = append(parent.junctions, jigsawJunction{target.X, groundY - above + parentDelta, target.Z, dy, cand.projection})
					child.junctions = append(child.junctions, jigsawJunction{at.X, groundY - cj.pos.Y + childDelta, at.Z, -dy, elem.projection})
					p.pieces = append(p.pieces, child)
					if depth+1 <= p.maxDepth {
						p.queue.add(&pieceState{child, space, depth + 1}, jig.placement)
					}
					break candidates
				}
			}
		}
	}
}

// expansion is how far a candidate's box grows upward for a jigsaw that
// leads inside it: the tallest template its pools could put there.
func (p *jigsawPlacer) expansion(home boundingBox, j jigsawInfo) int32 {
	if !home.contains(j.pos.offset(j.front)) {
		return 0
	}
	pool, ok := p.pools[p.aliases.lookup(j.pool)]
	if !ok {
		return 0
	}
	h := pool.maxHeight()
	if fb, ok := p.pools[pool.fallback]; ok {
		h = max(h, fb.maxHeight())
	}
	return h
}
