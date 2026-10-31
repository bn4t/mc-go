package worldgen

import (
	"encoding/json"
	"fmt"

	"mc-go/internal/world"
)

// Ruined portals (RuinedPortalStructure, RuinedPortalPiece): a setup
// picked by weight, the air pocket draw, a portal (giant with 5%), a
// rotation and a front-back mirror about the template's center pivot;
// the start sits at the chunk's min corner at a height found from the
// surface (or ocean floor) under the template's center by the setup's
// vertical placement, lowered until three of the four corner noise
// columns are solid. The piece places only from the chunk holding its
// center, with the chunk box widened to the whole template, then
// spreads netherrack (or magma) around the base, hangs drip columns
// under it and adds vines and leaves for jungle-like setups.

const (
	rpOnLandSurface = iota
	rpPartlyBuried
	rpOnOceanFloor
	rpInMountain
	rpUnderground
	rpInNether
)

var rpPlacementNames = []string{"on_land_surface", "partly_buried", "on_ocean_floor", "in_mountain", "underground", "in_nether"}

func rpPlacement(s string) int {
	for i, n := range rpPlacementNames {
		if n == s {
			return i
		}
	}
	panic("worldgen: ruined portal placement " + s)
}

type rpSetup struct {
	placement                                          int
	airPocket, mossiness, weight                       float32
	overgrown, vines, canBeCold, replaceWithBlackstone bool
}

// rpProps is RuinedPortalPiece.Properties.
type rpProps struct {
	cold                                           bool
	mossiness                                      float32
	airPocket, overgrown, vines, replaceBlackstone bool
}

var (
	rpPortals      = []string{"portal_1", "portal_2", "portal_3", "portal_4", "portal_5", "portal_6", "portal_7", "portal_8", "portal_9", "portal_10"}
	rpGiantPortals = []string{"giant_portal_1", "giant_portal_2", "giant_portal_3"}
)

var rpMirrorNames = map[int]string{mirNone: "NONE", mirLeftRight: "LEFT_RIGHT", mirFrontBack: "FRONT_BACK"}

func mirrorFromName(s string) int {
	for k, v := range rpMirrorNames {
		if v == s {
			return k
		}
	}
	return mirNone
}

func init() {
	structureTypes["ruined_portal"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		var raw []obj
		jsonUnmarshal(d.raw["setups"], &raw)
		var setups []rpSetup
		for _, o := range raw {
			s := rpSetup{placement: rpPlacement(o.str("placement")), airPocket: parseF32(o["air_pocket_probability"]),
				mossiness: parseF32(o["mossiness"]), weight: parseF32(o["weight"])}
			jsonUnmarshal(o["overgrown"], &s.overgrown)
			jsonUnmarshal(o["vines"], &s.vines)
			jsonUnmarshal(o["can_be_cold"], &s.canBeCold)
			jsonUnmarshal(o["replace_with_blackstone"], &s.replaceWithBlackstone)
			setups = append(setups, s)
		}
		return func(c *GenContext) (*GenerationStub, bool) { return ruinedPortalFind(c, setups) }
	}
	pieceLoaders["rupo"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		rot := rotationFromName(fmt3str(t["Rotation"]))
		mir := mirrorFromName(fmt3str(t["Mirror"]))
		pl := rpPlacement(fmt3str(t["VerticalPlacement"]))
		pm, _ := t["Properties"].(map[string]any)
		props := rpProps{cold: tagInt(pm, "cold") != 0, mossiness: nbtFloat(pm["mossiness"]), airPocket: tagInt(pm, "air_pocket") != 0,
			overgrown: tagInt(pm, "overgrown") != 0, vines: tagInt(pm, "vines") != 0, replaceBlackstone: tagInt(pm, "replace_with_blackstone") != 0}
		tp, err := loadTplPiece(t, func(name string) placeSettings {
			tpl := loadTemplate(name)
			return ruinedPortalSettings(g, rot, mir, pl, BlockPos{tpl.size[0] / 2, 0, tpl.size[2] / 2}, props)
		})
		if err != nil {
			return nil, err
		}
		return &ruinedPortalPiece{tplPiece: tp, rot: rot, mir: mir, placement: pl, props: props}, nil
	}
}

