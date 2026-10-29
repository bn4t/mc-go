package worldgen

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"mc-go/internal/world"
)

// Jigsaw structures (JigsawStructure + JigsawPlacement): template pools
// and their elements, the start piece, the breadth-by-priority child
// placement with its free-space shapes, projection (rigid /
// terrain_matching), the expansion hack, pool aliases, dimension
// padding and liquid settings; and PoolElementStructurePiece placement.

// Projections (StructureTemplatePool.Projection).
const (
	projTerrainMatching = iota
	projRigid
)

var projectionNames = [2]string{"terrain_matching", "rigid"}

// jigsawInfo is StructureTemplate.JigsawBlockInfo at a transformed
// position.
type jigsawInfo struct {
	pos                  BlockPos
	front, top           int // FrontAndTop directions
	rollable             bool
	name, target, pool   string
	placementPriority    int32
	selectionPriority    int32
	state                world.Block
}

// poolElement is a StructurePoolElement.
type poolElement interface {
	projection() int
	// jigsaws is getShuffledJigsawBlocks: the element's jigsaw blocks at
	// pos under rotation, shuffled with r then stably sorted by
	// selection priority, highest first.
	jigsaws(g *Generator, pos BlockPos, rot int, r Random) []jigsawInfo
	boundingBox(pos BlockPos, rot int) BoundingBox
	groundLevelDelta() int32
	place(c *PieceContext, pos, pivot BlockPos, rot int, liquid bool, keepJigsaws bool) bool
	raw() json.RawMessage
}

type emptyElement struct{ j json.RawMessage }

func (emptyElement) projection() int                                          { return projTerrainMatching }
func (emptyElement) jigsaws(*Generator, BlockPos, int, Random) []jigsawInfo     { return nil }
func (emptyElement) boundingBox(BlockPos, int) BoundingBox                    { return BoundingBox{} }
func (emptyElement) groundLevelDelta() int32                                  { return 1 }
func (emptyElement) place(*PieceContext, BlockPos, BlockPos, int, bool, bool) bool { return true }
func (e emptyElement) raw() json.RawMessage                                   { return e.j }

// singleElement is SinglePoolElement (legacy: LegacySinglePoolElement,
// which ignores air as well as structure blocks, last).
type singleElement struct {
	j        json.RawMessage
	location string
	tpl      *structTemplate
	procs    procSpec
	proj     int
	legacy   bool
	liquid   int // -1 unset, else 0 ignore / 1 apply waterlogging
}

func (e *singleElement) projection() int         { return e.proj }
func (e *singleElement) groundLevelDelta() int32 { return 1 }
func (e *singleElement) raw() json.RawMessage    { return e.j }

func (e *singleElement) template() *structTemplate {
	if e.tpl == nil {
		e.tpl = loadTemplate(e.location)
	}
	return e.tpl
}

func (e *singleElement) boundingBox(pos BlockPos, rot int) BoundingBox {
	s := newPlaceSettings()
	s.rotation = rot
	return bbFrom(e.template().boundingBox(s, pos))
}

// templateJigsaws is StructureTemplate.getJigsaws: the palette's jigsaw
// blocks (palette order) transformed by rotation and offset, states
// rotated.
func templateJigsaws(t *structTemplate, pos BlockPos, rot int) []jigsawInfo {
	if len(t.palettes) == 0 {
		return nil
	}
	pal := t.palettes[NewLegacy(BlockSeed(pos[0], pos[1], pos[2])).NextIntN(int32(len(t.palettes)))]
	var out []jigsawInfo
	for _, b := range pal {
		if !isBlock(b.state, "jigsaw") {
			continue
		}
		p := tplTransform(b.pos, mirNone, rot, BlockPos{})
		p = offset(p, pos[0], pos[1], pos[2])
		out = append(out, newJigsawInfo(p, rotateState(b.state, rot), b.nbt))
	}
	return out
}

