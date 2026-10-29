package worldgen

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"sync"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// Structure templates (StructureTemplate) and their placement
// (StructureTemplate.placeInWorld with StructurePlaceSettings and the
// processors), shared by the fossil and template features and meant
// for structure pieces later.

var (
	structuresOnce sync.Once
	structuresRaw  map[string][]byte
	templateMu     sync.Mutex
	templateCache  = map[string]*structTemplate{}
)

// templateNBT returns a bundled template's uncompressed NBT by id
// ("fossil/skull_1"); the blob decodes once on first use.
func templateNBT(id string) ([]byte, bool) {
	structuresOnce.Do(func() {
		raw, err := base64.StdEncoding.DecodeString(structuresBlob)
		if err != nil {
			panic(err)
		}
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			panic(err)
		}
		plain, err := io.ReadAll(zr)
		if err != nil {
			panic(err)
		}
		m := map[string][]byte{}
		for len(plain) > 0 {
			n := binary.BigEndian.Uint32(plain)
			name := string(plain[4 : 4+n])
			plain = plain[4+n:]
			d := binary.BigEndian.Uint32(plain)
			m[name] = plain[4 : 4+d]
			plain = plain[4+d:]
		}
		structuresRaw = m
	})
	b, ok := structuresRaw[trimNS(id)]
	return b, ok
}