// ruinedPortalFind is RuinedPortalStructure.findGenerationPoint.
func ruinedPortalFind(c *GenContext, setups []rpSetup) (*GenerationStub, bool) {
	g, r := c.G, c.Random
	setup := setups[0]
	if len(setups) > 1 {
		total := float32(0)
		for _, s := range setups {
			total += s.weight
		}
		f := r.NextFloat()
		for _, s := range setups {
			f -= s.weight / total
			if f < 0 {
				setup = s
				break
			}
		}
	}
	air := false
	switch setup.airPocket {
	case 0:
	case 1:
		air = true
	default:
		air = r.NextFloat() < setup.airPocket
	}
	var name string
	if r.NextFloat() < 0.05 {
		name = rpGiantPortals[r.NextIntN(int32(len(rpGiantPortals)))]
	} else {
		name = rpPortals[r.NextIntN(int32(len(rpPortals)))]
	}
	name = "minecraft:ruined_portal/" + name
	tpl := loadTemplate(name)
	rot := int(r.NextIntN(4))
	mir := mirFrontBack
	if r.NextFloat() < 0.5 {
		mir = mirNone
	}
	pivot := BlockPos{tpl.size[0] / 2, 0, tpl.size[2] / 2}
	origin := BlockPos{c.ChunkX * 16, 0, c.ChunkZ * 16}
	s := placeSettings{rotation: rot, mirror: mir, pivot: pivot}
	bb := bbFrom(tpl.boundingBox(&s, origin))
	ctr := bb.Center()
	y := g.BaseHeight(ctr[0], ctr[2], rpHeightmap(setup.placement)) - 1
	sy := ruinedPortalY(g, r, setup.placement, air, y, bb.YSpan(), bb)
	pos := BlockPos{origin[0], sy, origin[2]}
	return &GenerationStub{Pos: pos, Generate: func(add func(StructurePiece)) {
		props := rpProps{mossiness: setup.mossiness, airPocket: air, overgrown: setup.overgrown, vines: setup.vines,
			replaceBlackstone: setup.replaceWithBlackstone}
		if setup.canBeCold {
			// Native samples the chunk generator's biome source at the final
			// origin's quart coordinates while building the pieces. Live
			// /fillbiome edits do not change this generator-owned value.
			b := g.runtimeFlatBiome
			if b == "" {
				b = g.Biomes.Biome(g.RS, nil, quartFromBlock(pos[0]), quartFromBlock(pos[1]), quartFromBlock(pos[2]))
			}
			props.cold = g.RS.BiomeInfo(b).ColdEnoughToSnow(pos[0], pos[1], pos[2], g.seaLevel())
		}
		p := &ruinedPortalPiece{tplPiece: newTplPiece("rupo", name, ruinedPortalSettings(g, rot, mir, setup.placement, pivot, props), pos, 0),
			rot: rot, mir: mir, placement: setup.placement, props: props}
		add(p)
	}}, true
}

func rpHeightmap(pl int) int {
	if pl == rpOnOceanFloor {
		return HMOceanFloorWG
	}
	return HMWorldSurfaceWG
}

// randomBetweenInclusive is Mth.randomBetweenInclusive.
func randomBetweenInclusive(r Random, lo, hi int32) int32 { return r.NextIntN(hi-lo+1) + lo }

// ruinedPortalY is RuinedPortalStructure.findSuitableY.
func ruinedPortalY(g *Generator, r Random, pl int, air bool, y, ySpan int32, bb BoundingBox) int32 {
	floor := g.RS.lvMinY + 15
	var h int32
	within := func(lo, hi int32) int32 {
		if lo < hi {
			return randomBetweenInclusive(r, lo, hi)
		}
		return hi
	}
	switch pl {
	case rpInNether:
		switch {
		case air:
			h = randomBetweenInclusive(r, 32, 100)
		case r.NextFloat() < 0.5:
			h = randomBetweenInclusive(r, 27, 29)
		default:
			h = randomBetweenInclusive(r, 29, 100)
		}
	case rpInMountain:
		h = within(70, y-ySpan)
	case rpUnderground:
		h = within(floor, y-ySpan)
	case rpPartlyBuried:
		h = y - ySpan + randomBetweenInclusive(r, 2, 8)
	default:
		h = y
	}
	corners := [4][2]int32{{bb.MinX, bb.MinZ}, {bb.MaxX, bb.MinZ}, {bb.MinX, bb.MaxZ}, {bb.MaxX, bb.MaxZ}}
	var cols [4]baseColumn
	for i, c := range corners {
		if g.runtimeBaseBlock != nil {
			cols[i] = baseColumn{minY: g.RS.lvMinY, blocks: make([]world.Block, g.RS.lvHeight)}
			for j := range cols[i].blocks {
				cols[i].blocks[j] = g.runtimeBaseBlock(c[0], g.RS.lvMinY+int32(j), c[1])
			}
		} else {
			cols[i] = g.RS.baseColumn(c[0], c[1])
		}
	}
	pred := notAir
	if pl == rpOnOceanFloor {
		pred = blocksMotion
	}
	for ; h > floor; h-- {
		n := 0
		for _, col := range cols {
			if pred(col.at(h)) {
				n++
				if n == 3 {
					return h
				}
			}
		}
	}
	return h
}