func newJigsawInfo(p BlockPos, st world.Block, nbt map[string]any) jigsawInfo {
	ori, _ := world.StateProperty(st, "orientation")
	f, t, _ := strings.Cut(ori, "_")
	j := jigsawInfo{pos: p, front: dirNames[f], top: dirNames[t], state: st,
		name: "empty", target: "empty", pool: "empty"}
	str := func(k string) (string, bool) {
		v, ok := nbt[k].(string)
		return trimNS(v), ok
	}
	if v, ok := str("name"); ok {
		j.name = v
	}
	if v, ok := str("target"); ok {
		j.target = v
	}
	if v, ok := str("pool"); ok {
		j.pool = v
	}
	// StructureTemplate.getJointType: the saved joint, else aligned for
	// horizontal fronts and rollable for vertical ones.
	if v, ok := str("joint"); ok {
		j.rollable = v == "rollable"
	} else {
		j.rollable = j.front == dirUp || j.front == dirDown
	}
	j.placementPriority = nbtInt(nbt["placement_priority"])
	j.selectionPriority = nbtInt(nbt["selection_priority"])
	return j
}

// shuffleJigsaws is Util.shuffle followed by the stable selection
// priority sort.
func shuffleJigsaws(list []jigsawInfo, r Random) []jigsawInfo {
	for i := int32(len(list)); i > 1; i-- {
		j := r.NextIntN(i)
		list[i-1], list[j] = list[j], list[i-1]
	}
	sort.SliceStable(list, func(a, b int) bool { return list[a].selectionPriority > list[b].selectionPriority })
	return list
}

func (e *singleElement) jigsaws(g *Generator, pos BlockPos, rot int, r Random) []jigsawInfo {
	return shuffleJigsaws(templateJigsaws(e.template(), pos, rot), r)
}

// settings is SinglePoolElement.getSettings (LegacySinglePoolElement's
// override moves the ignore processor to the end and adds air).
func (e *singleElement) settings(rot int, bb BoundingBox, liquid bool, keepJigsaws bool) *placeSettings {
	s := newPlaceSettings()
	b := bb.internal()
	s.bb = &b
	s.rotation = rot
	s.knownShape = true
	keep := liquid
	if e.liquid >= 0 {
		keep = e.liquid == 1
	}
	s.keepLiquids = keep
	if !e.legacy {
		s.processors = append(s.processors, ignoreBlocksProc(map[string]bool{"structure_block": true}))
	}
	if !keepJigsaws {
		s.processors = append(s.processors, jigsawReplacementProc)
	}
	s.processors = append(s.processors, e.procs.procs...)
	s.finalizers = append(s.finalizers, e.procs.fins...)
	s.entirePiece = e.procs.entire
	if e.proj == projTerrainMatching {
		s.processors = append(s.processors, gravityProc(HMWorldSurfaceWG, -1))
	}
	if e.legacy {
		s.processors = append(s.processors, ignoreBlocksProc(map[string]bool{"air": true, "structure_block": true}))
	}
	return s
}

func (e *singleElement) place(c *PieceContext, pos, pivot BlockPos, rot int, liquid bool, keepJigsaws bool) bool {
	s := e.settings(rot, c.ChunkBox, liquid, keepJigsaws)
	// Data markers (structure blocks in DATA mode) are handled by
	// SinglePoolElement.handleDataMarker, a no-op.
	return e.template().placeInWorld(c.Region, pos, pivot, s, c.Random)
}

// featureElement is FeaturePoolElement: a placed feature at the
// element position, one downward "minecraft:bottom" jigsaw.
type featureElement struct {
	j       json.RawMessage
	feature string
	proj    int
}

func (e *featureElement) projection() int         { return e.proj }
func (e *featureElement) groundLevelDelta() int32 { return 1 }
func (e *featureElement) raw() json.RawMessage    { return e.j }

func (e *featureElement) boundingBox(pos BlockPos, rot int) BoundingBox {
	return BoundingBox{pos[0], pos[1], pos[2], pos[0], pos[1], pos[2]}
}

func (e *featureElement) jigsaws(g *Generator, pos BlockPos, rot int, r Random) []jigsawInfo {
	st, _ := world.BlockStateOf("jigsaw", map[string]string{"orientation": "down_south"})
	return []jigsawInfo{newJigsawInfo(pos, st, map[string]any{"name": "minecraft:bottom", "final_state": "minecraft:air",
		"pool": "minecraft:empty", "target": "minecraft:empty", "joint": "rollable"})}
}