// TemplateIDs lists the bundled structure template ids, sorted.
func TemplateIDs() []string {
	templateNBT("")
	out := make([]string, 0, len(structuresRaw))
	for k := range structuresRaw {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// tplBlock is StructureTemplate.StructureBlockInfo.
type tplBlock struct {
	pos   BlockPos
	state world.Block
	nbt   map[string]any
}

type structTemplate struct {
	size     BlockPos
	palettes [][]tplBlock
	entities int
}

// loadTemplate is StructureTemplateManager.getOrCreate +
// StructureTemplate.load for a bundled template.
func loadTemplate(id string) *structTemplate {
	id = trimNS(id)
	templateMu.Lock()
	defer templateMu.Unlock()
	if t, ok := templateCache[id]; ok {
		return t
	}
	raw, ok := templateNBT(id)
	if !ok {
		panic("worldgen: unknown structure template " + id)
	}
	_, v, err := nbt.ReadNamed(bytes.NewReader(raw))
	if err != nil {
		panic("worldgen: template " + id + ": " + err.Error())
	}
	root := v.(map[string]any)
	t := &structTemplate{size: nbtPos(root["size"])}
	blocks, _ := root["blocks"].(nbt.List)
	if pals, ok := root["palettes"].(nbt.List); ok {
		for _, p := range pals.Items {
			t.palettes = append(t.palettes, loadPalette(p.(nbt.List), blocks))
		}
	} else {
		p, _ := root["palette"].(nbt.List)
		t.palettes = append(t.palettes, loadPalette(p, blocks))
	}
	if e, ok := root["entities"].(nbt.List); ok {
		t.entities = len(e.Items)
	}
	templateCache[id] = t
	return t
}

func nbtInt(v any) int32 {
	switch x := v.(type) {
	case int32:
		return x
	case int8:
		return int32(x)
	case int16:
		return int32(x)
	case int64:
		return int32(x)
	}
	return 0
}

func nbtPos(v any) BlockPos {
	l, _ := v.(nbt.List)
	var p BlockPos
	for i := 0; i < 3 && i < len(l.Items); i++ {
		p[i] = nbtInt(l.Items[i])
	}
	return p
}

// readBlockState is NbtUtils.readBlockState: unknown blocks read as
// air, unknown or invalid properties are skipped.
func readBlockState(c map[string]any) world.Block {
	name, _ := c["Name"].(string)
	b, ok := world.BlockStateOf(trimNS(name), nil)
	if !ok {
		return world.Air
	}
	if props, ok := c["Properties"].(map[string]any); ok {
		for k, v := range props {
			s, _ := v.(string)
			if nb, ok := world.WithProperty(b, k, s); ok {
				b = nb
			}
		}
	}
	return b
}

// loadPalette is StructureTemplate.loadPalette: blocks split into
// full-cube blocks, other blocks and block-entity blocks, each sorted
// by (y, x, z), concatenated in that order. (Block.hasDynamicShape,
// which also routes a block to the "other" list, is not a measured
// per-state fact; full-collision dynamic-shape blocks are filed as
// full cubes.)
func loadPalette(pal nbt.List, blocks nbt.List) []tplBlock {
	states := make([]world.Block, len(pal.Items))
	for i, p := range pal.Items {
		states[i] = readBlockState(p.(map[string]any))
	}
	var full, other, withNBT []tplBlock
	for _, it := range blocks.Items {
		c := it.(map[string]any)
		b := tplBlock{pos: nbtPos(c["pos"]), state: world.Air}
		if s := nbtInt(c["state"]); s >= 0 && int(s) < len(states) {
			b.state = states[s]
		}
		if n, ok := c["nbt"].(map[string]any); ok {
			b.nbt = n
		}
		switch {
		case b.nbt != nil:
			withNBT = append(withNBT, b)
		case fullCollision(b.state):
			full = append(full, b)
		default:
			other = append(other, b)
		}
	}
	less := func(l []tplBlock) func(i, j int) bool {
		return func(i, j int) bool {
			a, b := l[i].pos, l[j].pos
			if a[1] != b[1] {
				return a[1] < b[1]
			}
			if a[0] != b[0] {
				return a[0] < b[0]
			}
			return a[2] < b[2]
		}
	}
	sort.SliceStable(full, less(full))
	sort.SliceStable(other, less(other))
	sort.SliceStable(withNBT, less(withNBT))
	out := append(append(full, other...), withNBT...)
	return out
}

// Rotation and Mirror enum values (vanilla ordinal order).
const (
	rotNone = iota
	rotCW90
	rotCW180
	rotCCW90
)

const (
	mirNone = iota
	mirLeftRight
	mirFrontBack
)

var rotationNames = map[string]int{"none": rotNone, "clockwise_90": rotCW90, "180": rotCW180, "counterclockwise_90": rotCCW90}

// box is a BoundingBox (inclusive).
type box struct{ x0, y0, z0, x1, y1, z1 int32 }

func (b *box) inside(p BlockPos) bool {
	return p[0] >= b.x0 && p[0] <= b.x1 && p[1] >= b.y0 && p[1] <= b.y1 && p[2] >= b.z0 && p[2] <= b.z1
}

func boxCorners(a, b BlockPos) box {
	return box{min(a[0], b[0]), min(a[1], b[1]), min(a[2], b[2]), max(a[0], b[0]), max(a[1], b[1]), max(a[2], b[2])}
}

// tplProcessor is a StructureProcessor.processBlock; nil drops the block.
type tplProcessor func(rg *Region, offset, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock

// placeSettings is StructurePlaceSettings.
type placeSettings struct {
	mirror, rotation int
	pivot            BlockPos
	bb               *box
	random           Random
	processors       []tplProcessor
	ignoreEntities   bool
	keepLiquids      bool // LiquidSettings.IGNORE_WATERLOGGING when false
}

func newPlaceSettings() *placeSettings { return &placeSettings{keepLiquids: true} }

// randomAt is StructurePlaceSettings.getRandom.
func (s *placeSettings) randomAt(p BlockPos) Random {
	if s.random != nil {
		return s.random
	}
	return NewLegacy(BlockSeed(p[0], p[1], p[2]))
}

// tplTransform is StructureTemplate.transform(BlockPos, Mirror,
// Rotation, pivot).
func tplTransform(p BlockPos, mirror, rot int, pivot BlockPos) BlockPos {
	x, y, z := p[0], p[1], p[2]
	mirrored := true
	switch mirror {
	case mirLeftRight:
		z = -z
	case mirFrontBack:
		x = -x
	default:
		mirrored = false
	}
	px, pz := pivot[0], pivot[2]
	switch rot {
	case rotCCW90:
		return BlockPos{px - pz + z, y, px + pz - x}
	case rotCW90:
		return BlockPos{px + pz - z, y, pz - px + x}
	case rotCW180:
		return BlockPos{px + px - x, y, pz + pz - z}
	}
	if mirrored {
		return BlockPos{x, y, z}
	}
	return p
}

// sizeFor is StructureTemplate.getSize(Rotation).
func (t *structTemplate) sizeFor(rot int) BlockPos {
	if rot == rotCW90 || rot == rotCCW90 {
		return BlockPos{t.size[2], t.size[1], t.size[0]}
	}
	return t.size
}

// zeroPosition is StructureTemplate.getZeroPositionWithTransform.
func (t *structTemplate) zeroPosition(p BlockPos, mirror, rot int) BlockPos {
	sx, sz := t.size[0]-1, t.size[2]-1
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

// boundingBox is StructureTemplate.getBoundingBox(settings, pos).
func (t *structTemplate) boundingBox(s *placeSettings, p BlockPos) box {
	a := tplTransform(BlockPos{}, s.mirror, s.rotation, s.pivot)
	b := tplTransform(offset(t.size, -1, -1, -1), s.mirror, s.rotation, s.pivot)
	bb := boxCorners(a, b)
	bb.x0, bb.x1 = bb.x0+p[0], bb.x1+p[0]
	bb.y0, bb.y1 = bb.y0+p[1], bb.y1+p[1]
	bb.z0, bb.z1 = bb.z0+p[2], bb.z1+p[2]
	return bb
}

// horizontal direction rotation (Direction.getClockWise order).
var cwNext = map[string]string{"north": "east", "east": "south", "south": "west", "west": "north"}

func rotateDirName(d string, rot int) string {
	if _, ok := cwNext[d]; !ok {
		return d
	}
	for i := 0; i < rot; i++ {
		d = cwNext[d]
	}
	return d
}

// rotateState is BlockState.rotate for the property families
// templates use: facing, axis, the 16-step rotation and the four
// horizontal side properties.
func rotateState(b world.Block, rot int) world.Block {
	if rot == rotNone {
		return b
	}
	_, props, ok := world.DescribeState(b)
	if !ok {
		return b
	}
	out := b
	if f, ok := props["facing"]; ok {
		out, _ = world.WithProperty(out, "facing", rotateDirName(f, rot))
	}
	if a, ok := props["axis"]; ok && rot != rotCW180 {
		switch a {
		case "x":
			out, _ = world.WithProperty(out, "axis", "z")
		case "z":
			out, _ = world.WithProperty(out, "axis", "x")
		}
	}
	if r, ok := props["rotation"]; ok {
		var v int
		json.Unmarshal([]byte(r), &v)
		out, _ = world.WithProperty(out, "rotation", itoa((v+4*rot)&15))
	}
	if _, ok := props["north"]; ok {
		if _, ok := props["east"]; ok {
			for _, d := range []string{"north", "east", "south", "west"} {
				if v, ok := props[d]; ok {
					if nb, ok := world.WithProperty(out, rotateDirName(d, rot), v); ok {
						out = nb
					}
				}
			}
		}
	}
	return out
}

// mirrorState is BlockState.mirror for the same families.
func mirrorState(b world.Block, mirror int) world.Block {
	if mirror == mirNone {
		return b
	}
	_, props, ok := world.DescribeState(b)
	if !ok {
		return b
	}
	swap := map[string]string{"north": "south", "south": "north"}
	if mirror == mirFrontBack {
		swap = map[string]string{"east": "west", "west": "east"}
	}
	out := b
	if f, ok := props["facing"]; ok {
		if g, ok := swap[f]; ok {
			out, _ = world.WithProperty(out, "facing", g)
		}
	}
	if r, ok := props["rotation"]; ok {
		var v int
		json.Unmarshal([]byte(r), &v)
		if mirror == mirLeftRight {
			v = (16 - v) & 15
		} else {
			v = (24 - v) & 15
		}
		out, _ = world.WithProperty(out, "rotation", itoa(v))
	}
	for from, to := range swap {
		if v, ok := props[from]; ok {
			if _, ok := props[to]; ok {
				if nb, ok := world.WithProperty(out, to, v); ok {
					out = nb
				}
			}
		}
	}
	return out
}

// processBlocks is StructureTemplate.processBlockInfos (no processor
// here evaluates the entire piece state, so blocks outside the
// settings' box are dropped before processing).
func processBlocks(rg *Region, off, pivot BlockPos, s *placeSettings, blocks []tplBlock) []tplBlock {
	var out []tplBlock
	for _, b := range blocks {
		wp := tplTransform(b.pos, s.mirror, s.rotation, s.pivot)
		wp = offset(wp, off[0], off[1], off[2])
		if s.bb != nil && !s.bb.inside(wp) {
			continue
		}
		cur := &tplBlock{pos: wp, state: b.state, nbt: b.nbt}
		for _, p := range s.processors {
			if cur == nil {
				break
			}
			cur = p(rg, off, pivot, b.pos, cur, s)
		}
		if cur != nil {
			out = append(out, *cur)
		}
	}
	return out
}

// randomizableContainer marks block entities that take a loot seed
// when a template places them (RandomizableContainer).
var randomizableContainer = map[string]bool{
	"ChestBlock": true, "BarrelBlock": true, "DispenserBlock": true, "HopperBlock": true,
	"ShulkerBoxBlock": true, "DecoratedPotBlock": true,
}

func isRandomizable(b world.Block) bool {
	n, _ := world.StateOwner(b)
	for _, c := range blockClass(n) {
		if randomizableContainer[c] {
			return true
		}
	}
	return false
}

// placeLiquid is LiquidBlockContainer.placeLiquid for waterloggable
// blocks: an unwaterlogged block takes in a water source.
func placeLiquid(rg *Region, p BlockPos, st, fluid world.Block) {
	if v, ok := world.StateProperty(st, "waterlogged"); ok && v == "false" && fluidID(fluid) == "water" {
		nb, _ := world.WithProperty(st, "waterlogged", "true")
		rg.set(p, nb)
	}
}

func liquidContainer(b world.Block) bool { return world.HasProperty(b, "waterlogged") }

// placeInWorld is StructureTemplate.placeInWorld: a palette drawn from
// the settings' random, the processed blocks written (block-entity
// blocks over a barrier first, containers drawing a loot seed from
// random), waterlogging spread from neighbouring sources, then the
// shape updates (edge neighbours, then every placed cell) for the block
// families in shapeByClass. Entities are not placed.
func (t *structTemplate) placeInWorld(rg *Region, off, pivot BlockPos, s *placeSettings, random Random) bool {
	if len(t.palettes) == 0 {
		return false
	}
	blocks := t.palettes[s.randomAt(off).NextIntN(int32(len(t.palettes)))]
	if len(blocks) == 0 && (s.ignoreEntities || t.entities == 0 || t.size[0] < 1 || t.size[1] < 1 || t.size[2] < 1) {
		return false
	}
	var pending, placed []BlockPos
	isSource := map[BlockPos]bool{}
	inPlaced := map[BlockPos]bool{}
	barrier := blockNamed("barrier")
	for _, b := range processBlocks(rg, off, pivot, s, blocks) {
		if s.bb != nil && !s.bb.inside(b.pos) {
			continue
		}
		var fluid world.Block
		if s.keepLiquids {
			fluid = rg.at(b.pos)
		}
		st := rotateState(mirrorState(b.state, s.mirror), s.rotation)
		if b.nbt != nil {
			rg.set(b.pos, barrier)
		}
		if !rg.set(b.pos, st) {
			continue
		}
		placed = append(placed, b.pos)
		inPlaced[b.pos] = true
		// The placed block's block entity takes the template's tag (a
		// container first drawing its LootTableSeed from random).
		if b.nbt != nil {
			if be := rg.blockEntity(b.pos); be != nil {
				tag := b.nbt
				if isRandomizable(rg.at(b.pos)) {
					tag = cloneNBT(b.nbt).(map[string]any)
					tag["LootTableSeed"] = random.NextLong()
				}
				loadTemplateNBT(be, tag)
			}
		}
		if !s.keepLiquids {
			continue
		}
		if hasFluid(st) && isFluidSource(st) {
			isSource[b.pos] = true
		} else if liquidContainer(st) {
			placeLiquid(rg, b.pos, st, fluid)
			if !(hasFluid(fluid) && isFluidSource(fluid)) {
				pending = append(pending, b.pos)
			}
		}
	}
	dirs := [5]int{dirUp, dirNorth, dirEast, dirSouth, dirWest}
	for changed := true; changed && len(pending) > 0; {
		changed = false
		kept := pending[:0]
		for _, p := range pending {
			fl := rg.at(p)
			for _, d := range dirs {
				if hasFluid(fl) && isFluidSource(fl) {
					break
				}
				o := dirOffsets[d]
				q := offset(p, o[0], o[1], o[2])
				nf := rg.at(q)
				if hasFluid(nf) && isFluidSource(nf) && !isSource[q] {
					fl = nf
				}
			}
			if hasFluid(fl) && isFluidSource(fl) {
				if st := rg.at(p); liquidContainer(st) {
					placeLiquid(rg, p, st, fl)
					changed = true
					continue
				}
			}
			kept = append(kept, p)
		}
		pending = kept
	}
	// Not a known shape: blocks just outside the placed cells react to
	// them (updateShapeAtEdge), then every placed cell is refreshed
	// from its neighbours in placement order.
	for _, p := range placed {
		for d := 0; d < 6; d++ {
			o := dirOffsets[d]
			q := offset(p, o[0], o[1], o[2])
			if inPlaced[q] {
				continue
			}
			in, out := rg.at(p), rg.at(q)
			ni := updateShape(rg, in, p, d, out)
			if ni != in {
				rg.set(p, ni)
			}
			if no := updateShape(rg, out, q, oppositeDir(d), ni); no != out {
				rg.set(q, no)
			}
		}
	}
	for _, p := range placed {
		st := rg.at(p)
		if ns := updateFromNeighbourShapes(rg, st, p); ns != st {
			rg.set(p, ns)
		}
	}
	return true
}

// shapeFn is BlockState.updateShape for one block family: the state at
// p reacting to its neighbour nb in direction dir.
type shapeFn func(rg *Region, st world.Block, p BlockPos, dir int, nb world.Block) world.Block

// shapeByClass holds the updateShape overrides template placement
// needs, keyed by behaviour class; other blocks keep their state.
var shapeByClass = map[string]shapeFn{
	// SnowyBlock.updateShape: the block above decides snowy.
	"SnowyBlock": func(rg *Region, st world.Block, p BlockPos, dir int, nb world.Block) world.Block {
		if dir != dirUp {
			return st
		}
		v := "false"
		if blockInTag(nb, "snow") {
			v = "true"
		}
		out, _ := world.WithProperty(st, "snowy", v)
		return out
	},
	"SpeleothemBlock": speleothemShape,
	// PotentSulfurBlock.updateShape (validBlockState): dry without a
	// water source above, else continuous / dormant / wet by the block
	// below and its fluid.
	"PotentSulfurBlock": func(rg *Region, st world.Block, p BlockPos, dir int, nb world.Block) world.Block {
		set := func(v string) world.Block { out, _ := world.WithProperty(st, "potent_sulfur_state", v); return out }
		if fluidID(rg.at(offset(p, 0, 1, 0))) != "water" {
			return set("dry")
		}
		below := rg.at(offset(p, 0, -1, 0))
		srcOrEmpty := !hasFluid(below) || isFluidSource(below)
		if blockInTag(below, "causes_continuous_geyser_eruptions") && srcOrEmpty {
			return set("continuous")
		}
		if blockInTag(below, "causes_periodic_geyser_eruptions") && srcOrEmpty {
			if v, _ := world.StateProperty(st, "potent_sulfur_state"); v == "erupting" {
				return st
			}
			return set("dormant")
		}
		return set("wet")
	},
}

func speleothemDir(b world.Block, d string) bool {
	if !blockInTag(b, "speleothems") {
		return false
	}
	v, _ := world.StateProperty(b, "vertical_direction")
	return v == d
}

// speleothemShape is SpeleothemBlock.updateShape: vertical neighbours
// recompute the thickness (calculateSpeleothemThickness); an
// unsupported speleothem keeps its state (it only schedules a tick).
func speleothemShape(rg *Region, st world.Block, p BlockPos, dir int, nb world.Block) world.Block {
	if dir != dirUp && dir != dirDown {
		return st
	}
	td, _ := world.StateProperty(st, "vertical_direction")
	tip, opp := dirUp, dirDown
	tipName, oppName := "up", "down"
	if td == "down" {
		tip, opp = dirDown, dirUp
		tipName, oppName = "down", "up"
	}
	name, _ := world.StateOwner(st)
	if dir == opp {
		o := dirOffsets[opp]
		base := offset(p, o[0], o[1], o[2])
		bs := rg.at(base)
		if !sturdyFull(bs, tip) && !(speleothemDir(bs, tipName) && isBlock(bs, name)) {
			return st
		}
	}
	th, _ := world.StateProperty(st, "thickness")
	merge := th == "tip_merge"
	t := dirOffsets[tip]
	next := rg.at(offset(p, t[0], t[1], t[2]))
	var out string
	switch {
	case speleothemDir(next, oppName) && isBlock(next, name):
		nt, _ := world.StateProperty(next, "thickness")
		if merge || nt == "tip_merge" {
			out = "tip_merge"
		} else {
			out = "tip"
		}
	case !speleothemDir(next, tipName):
		out = "tip"
	default:
		nt, _ := world.StateProperty(next, "thickness")
		if nt == "tip" || nt == "tip_merge" {
			out = "frustum"
		} else {
			o := dirOffsets[opp]
			if speleothemDir(rg.at(offset(p, o[0], o[1], o[2])), tipName) {
				out = "middle"
			} else {
				out = "base"
			}
		}
	}
	res, _ := world.WithProperty(st, "thickness", out)
	return res
}

func updateShape(rg *Region, st world.Block, p BlockPos, dir int, nb world.Block) world.Block {
	n, _ := world.StateOwner(st)
	for _, c := range blockClass(n) {
		if f, ok := shapeByClass[c]; ok {
			return f(rg, st, p, dir, nb)
		}
	}
	return st
}

// updateShapeOrder is Block.UPDATE_SHAPE_ORDER.
var updateShapeOrder = [6]int{dirWest, dirEast, dirNorth, dirSouth, dirDown, dirUp}

// updateFromNeighbourShapes is Block.updateFromNeighbourShapes.
func updateFromNeighbourShapes(rg *Region, st world.Block, p BlockPos) world.Block {
	for _, d := range updateShapeOrder {
		o := dirOffsets[d]
		st = updateShape(rg, st, p, d, rg.at(offset(p, o[0], o[1], o[2])))
	}
	return st
}

// UnsupportedProcessors collects template processor types without an
// implementation (they pass blocks through unchanged).
var UnsupportedProcessors = map[string]bool{}

// compileProcessors resolves a processor list given by name or inline
// ({"processors": [...]} or a bare list).
func (g *Generator) compileProcessors(raw json.RawMessage) []tplProcessor {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		r, ok := g.RS.data.Get("processor_list/" + trimNS(name))
		if !ok {
			panic("worldgen: unknown processor list " + name)
		}
		raw = r
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		var o obj
		jsonUnmarshal(raw, &o)
		jsonUnmarshal(o["processors"], &list)
	}
	var out []tplProcessor
	for _, p := range list {
		out = append(out, g.compileProcessor(p))
	}
	return out
}

func (g *Generator) compileProcessor(raw json.RawMessage) tplProcessor {
	var o obj
	jsonUnmarshal(raw, &o)
	switch t := trimNS(o.str("processor_type")); t {
	case "nop":
		return func(rg *Region, off, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock { return cur }
	case "block_rot":
		// BlockRotProcessor: rottable blocks (all, when unset) survive
		// with probability integrity.
		integrity := parseF32(o["integrity"])
		var rottable map[string]bool
		if v, ok := o["rottable_blocks"]; ok {
			rottable = blockNameSet(v)
		}
		return func(rg *Region, off, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock {
			r := s.randomAt(cur.pos)
			if rottable != nil && !inTag(cur.state, rottable) {
				return cur
			}
			if r.NextFloat() <= integrity {
				return cur
			}
			return nil
		}
	case "protected_blocks":
		// ProtectedBlockProcessor: never overwrite the tagged blocks.
		set := blockNameSet(o["value"])
		return func(rg *Region, off, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock {
			if inTag(rg.at(cur.pos), set) {
				return nil
			}
			return cur
		}
	case "block_ignore":
		set := map[string]bool{}
		var bl []obj
		jsonUnmarshal(o["blocks"], &bl)
		for _, b := range bl {
			set[trimNS(b.str("Name"))] = true
		}
		return func(rg *Region, off, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock {
			if inTag(cur.state, set) {
				return nil
			}
			return cur
		}
	case "rule":
		// RuleProcessor: the first matching rule replaces the state; its
		// tests draw from a random seeded by the world position.
		var rules []obj
		jsonUnmarshal(o["rules"], &rules)
		type rule struct {
			input, loc ruleTest
			out        world.Block
		}
		var rs []rule
		for _, ru := range rules {
			if p, ok := ru["position_predicate"]; ok {
				var po obj
				jsonUnmarshal(p, &po)
				if trimNS(po.str("predicate_type")) != "always_true" {
					UnsupportedProcessors["rule/"+po.str("predicate_type")] = true
				}
			}
			var st obj
			jsonUnmarshal(ru["output_state"], &st)
			rs = append(rs, rule{compileRuleTest(ru["input_predicate"]), compileRuleTest(ru["location_predicate"]), stateFromJSON(st)})
		}
		return func(rg *Region, off, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock {
			r := NewLegacy(BlockSeed(cur.pos[0], cur.pos[1], cur.pos[2]))
			for _, ru := range rs {
				if ru.input(cur.state, r) && ru.loc(rg.at(cur.pos), r) {
					return &tplBlock{pos: cur.pos, state: ru.out, nbt: cur.nbt}
				}
			}
			return cur
		}
	default:
		g.featMu.Lock()
		UnsupportedProcessors[t] = true
		g.featMu.Unlock()
		return func(rg *Region, off, pivot, local BlockPos, cur *tplBlock, s *placeSettings) *tplBlock { return cur }
	}
}

func init() {
	featureBuilders["fossil"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		var fossils, overlays []string
		jsonUnmarshal(cfg["fossil_structures"], &fossils)
		jsonUnmarshal(cfg["overlay_structures"], &overlays)
		fp := g.compileProcessors(cfg["fossil_processors"])
		op := g.compileProcessors(cfg["overlay_processors"])
		maxEmpty := cfg.i32("max_empty_corners_allowed")
		return func(c *featCtx, r Random, p BlockPos) bool {
			return placeFossil(c.rg, r, fossils, overlays, fp, op, maxEmpty, p)
		}
	}
	featureBuilders["template"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		type entry struct {
			id   string
			rots []int
		}
		w := parseWeighted(cfg["templates"], func(raw json.RawMessage) entry {
			var o obj
			jsonUnmarshal(raw, &o)
			e := entry{id: o.str("id"), rots: []int{rotNone, rotCW90, rotCW180, rotCCW90}}
			if v, ok := o["rotations"]; ok {
				var names []string
				jsonUnmarshal(v, &names)
				e.rots = nil
				for _, n := range names {
					e.rots = append(e.rots, rotationNames[strings.ToLower(n)])
				}
			}
			return e
		})
		return func(c *featCtx, r Random, p BlockPos) bool {
			e := w.pick(r)
			rot := e.rots[r.NextIntN(int32(len(e.rots)))]
			return placeTemplateFeature(c.rg, r, loadTemplate(e.id), rot, p)
		}
	}
}

// rotatedUnit is Rotation.rotate(direction).getUnitVec3i for the
// negative x (west) and z (north) directions.
func rotatedUnit(d string, rot int) BlockPos {
	switch rotateDirName(d, rot) {
	case "north":
		return BlockPos{0, 0, -1}
	case "south":
		return BlockPos{0, 0, 1}
	case "west":
		return BlockPos{-1, 0, 0}
	}
	return BlockPos{1, 0, 0}
}

// placeTemplateFeature is TemplateFeature.place: the template is
// shifted by half its size along the rotated negative x and z axes and
// placed with the chosen rotation.
func placeTemplateFeature(rg *Region, r Random, t *structTemplate, rot int, p BlockPos) bool {
	ox := rotatedUnit("west", rot)
	oz := rotatedUnit("north", rot)
	hx, hz := t.size[0]/2, t.size[2]/2
	q := offset(p, ox[0]*hx+oz[0]*hz, 0, ox[2]*hx+oz[2]*hz)
	s := newPlaceSettings()
	s.rotation = rot
	s.random = r
	return t.placeInWorld(rg, q, q, s, r)
}

// placeFossil is FossilFeature.place: rotation and fossil index drawn,
// the template sunk 15-24 blocks under the lowest OCEAN_FLOOR_WG
// column it covers (at least minY+10), refused with too many open
// corners, then the fossil and its ore overlay placed with their
// processor lists, clipped to the chunk plus a 16-block margin.
func placeFossil(rg *Region, r Random, fossils, overlays []string, fp, op []tplProcessor, maxEmpty int32, origin BlockPos) bool {
	rot := int(r.NextIntN(4))
	i := r.NextIntN(int32(len(fossils)))
	fossil := loadTemplate(fossils[i])
	overlay := loadTemplate(overlays[i])
	cx, cz := origin[0]>>4, origin[2]>>4
	bb := &box{cx*16 - 16, rg.MinY(), cz*16 - 16, cx*16 + 15 + 16, rg.MaxY(), cz*16 + 15 + 16}
	s := newPlaceSettings()
	s.rotation = rot
	s.bb = bb
	s.random = r
	size := fossil.sizeFor(rot)
	base := offset(origin, -size[0]/2, 0, -size[2]/2)
	y := origin[1]
	for dx := int32(0); dx < size[0]; dx++ {
		for dz := int32(0); dz < size[2]; dz++ {
			y = min(y, rg.Height(HMOceanFloorWG, base[0]+dx, base[2]+dz))
		}
	}
	y = max(y-15-r.NextIntN(10), rg.MinY()+10)
	zero := fossil.zeroPosition(BlockPos{base[0], y, base[2]}, mirNone, rot)
	fb := fossil.boundingBox(s, zero)
	empty := int32(0)
	for _, x := range [2]int32{fb.x0, fb.x1} {
		for _, yy := range [2]int32{fb.y0, fb.y1} {
			for _, z := range [2]int32{fb.z0, fb.z1} {
				st := rg.at(BlockPos{x, yy, z})
				if isAir(st) || isBlock(st, "lava") || isBlock(st, "water") {
					empty++
				}
			}
		}
	}
	if empty > maxEmpty {
		return false
	}
	s.processors = fp
	fossil.placeInWorld(rg, zero, zero, s, r)
	s.processors = op
	overlay.placeInWorld(rg, zero, zero, s, r)
	return true
}