// baseColumn is a NoiseColumn from ChunkGenerator.getBaseColumn.
type baseColumn struct {
	minY   int32
	blocks []world.Block
}

func (c baseColumn) at(y int32) world.Block {
	i := y - c.minY
	if i < 0 || int(i) >= len(c.blocks) {
		return world.Air
	}
	return c.blocks[i]
}

// baseColumn is NoiseBasedChunkGenerator.getBaseColumn: the noise
// column's states (aquifer and ore veins, no surface) over the clamped
// noise height range.
func (rs *RandomState) baseColumn(x, z int32) baseColumn {
	s := rs.settings
	minY := max(s.minY, rs.lvMinY)
	top := min(s.minY+s.height, rs.lvMinY+rs.lvHeight)
	cellW, cellH := s.sizeHorizontal*4, s.sizeVertical*4
	cellMinY := floorDiv(minY, cellH)
	cellCountY := floorDiv(top-minY, cellH)
	if cellCountY <= 0 {
		return baseColumn{minY: minY}
	}
	col := baseColumn{minY: cellMinY * cellH, blocks: make([]world.Block, cellCountY*cellH)}
	cellX, cellZ := floorDiv(x, cellW), floorDiv(z, cellW)
	fx := float64(x-cellX*cellW) / float64(cellW)
	fz := float64(z-cellZ*cellW) / float64(cellW)
	nc := newNoiseChunkCells(rs, 1, cellX*cellW, cellZ*cellW, false)
	nc.lazyCell = true
	subs := substanceBlocks()
	def := blockNamed(s.defaultBlock)
	nc.initializeForFirstCellX()
	nc.advanceCellX(0)
	defer func() { nc.interpolating = false }()
	for cy := cellCountY - 1; cy >= 0; cy-- {
		nc.selectCellYZ(cy, 0)
		for yy := cellH - 1; yy >= 0; yy-- {
			y := (cellMinY+cy)*cellH + yy
			nc.updateForY(y, float64(yy)/float64(cellH))
			nc.updateForX(x, fx)
			nc.updateForZ(z, fz)
			sub, _ := nc.substance()
			b := def
			if sub != SubDefault {
				b = subs[sub]
			}
			col.blocks[cy*cellH+yy] = b
		}
	}
	return col
}