func (e *featureElement) place(c *PieceContext, pos, pivot BlockPos, rot int, liquid bool, keepJigsaws bool) bool {
	return c.G.placed(e.feature).place(&placeCtx{g: c.G, rg: c.Region}, c.Random, pos)
}

// listElement is ListPoolElement: elements placed together; jigsaws of
// the first, the union of the boxes.
type listElement struct {
	j     json.RawMessage
	elems []poolElement
	proj  int
}

func (e *listElement) projection() int         { return e.proj }
func (e *listElement) groundLevelDelta() int32 { return 1 }
func (e *listElement) raw() json.RawMessage    { return e.j }

func (e *listElement) jigsaws(g *Generator, pos BlockPos, rot int, r Random) []jigsawInfo {
	return e.elems[0].jigsaws(g, pos, rot, r)
}

func (e *listElement) boundingBox(pos BlockPos, rot int) BoundingBox {
	var out BoundingBox
	first := true
	for _, el := range e.elems {
		if _, ok := el.(emptyElement); ok {
			continue
		}
		b := el.boundingBox(pos, rot)
		if first {
			out, first = b, false
		} else {
			out = out.Encapsulate(b)
		}
	}
	return out
}

func (e *listElement) place(c *PieceContext, pos, pivot BlockPos, rot int, liquid bool, keepJigsaws bool) bool {
	for _, el := range e.elems {
		if !el.place(c, pos, pivot, rot, liquid, keepJigsaws) {
			return false
		}
	}
	return true
}

// templatePool is StructureTemplatePool: elements repeated by weight.
type templatePool struct {
	name      string
	fallback  string
	templates []poolElement
	maxSize   int32
	maxOnce   sync.Once
}

// getMaxSize is StructureTemplatePool.getMaxSize: the tallest
// non-empty element's unrotated box at the origin.
func (p *templatePool) getMaxSize() int32 {
	p.maxOnce.Do(func() {
		for _, e := range p.templates {
			if _, ok := e.(emptyElement); ok {
				continue
			}
			p.maxSize = max(p.maxSize, e.boundingBox(BlockPos{}, rotNone).YSpan())
		}
	})
	return p.maxSize
}

func (p *templatePool) shuffled(r Random) []poolElement {
	out := append([]poolElement(nil), p.templates...)
	for i := int32(len(out)); i > 1; i-- {
		j := r.NextIntN(i)
		out[i-1], out[j] = out[j], out[i-1]
	}
	return out
}

var poolsMu sync.Mutex

func (g *Generator) pool(name string) *templatePool {
	name = trimNS(name)
	poolsMu.Lock()
	if g.pools == nil {
		g.pools = map[string]*templatePool{}
	}
	if p, ok := g.pools[name]; ok {
		poolsMu.Unlock()
		return p
	}
	poolsMu.Unlock()
	raw, ok := g.RS.data.Get("template_pool/" + name)
	if !ok {
		return nil
	}
	var o obj
	jsonUnmarshal(raw, &o)
	p := &templatePool{name: name, fallback: trimNS(o.str("fallback"))}
	var elems []struct {
		Element json.RawMessage `json:"element"`
		Weight  int             `json:"weight"`
	}
	jsonUnmarshal(o["elements"], &elems)
	for _, e := range elems {
		el := g.compileElement(e.Element, -1)
		for i := 0; i < e.Weight; i++ {
			p.templates = append(p.templates, el)
		}
	}
	poolsMu.Lock()
	g.pools[name] = p
	poolsMu.Unlock()
	return p
}

