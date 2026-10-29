package worldgen

// End cities (EndCityStructure, EndCityPieces): a random rotation,
// the lowest surface corner of a 5x5 box 7 blocks into the chunk
// (no start below y 60), then the house tower and the recursive
// tower / bridge / fat tower / house sections, each section's pieces
// attached to the previous piece's template position by the
// rotated connection offset. A section is kept only when none of its
// pieces hits a piece of a different generation depth in the list it
// joins; kept sections take one random generation depth. At most one
// ship per city. Template pieces ("ecp") save their short template
// name; "Chest" data markers give the chest below the treasure table
// (shulker sentries and elytra item frames are entities, not placed).

type ecBridge struct {
	rot int
	off BlockPos
}

var (
	ecTowerBridges = []ecBridge{{rotNone, BlockPos{1, -1, 0}}, {rotCW90, BlockPos{6, -1, 1}}, {rotCCW90, BlockPos{0, -1, 5}}, {rotCW180, BlockPos{5, -1, 6}}}
	ecFatBridges   = []ecBridge{{rotNone, BlockPos{4, -1, 0}}, {rotCW90, BlockPos{12, -1, 4}}, {rotCCW90, BlockPos{0, -1, 8}}, {rotCW180, BlockPos{8, -1, 12}}}
)

// Section generators (EndCityPieces.SectionGenerator implementations).
const (
	ecHouseTower = iota
	ecTower
	ecTowerBridge
	ecFatTower
)

func init() {
	structureTypes["end_city"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		return func(c *GenContext) (*GenerationStub, bool) {
			r := c.Random
			rot := int(r.NextIntN(4))
			// Structure.getLowestYIn5by5BoxOffset7Blocks.
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
			pos := BlockPos{x, lowestCornerY(c, x, z, dx, dz), z}
			if pos[1] < 60 {
				return nil, false
			}
			return &GenerationStub{Pos: pos, Generate: func(add func(StructurePiece)) {
				for _, p := range endCityPieces(pos, rot, r) {
					add(p)
				}
			}}, true
		}
	}
	pieceLoaders["ecp"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		b, err := loadVPBase(t)
		if err != nil {
			return nil, err
		}
		name := fmt3str(t["Template"])
		rot := rotationFromName(fmt3str(t["Rot"]))
		ow := tagInt(t, "OW") != 0
		p := newEndCityPiece(name, BlockPos{tagInt(t, "TPX"), tagInt(t, "TPY"), tagInt(t, "TPZ")}, rot, ow)
		p.depth = b.depth
		return p, nil
	}
}

func endCitySettings(rot int, overwrite bool) placeSettings {
	s := *newPlaceSettings()
	s.ignoreEntities = true
	if overwrite {
		s.processors = []tplProcessor{ignoreBlocksProc(map[string]bool{"structure_block": true})}
	} else {
		s.processors = []tplProcessor{ignoreBlocksProc(map[string]bool{"structure_block": true, "air": true})}
	}
	s.rotation = rot
	return s
}

type endCityPiece struct {
	tplPiece
	rot       int
	overwrite bool
}

func newEndCityPiece(name string, pos BlockPos, rot int, overwrite bool) *endCityPiece {
	p := &endCityPiece{rot: rot, overwrite: overwrite}
	p.tplPiece = tplPiece{vpBase: vpBase{typ: "ecp"}, name: name, tpl: loadTemplate("end_city/" + name), pos: pos, set: endCitySettings(rot, overwrite)}
	p.bb = bbFrom(p.tpl.boundingBox(&p.set, pos))
	p.setOrientation(dirNorth)
	return p
}

// SaveData is EndCityPiece.addAdditionalSaveData.
func (p *endCityPiece) SaveData(t map[string]any) {
	p.saveTpl(t)
	t["Rot"] = rotationEnumNames[p.rot]
	t["OW"] = boolByte(p.overwrite)
}

// PostProcess is TemplateStructurePiece.postProcess with
// EndCityPiece.handleDataMarker.
func (p *endCityPiece) PostProcess(c *PieceContext) {
	p.dataMarker = func(c *PieceContext, meta string, pos BlockPos) {
		if len(meta) >= 5 && meta[:5] == "Chest" {
			below := offset(pos, 0, -1, 0)
			if c.ChunkBox.IsInside(below) {
				setContainerLoot(c, c.Random, below, "minecraft:chests/end_city_treasure")
			}
		}
	}
	p.placeTemplate(c)
}

// endCityGen is the generation state (the static shipCreated flag).
type endCityGen struct {
	r           Random
	shipCreated bool
}

// addPiece is EndCityPieces.addPiece: the new piece starts at the
// previous template position and moves by the offset transformed by
// the previous piece's rotation.
func (e *endCityGen) addPiece(prev *endCityPiece, off BlockPos, name string, rot int, overwrite bool) *endCityPiece {
	p := newEndCityPiece(name, prev.pos, rot, overwrite)
	d := tplTransform(off, mirNone, prev.rot, BlockPos{})
	p.Move(d[0], d[1], d[2])
	return p
}

// endCityPieces is EndCityPieces.startHouseTower.
func endCityPieces(pos BlockPos, rot int, r Random) []*endCityPiece {
	e := &endCityGen{r: r}
	var list []*endCityPiece
	base := newEndCityPiece("base_floor", pos, rot, true)
	list = append(list, base)
	p := e.addPiece(base, BlockPos{-1, 0, -1}, "second_floor_1", rot, false)
	list = append(list, p)
	p = e.addPiece(p, BlockPos{-1, 4, -1}, "third_floor_1", rot, false)
	list = append(list, p)
	p = e.addPiece(p, BlockPos{-1, 8, -1}, "third_roof", rot, true)
	list = append(list, p)
	e.recursiveChildren(ecTower, 1, p, BlockPos{}, &list)
	return list
}