// ruinedPortalSettings is RuinedPortalPiece.makeSettings.
func ruinedPortalSettings(g *Generator, rot, mir, pl int, pivot BlockPos, p rpProps) placeSettings {
	ignore := `{"Name":"minecraft:structure_block"},{"Name":"minecraft:air"}`
	if p.airPocket {
		ignore = `{"Name":"minecraft:structure_block"}`
	}
	rule := func(block string, prob float32, out string) string {
		if prob < 0 {
			return fmt.Sprintf(`{"input_predicate":{"predicate_type":"minecraft:block_match","block":"minecraft:%s"},"location_predicate":{"predicate_type":"minecraft:always_true"},"output_state":{"Name":"minecraft:%s"}}`, block, out)
		}
		return fmt.Sprintf(`{"input_predicate":{"predicate_type":"minecraft:random_block_match","block":"minecraft:%s","probability":%s},"location_predicate":{"predicate_type":"minecraft:always_true"},"output_state":{"Name":"minecraft:%s"}}`, block, jsonFloat32(prob), out)
	}
	rules := []string{rule("gold_block", 0.3, "air")}
	switch {
	case pl == rpOnOceanFloor:
		rules = append(rules, rule("lava", -1, "magma_block"))
	case p.cold:
		rules = append(rules, rule("lava", -1, "netherrack"))
	default:
		rules = append(rules, rule("lava", 0.2, "magma_block"))
	}
	if !p.cold {
		rules = append(rules, rule("netherrack", 0.07, "magma_block"))
	}
	procs := fmt.Sprintf(`[{"processor_type":"minecraft:block_ignore","blocks":[%s]},{"processor_type":"minecraft:rule","rules":[%s]},`+
		`{"processor_type":"minecraft:block_age","mossiness":%s},{"processor_type":"minecraft:protected_blocks","value":"#minecraft:features_cannot_replace"},`+
		`{"processor_type":"minecraft:lava_submerged_block"}`, ignore, joinComma(rules), jsonFloat32(p.mossiness))
	if p.replaceBlackstone {
		procs += `,{"processor_type":"minecraft:blackstone_replace"}`
	}
	procs += "]"
	spec := g.compileProcSpec(json.RawMessage(procs))
	s := *newPlaceSettings()
	s.rotation, s.mirror, s.pivot = rot, mir, pivot
	s.processors = spec.procs
	s.finalizers = spec.fins
	s.entirePiece = spec.entire
	return s
}