// compileElement parses a pool element; proj >= 0 overrides the
// element's own projection (ListPoolElement.setProjection).
func (g *Generator) compileElement(raw json.RawMessage, proj int) poolElement {
	var o obj
	jsonUnmarshal(raw, &o)
	p := projTerrainMatching
	if o.optStr("projection") == "rigid" {
		p = projRigid
	}
	if proj >= 0 {
		p = proj
	}
	switch t := trimNS(o.str("element_type")); t {
	case "empty_pool_element":
		return emptyElement{raw}
	case "single_pool_element", "legacy_single_pool_element":
		e := &singleElement{j: raw, location: trimNS(o.str("location")), proj: p, legacy: t == "legacy_single_pool_element", liquid: -1}
		e.procs = g.compileProcSpec(o["processors"])
		if v := o.optStr("override_liquid_settings"); v != "" {
			e.liquid = 0
			if v == "apply_waterlogging" {
				e.liquid = 1
			}
		}
		return e
	case "feature_pool_element":
		var name string
		jsonUnmarshal(o["feature"], &name)
		return &featureElement{j: raw, feature: trimNS(name), proj: p}
	case "list_pool_element":
		var list []json.RawMessage
		jsonUnmarshal(o["elements"], &list)
		e := &listElement{j: raw, proj: p}
		for _, l := range list {
			e.elems = append(e.elems, g.compileElement(l, p))
		}
		return e
	default:
		panic("worldgen: unknown pool element " + t)
	}
}

// JigsawPiece is PoolElementStructurePiece.
type JigsawPiece struct {
	element   poolElement
	Position  BlockPos
	GroundLvl int32 // ground_level_delta
	Rotation  int
	Box       BoundingBox
	Junctions []JigsawJunction
	liquid    bool // apply waterlogging
}

// JigsawJunction is a recorded jigsaw connection (beardifier input).
type JigsawJunction struct {
	SourceX, SourceGroundY, SourceZ, DeltaY int32
	DestProjection                          int
}

func (p *JigsawPiece) Type() string             { return "jigsaw" }
func (p *JigsawPiece) BoundingBox() BoundingBox { return p.Box }
func (p *JigsawPiece) GenDepth() int32          { return 0 }
func (p *JigsawPiece) Orientation() int32       { return -1 }

// Move is PoolElementStructurePiece.move (box and position).
func (p *JigsawPiece) Move(dx, dy, dz int32) {
	p.Box = p.Box.Moved(dx, dy, dz)
	p.Position = offset(p.Position, dx, dy, dz)
}

// PostProcess is PoolElementStructurePiece.postProcess: the element
// placed at the piece position, jigsaws replaced.
func (p *JigsawPiece) PostProcess(c *PieceContext) {
	p.element.place(c, p.Position, c.Pivot, p.Rotation, p.liquid, false)
}

// ElementJSON is the piece's pool element as datapack JSON (the saved
// "pool_element").
func (p *JigsawPiece) ElementJSON() json.RawMessage { return p.element.raw() }

// Projection is the element's projection name.
func (p *JigsawPiece) Projection() string { return projectionNames[p.element.projection()] }

// LiquidSettings is the saved liquid_settings name.
func (p *JigsawPiece) LiquidSettings() string {
	if p.liquid {
		return "apply_waterlogging"
	}
	return "ignore_waterlogging"
}

// canAttach is JigsawBlock.canAttach.
func canAttach(a, b jigsawInfo) bool {
	return a.front == oppositeDir(b.front) && (a.rollable || a.top == b.top) && a.target == b.name
}

// freeShape is the VoxelShape a jigsaw child must fit in: a base box
// minus the boxes already taken (Shapes.join ONLY_FIRST); a box fits
// when its cells (AABB deflated by 0.25) all lie in the shape.
type freeShape struct {
	base  BoundingBox
	taken []BoundingBox
}

func (f *freeShape) fits(b BoundingBox) bool {
	if b.MinX < f.base.MinX || b.MaxX > f.base.MaxX || b.MinY < f.base.MinY || b.MaxY > f.base.MaxY || b.MinZ < f.base.MinZ || b.MaxZ > f.base.MaxZ {
		return false
	}
	for _, t := range f.taken {
		if t.Intersects(b) {
			return false
		}
	}
	return true
}

type jigsawConfig struct {
	startPool       string
	startJigsawName string
	maxDepth        int32
	startHeight     heightProvider
	expansionHack   bool
	projectHM       int // -1 none
	maxH, maxV      int32
	aliases         []json.RawMessage
	padBottom       int32
	padTop          int32
	liquid          bool
}