// recursiveChildren is EndCityPieces.recursiveChildren: the section is
// generated into a list of its own and joins list unless one of its
// pieces collides with a piece of list at another depth than parent.
func (e *endCityGen) recursiveChildren(gen, depth int, parent *endCityPiece, pos BlockPos, list *[]*endCityPiece) bool {
	if depth > 8 {
		return false
	}
	var kids []*endCityPiece
	if !e.generate(gen, depth, parent, pos, &kids) {
		return false
	}
	d := e.r.NextInt()
	for _, k := range kids {
		k.depth = d
		for _, o := range *list {
			if o.bb.Intersects(k.bb) {
				if o.depth != parent.depth {
					return false
				}
				break
			}
		}
	}
	*list = append(*list, kids...)
	return true
}

// generate is the section generators' generate.
func (e *endCityGen) generate(gen, depth int, parent *endCityPiece, pos BlockPos, list *[]*endCityPiece) bool {
	r := e.r
	rot := parent.rot
	add := func(prev *endCityPiece, off BlockPos, name string, rot int, ow bool) *endCityPiece {
		p := e.addPiece(prev, off, name, rot, ow)
		*list = append(*list, p)
		return p
	}
	switch gen {
	case ecHouseTower:
		if depth > 8 {
			return false
		}
		p := add(parent, pos, "base_floor", rot, true)
		switch r.NextIntN(3) {
		case 0:
			add(p, BlockPos{-1, 4, -1}, "base_roof", rot, true)
		case 1:
			p = add(p, BlockPos{-1, 0, -1}, "second_floor_2", rot, false)
			p = add(p, BlockPos{-1, 8, -1}, "second_roof", rot, false)
			e.recursiveChildren(ecTower, depth+1, p, BlockPos{}, list)
		case 2:
			p = add(p, BlockPos{-1, 0, -1}, "second_floor_2", rot, false)
			p = add(p, BlockPos{-1, 4, -1}, "third_floor_2", rot, false)
			p = add(p, BlockPos{-1, 8, -1}, "third_roof", rot, true)
			e.recursiveChildren(ecTower, depth+1, p, BlockPos{}, list)
		}
		return true
	case ecTower:
		bx := 3 + r.NextIntN(2)
		bz := 3 + r.NextIntN(2)
		p := add(parent, BlockPos{bx, -3, bz}, "tower_base", rot, true)
		p = add(p, BlockPos{0, 7, 0}, "tower_piece", rot, true)
		var from *endCityPiece
		if r.NextIntN(3) == 0 {
			from = p
		}
		n := 1 + r.NextIntN(3)
		for i := int32(0); i < n; i++ {
			p = add(p, BlockPos{0, 4, 0}, "tower_piece", rot, true)
			if i < n-1 && r.NextBool() {
				from = p
			}
		}
		if from != nil {
			for _, b := range ecTowerBridges {
				if r.NextBool() {
					end := add(from, b.off, "bridge_end", (rot+b.rot)&3, true)
					e.recursiveChildren(ecTowerBridge, depth+1, end, BlockPos{}, list)
				}
			}
			add(p, BlockPos{-1, 4, -1}, "tower_top", rot, true)
			return true
		}
		if depth == 7 {
			add(p, BlockPos{-1, 4, -1}, "tower_top", rot, true)
			return true
		}
		return e.recursiveChildren(ecFatTower, depth+1, p, BlockPos{}, list)
	case ecTowerBridge:
		n := r.NextIntN(4) + 1
		p := add(parent, BlockPos{0, 0, -4}, "bridge_piece", rot, true)
		p.depth = -1
		up := int32(0)
		for i := int32(0); i < n; i++ {
			if r.NextBool() {
				p = add(p, BlockPos{0, up, -4}, "bridge_piece", rot, true)
				up = 0
				continue
			}
			if r.NextBool() {
				p = add(p, BlockPos{0, up, -4}, "bridge_steep_stairs", rot, true)
			} else {
				p = add(p, BlockPos{0, up, -8}, "bridge_gentle_stairs", rot, true)
			}
			up = 4
		}
		if e.shipCreated || r.NextIntN(int32(10-depth)) != 0 {
			if !e.recursiveChildren(ecHouseTower, depth+1, p, BlockPos{-3, up + 1, -11}, list) {
				return false
			}
		} else {
			sx := -8 + r.NextIntN(8)
			sz := -70 + r.NextIntN(10)
			add(p, BlockPos{sx, up, sz}, "ship", rot, true)
			e.shipCreated = true
		}
		end := add(p, BlockPos{4, up, 0}, "bridge_end", (rot+rotCW180)&3, true)
		end.depth = -1
		return true
	case ecFatTower:
		p := add(parent, BlockPos{-3, 4, -3}, "fat_tower_base", rot, true)
		p = add(p, BlockPos{0, 4, 0}, "fat_tower_middle", rot, true)
		for i := 0; i < 2 && r.NextIntN(3) != 0; i++ {
			p = add(p, BlockPos{0, 8, 0}, "fat_tower_middle", rot, true)
			for _, b := range ecFatBridges {
				if r.NextBool() {
					end := add(p, b.off, "bridge_end", (rot+b.rot)&3, true)
					e.recursiveChildren(ecTowerBridge, depth+1, end, BlockPos{}, list)
				}
			}
		}
		add(p, BlockPos{-2, 8, -2}, "fat_tower_top", rot, true)
		return true
	}
	return false
}