func joinComma(l []string) string {
	out := ""
	for i, s := range l {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

type ruinedPortalPiece struct {
	tplPiece
	rot, mir  int
	placement int
	props     rpProps
}

// SaveData is RuinedPortalPiece.addAdditionalSaveData.
func (p *ruinedPortalPiece) SaveData(t map[string]any) {
	p.saveTpl(t)
	t["Rotation"] = rotationEnumNames[p.rot]
	t["Mirror"] = rpMirrorNames[p.mir]
	t["VerticalPlacement"] = rpPlacementNames[p.placement]
	t["Properties"] = map[string]any{
		"cold": boolByte(p.props.cold), "mossiness": p.props.mossiness, "air_pocket": boolByte(p.props.airPocket),
		"overgrown": boolByte(p.props.overgrown), "vines": boolByte(p.props.vines),
		"replace_with_blackstone": boolByte(p.props.replaceBlackstone),
	}
}

// PostProcess is RuinedPortalPiece.postProcess: only the chunk holding
// the template's center places it, with the chunk box grown over the
// whole template.
func (p *ruinedPortalPiece) PostProcess(c *PieceContext) {
	s := p.set
	tb := bbFrom(p.tpl.boundingBox(&s, p.pos))
	if !c.ChunkBox.IsInside(tb.Center()) {
		return
	}
	wide := *c
	wide.ChunkBox = c.ChunkBox.Encapsulate(tb)
	p.placeTemplate(&wide)
	rg, r := c.Region, c.Random
	p.spreadNetherrack(rg, r)
	p.dripColumnsBelow(rg, r)
	if p.props.vines || p.props.overgrown {
		b := p.bb
		for z := b.MinZ; z <= b.MaxZ; z++ {
			for y := b.MinY; y <= b.MaxY; y++ {
				for x := b.MinX; x <= b.MaxX; x++ {
					q := BlockPos{x, y, z}
					if p.props.vines {
						p.maybeVines(rg, r, q)
					}
					if p.props.overgrown {
						p.maybeLeavesAbove(rg, r, q)
					}
				}
			}
		}
	}
}

var rpSpreadChance = [14]float32{1, 1, 1, 1, 1, 1, 1, 0.9, 0.9, 0.8, 0.7, 0.6, 0.4, 0.2}

// spreadNetherrack is RuinedPortalPiece.spreadNetherrack: a diamond of
// fading chance around the box center, on the surface (or not above
// the portal base for buried placements) within 3 of the base.
func (p *ruinedPortalPiece) spreadNetherrack(rg *Region, r Random) {
	surface := p.placement == rpOnLandSurface || p.placement == rpOnOceanFloor
	ctr := p.bb.Center()
	n := int32(len(rpSpreadChance))
	span := (p.bb.MaxX - p.bb.MinX + 1 + p.bb.MaxZ - p.bb.MinZ + 1) / 2
	shift := r.NextIntN(max(1, 8-span/2))
	hm := rpHeightmap(p.placement)
	for x := ctr[0] - n; x <= ctr[0]+n; x++ {
		for z := ctr[2] - n; z <= ctr[2]+n; z++ {
			d := absI32(x-ctr[0]) + absI32(z-ctr[2])
			k := max(0, d+shift)
			if k >= n {
				continue
			}
			if !(r.NextDouble() < float64(rpSpreadChance[k])) {
				continue
			}
			y := rg.Height(hm, x, z) - 1
			if !surface {
				y = min(p.bb.MinY, y)
			}
			q := BlockPos{x, y, z}
			if absI32(y-p.bb.MinY) > 3 || !p.netherrackReplaceable(rg, q) {
				continue
			}
			p.netherrackOrMagma(rg, r, q)
			if p.props.overgrown {
				p.maybeLeavesAbove(rg, r, q)
			}
			p.dripColumn(rg, r, offset(q, 0, -1, 0))
		}
	}
}

// netherrackReplaceable is canBlockBeReplacedByNetherrackOrMagma.
func (p *ruinedPortalPiece) netherrackReplaceable(rg *Region, q BlockPos) bool {
	st := rg.at(q)
	return !isBlock(st, "air") && !isBlock(st, "obsidian") && !blockInTag(st, "features_cannot_replace") &&
		(p.placement == rpInNether || !isBlock(st, "lava"))
}

// netherrackOrMagma is placeNetherrackOrMagma.
func (p *ruinedPortalPiece) netherrackOrMagma(rg *Region, r Random, q BlockPos) {
	if !p.props.cold && r.NextFloat() < 0.07 {
		rg.set(q, blockNamed("magma_block"))
	} else {
		rg.set(q, blockNamed("netherrack"))
	}
}

// dripColumn is addNetherrackDripColumn: up to 8 more blocks down,
// each with chance one half.
func (p *ruinedPortalPiece) dripColumn(rg *Region, r Random, q BlockPos) {
	p.netherrackOrMagma(rg, r, q)
	for i := 8; i > 0 && r.NextFloat() < 0.5; i-- {
		q[1]--
		p.netherrackOrMagma(rg, r, q)
	}
}

// dripColumnsBelow is addNetherrackDripColumnsBelowPortal.
func (p *ruinedPortalPiece) dripColumnsBelow(rg *Region, r Random) {
	b := p.bb
	for x := b.MinX + 1; x < b.MaxX; x++ {
		for z := b.MinZ + 1; z < b.MaxZ; z++ {
			q := BlockPos{x, b.MinY, z}
			if isBlock(rg.at(q), "netherrack") {
				p.dripColumn(rg, r, offset(q, 0, -1, 0))
			}
		}
	}
}

var horizontalPlane = [4]int{dirNorth, dirEast, dirSouth, dirWest}

// maybeVines is RuinedPortalPiece.maybeAddVines.
func (p *ruinedPortalPiece) maybeVines(rg *Region, r Random, q BlockPos) {
	st := rg.at(q)
	if isAir(st) || isBlock(st, "vine") {
		return
	}
	d := horizontalPlane[r.NextIntN(4)]
	o := dirOffsets[d]
	n := offset(q, o[0], o[1], o[2])
	// Block.isFaceFull over the collision shape: full-cube collisions
	// (leaves among them) and full sturdy faces.
	if !isAir(rg.at(n)) || !(fullCollision(st) || sturdyFull(st, d)) {
		return
	}
	v, _ := world.BlockStateOf("vine", map[string]string{dirNameOf[oppositeDir(d)]: "true"})
	rg.set(n, v)
}

// maybeLeavesAbove is RuinedPortalPiece.maybeAddLeavesAbove.
func (p *ruinedPortalPiece) maybeLeavesAbove(rg *Region, r Random, q BlockPos) {
	if r.NextFloat() < 0.5 && isBlock(rg.at(q), "netherrack") && isAir(rg.at(offset(q, 0, 1, 0))) {
		l, _ := world.BlockStateOf("jungle_leaves", map[string]string{"persistent": "true"})
		rg.set(offset(q, 0, 1, 0), l)
	}
}