func init() {
	structureTypes["jigsaw"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		o := d.raw
		cfg := &jigsawConfig{startPool: trimNS(o.str("start_pool")), maxDepth: o.i32("size"), projectHM: -1, liquid: true}
		cfg.startJigsawName = trimNS(o.optStr("start_jigsaw_name"))
		cfg.startHeight = parseHeightProvider(o["start_height"])
		jsonUnmarshal(o["use_expansion_hack"], &cfg.expansionHack)
		if v := o.optStr("project_start_to_heightmap"); v != "" {
			cfg.projectHM = heightmapType(v)
		}
		var md obj
		if json.Unmarshal(o["max_distance_from_center"], &md) == nil {
			cfg.maxH = md.i32("horizontal")
			cfg.maxV = md.optI32("vertical", 4064)
		} else {
			cfg.maxH = o.i32("max_distance_from_center")
			cfg.maxV = cfg.maxH
		}
		if v, ok := o["pool_aliases"]; ok {
			jsonUnmarshal(v, &cfg.aliases)
		}
		if v, ok := o["dimension_padding"]; ok {
			var po obj
			if json.Unmarshal(v, &po) == nil {
				cfg.padBottom = po.optI32("bottom", 0)
				cfg.padTop = po.optI32("top", 0)
			} else {
				cfg.padBottom = o.i32("dimension_padding")
				cfg.padTop = cfg.padBottom
			}
		}
		if v := o.optStr("liquid_settings"); v == "ignore_waterlogging" {
			cfg.liquid = false
		}
		return func(c *GenContext) (*GenerationStub, bool) { return jigsawFind(c, cfg) }
	}
}

// jigsawFind is JigsawStructure.findGenerationPoint +
// JigsawPlacement.addPieces up to the stub.
func jigsawFind(c *GenContext, cfg *jigsawConfig) (*GenerationStub, bool) {
	g := c.G
	r := c.Random
	y := cfg.startHeight(r, genCtx{g.RS.settings.minY, g.RS.settings.height})
	startPos := BlockPos{c.ChunkX * 16, y, c.ChunkZ * 16}
	alias := g.poolAliases(cfg.aliases, startPos, c.Seed)
	rot := int(r.NextIntN(4))
	pool := g.pool(alias(cfg.startPool))
	if pool == nil || len(pool.templates) == 0 {
		return nil, false
	}
	elem := pool.templates[r.NextIntN(int32(len(pool.templates)))]
	if _, ok := elem.(emptyElement); ok {
		return nil, false
	}
	jpos := startPos
	if cfg.startJigsawName != "" {
		found := false
		for _, j := range elem.jigsaws(g, startPos, rot, r) {
			if j.name == cfg.startJigsawName {
				jpos, found = j.pos, true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	off := BlockPos{jpos[0] - startPos[0], jpos[1] - startPos[1], jpos[2] - startPos[2]}
	adj := BlockPos{startPos[0] - off[0], startPos[1] - off[1], startPos[2] - off[2]}
	start := &JigsawPiece{element: elem, Position: adj, GroundLvl: elem.groundLevelDelta(), Rotation: rot,
		Box: elem.boundingBox(adj, rot), liquid: cfg.liquid}
	bb := start.Box
	cx := (bb.MaxX + bb.MinX) / 2
	cz := (bb.MaxZ + bb.MinZ) / 2
	sy := adj[1]
	if cfg.projectHM >= 0 {
		sy = startPos[1] + g.BaseHeight(cx, cz, cfg.projectHM)
	}
	start.Move(0, sy-(bb.MinY+start.GroundLvl), 0)
	lvMin, lvMax := g.RS.lvMinY, g.RS.lvMinY+g.RS.lvHeight-1
	if cfg.padBottom != 0 || cfg.padTop != 0 {
		if start.Box.MinY < lvMin+cfg.padBottom || start.Box.MaxY > lvMax-cfg.padTop {
			return nil, false
		}
	}
	stubY := sy + off[1]
	return &GenerationStub{Pos: BlockPos{cx, stubY, cz}, Generate: func(add func(StructurePiece)) {
		pieces := []*JigsawPiece{start}
		if cfg.maxDepth > 0 {
			free := &freeShape{base: BoundingBox{cx - cfg.maxH, max(stubY-cfg.maxV, lvMin+cfg.padBottom), cz - cfg.maxH,
				cx + cfg.maxH, min(stubY+cfg.maxV+1, lvMax+1-cfg.padTop) - 1, cz + cfg.maxH}}
			free.taken = append(free.taken, start.Box)
			pl := &jigsawPlacer{g: g, maxDepth: cfg.maxDepth, r: r, alias: alias, liquid: cfg.liquid, hack: cfg.expansionHack}
			pl.pieces = &pieces
			pl.tryPlacingChildren(start, free, 0)
			for {
				st, ok := pl.next()
				if !ok {
					break
				}
				pl.tryPlacingChildren(st.piece, st.free, st.depth)
			}
		}
		for _, p := range pieces {
			add(p)
		}
	}}, true
}

type pieceState struct {
	piece *JigsawPiece
	free  *freeShape
	depth int32
}

// jigsawPlacer is JigsawPlacement.Placer with its
// SequencedPriorityIterator (highest placement priority first, FIFO
// within a priority).
type jigsawPlacer struct {
	g        *Generator
	maxDepth int32
	r        Random
	alias    func(string) string
	liquid   bool
	hack     bool
	pieces   *[]*JigsawPiece
	queues   map[int32][]pieceState
}

func (pl *jigsawPlacer) add(s pieceState, prio int32) {
	if pl.queues == nil {
		pl.queues = map[int32][]pieceState{}
	}
	pl.queues[prio] = append(pl.queues[prio], s)
}

func (pl *jigsawPlacer) next() (pieceState, bool) {
	best := int32(0)
	found := false
	for p, q := range pl.queues {
		if len(q) > 0 && (!found || p > best) {
			best, found = p, true
		}
	}
	if !found {
		return pieceState{}, false
	}
	q := pl.queues[best]
	s := q[0]
	pl.queues[best] = q[1:]
	return s, true
}

const unsetHeight = int32(-1 << 31)

func (pl *jigsawPlacer) tryPlacingChildren(piece *JigsawPiece, parentFree *freeShape, depth int32) {
	g := pl.g
	r := pl.r
	elem := piece.element
	parentRigid := elem.projection() == projRigid
	var inner *freeShape
	bb := piece.Box
	minY := bb.MinY
	for _, j := range elem.jigsaws(g, piece.Position, piece.Rotation, r) {
		fo := dirOffsets[j.front]
		attach := offset(j.pos, fo[0], fo[1], fo[2])
		relY := j.pos[1] - minY
		surfaceY := unsetHeight
		pool := g.pool(pl.alias(j.pool))
		if pool == nil || (len(pool.templates) == 0 && pool.name != "empty") {
			continue
		}
		fallback := g.pool(pool.fallback)
		if fallback == nil || (len(fallback.templates) == 0 && fallback.name != "empty") {
			continue
		}
		free := parentFree
		if bb.IsInside(attach) {
			if inner == nil {
				inner = &freeShape{base: bb}
			}
			free = inner
		}
		var cands []poolElement
		if depth != pl.maxDepth {
			cands = append(cands, pool.shuffled(r)...)
		}
		cands = append(cands, fallback.shuffled(r)...)
	candidates:
		for _, cand := range cands {
			if _, ok := cand.(emptyElement); ok {
				break
			}
			for _, rot := range shuffledRotations(r) {
				cj := cand.jigsaws(g, BlockPos{}, rot, r)
				cbb := cand.boundingBox(BlockPos{}, rot)
				expand := int32(0)
				if pl.hack && cbb.YSpan() <= 16 {
					for _, k := range cj {
						ko := dirOffsets[k.front]
						if !cbb.IsInside(offset(k.pos, ko[0], ko[1], ko[2])) {
							continue
						}
						kp := g.pool(pl.alias(k.pool))
						v := int32(0)
						if kp != nil {
							v = kp.getMaxSize()
							if fb := g.pool(kp.fallback); fb != nil {
								v = max(v, fb.getMaxSize())
							}
						}
						expand = max(expand, v)
					}
				}
				for _, k := range cj {
					if !canAttach(j, k) {
						continue
					}
					off := BlockPos{attach[0] - k.pos[0], attach[1] - k.pos[1], attach[2] - k.pos[2]}
					box := cand.boundingBox(off, rot)
					cminY := box.MinY
					childRigid := cand.projection() == projRigid
					kY := k.pos[1]
					dy := relY - kY + dirOffsets[j.front][1]
					var placedY int32
					if parentRigid && childRigid {
						placedY = minY + dy
					} else {
						if surfaceY == unsetHeight {
							surfaceY = g.BaseHeight(j.pos[0], j.pos[2], HMWorldSurfaceWG)
						}
						placedY = surfaceY - kY
					}
					shift := placedY - cminY
					box = box.Moved(0, shift, 0)
					cpos := offset(off, 0, shift, 0)
					if expand > 0 {
						m := max(expand+1, box.MaxY-box.MinY)
						box = box.Encapsulate(BoundingBox{box.MinX, box.MinY + m, box.MinZ, box.MinX, box.MinY + m, box.MinZ})
					}
					if !free.fits(box) {
						continue
					}
					free.taken = append(free.taken, box)
					parentDelta := piece.GroundLvl
					childDelta := cand.groundLevelDelta()
					if childRigid {
						childDelta = parentDelta - dy
					}
					child := &JigsawPiece{element: cand, Position: cpos, GroundLvl: childDelta, Rotation: rot, Box: box, liquid: pl.liquid}
					var jy int32
					switch {
					case parentRigid:
						jy = minY + relY
					case childRigid:
						jy = placedY + kY
					default:
						if surfaceY == unsetHeight {
							surfaceY = g.BaseHeight(j.pos[0], j.pos[2], HMWorldSurfaceWG)
						}
						jy = surfaceY + dy/2
					}
					piece.Junctions = append(piece.Junctions, JigsawJunction{attach[0], jy - relY + parentDelta, attach[2], dy, cand.projection()})
					child.Junctions = append(child.Junctions, JigsawJunction{j.pos[0], jy - kY + childDelta, j.pos[2], -dy, elem.projection()})
					*pl.pieces = append(*pl.pieces, child)
					if depth+1 <= pl.maxDepth {
						pl.add(pieceState{child, free, depth + 1}, j.placementPriority)
					}
					break candidates
				}
			}
		}
	}
}

// shuffledRotations is Rotation.getShuffled.
func shuffledRotations(r Random) []int {
	out := []int{rotNone, rotCW90, rotCW180, rotCCW90}
	for i := int32(4); i > 1; i-- {
		j := r.NextIntN(i)
		out[i-1], out[j] = out[j], out[i-1]
	}
	return out
}

// poolAliases is PoolAliasLookup.create: each binding resolved with a
// random positioned at the start (RandomSource.create(seed)
// .forkPositional().at(pos)); unbound pools map to themselves.
func (g *Generator) poolAliases(bindings []json.RawMessage, pos BlockPos, seed int64) func(string) string {
	if len(bindings) == 0 {
		return func(s string) string { return trimNS(s) }
	}
	r := NewLegacy(seed).ForkPositional().At(pos[0], pos[1], pos[2])
	m := map[string]string{}
	var resolve func(raw json.RawMessage)
	resolve = func(raw json.RawMessage) {
		var o obj
		jsonUnmarshal(raw, &o)
		switch trimNS(o.str("type")) {
		case "direct":
			m[trimNS(o.str("alias"))] = trimNS(o.str("target"))
		case "random":
			w := parseWeighted(o["targets"], func(raw json.RawMessage) string {
				var s string
				jsonUnmarshal(raw, &s)
				return trimNS(s)
			})
			m[trimNS(o.str("alias"))] = w.pick(r)
		case "random_group":
			w := parseWeighted(o["groups"], func(raw json.RawMessage) []json.RawMessage {
				var l []json.RawMessage
				jsonUnmarshal(raw, &l)
				return l
			})
			for _, b := range w.pick(r) {
				resolve(b)
			}
		default:
			panic("worldgen: unknown pool alias binding " + o.str("type"))
		}
	}
	for _, b := range bindings {
		resolve(b)
	}
	return func(s string) string {
		s = trimNS(s)
		if t, ok := m[s]; ok {
			return t
		}
		return s
	}
}
