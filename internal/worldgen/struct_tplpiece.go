package worldgen

import (
	"fmt"

	"mc-go/internal/world"
)

// tplPiece is TemplateStructurePiece: a template placed at
// templatePosition with fixed place settings, orientation north. Its
// postProcess clips the settings to the chunk box, recomputes the
// piece box from the template, places it, then hands every DATA
// structure block of the palette (unclipped) to the subclass's data
// marker handler and turns jigsaw blocks into their final state.
type tplPiece struct {
	vpBase
	name string // template id as saved ("minecraft:shipwreck/with_mast")
	tpl  *structTemplate
	pos  BlockPos
	set  placeSettings
	// dataMarker is handleDataMarker (nil: none).
	dataMarker func(c *PieceContext, meta string, pos BlockPos)
}

func newTplPiece(typ, name string, set placeSettings, pos BlockPos, depth int32) tplPiece {
	t := loadTemplate(name)
	p := tplPiece{vpBase: vpBase{typ: typ, depth: depth}, name: name, tpl: t, pos: pos, set: set}
	p.bb = bbFrom(t.boundingBox(&p.set, pos))
	p.setOrientation(dirNorth)
	return p
}

// Move is TemplateStructurePiece.move (box and template position).
func (p *tplPiece) Move(dx, dy, dz int32) {
	p.bb = p.bb.Moved(dx, dy, dz)
	p.pos = offset(p.pos, dx, dy, dz)
}

// saveTpl is TemplateStructurePiece.addAdditionalSaveData.
func (p *tplPiece) saveTpl(t map[string]any) {
	t["TPX"], t["TPY"], t["TPZ"] = p.pos[0], p.pos[1], p.pos[2]
	t["Template"] = p.name
}

// loadTplPiece is the TemplateStructurePiece load constructor: the
// common fields, the template position and name, the settings rebuilt
// by the subclass, and the box recomputed from the template.
func loadTplPiece(t map[string]any, settings func(name string) placeSettings) (tplPiece, error) {
	b, err := loadVPBase(t)
	if err != nil {
		return tplPiece{}, err
	}
	name, _ := t["Template"].(string)
	if name == "" {
		return tplPiece{}, fmt.Errorf("worldgen: template piece %s without Template", b.typ)
	}
	p := tplPiece{vpBase: b, name: name, tpl: loadTemplate(name), pos: BlockPos{tagInt(t, "TPX"), tagInt(t, "TPY"), tagInt(t, "TPZ")}}
	p.setOrientation(dirNorth)
	p.set = settings(name)
	p.bb = bbFrom(p.tpl.boundingBox(&p.set, p.pos))
	return p, nil
}

// placeTemplate is TemplateStructurePiece.postProcess.
func (p *tplPiece) placeTemplate(c *PieceContext) {
	s := p.set
	cb := c.ChunkBox.internal()
	s.bb = &cb
	p.bb = bbFrom(p.tpl.boundingBox(&s, p.pos))
	if !p.tpl.placeInWorld(c.Region, p.pos, c.Pivot, &s, c.Random) {
		return
	}
	for _, b := range tplFilter(p.tpl, p.pos, &s, "structure_block") {
		if b.nbt == nil {
			continue
		}
		if m, _ := b.nbt["mode"].(string); m != "DATA" {
			continue
		}
		meta, _ := b.nbt["metadata"].(string)
		if p.dataMarker != nil {
			p.dataMarker(c, meta, b.pos)
		}
	}
	for _, b := range tplFilter(p.tpl, p.pos, &s, "jigsaw") {
		if b.nbt == nil {
			continue
		}
		fs, _ := b.nbt["final_state"].(string)
		if fs == "" {
			fs = "minecraft:air"
		}
		st, ok := parseBlockState(fs)
		if !ok {
			st = world.Air
		}
		c.Region.set(b.pos, st)
	}
}

// tplFilter is StructureTemplate.filterBlocks(pos, settings, block):
// the settings' palette's blocks of one kind, transformed to world
// positions (no processors, no box).
func tplFilter(t *structTemplate, pos BlockPos, s *placeSettings, block string) []tplBlock {
	if len(t.palettes) == 0 {
		return nil
	}
	pal := t.palettes[s.randomAt(pos).NextIntN(int32(len(t.palettes)))]
	var out []tplBlock
	for _, b := range pal {
		if !isBlock(b.state, block) {
			continue
		}
		wp := tplTransform(b.pos, s.mirror, s.rotation, s.pivot)
		out = append(out, tplBlock{pos: offset(wp, pos[0], pos[1], pos[2]), state: b.state, nbt: b.nbt})
	}
	return out
}

// setContainerLoot is RandomizableContainer.setBlockEntityLootTable:
// an existing container block entity takes the table and a seed drawn
// from r.
func setContainerLoot(c *PieceContext, r Random, pos BlockPos, table string) {
	// WorldGenRegion.getBlockEntity creates the entity of any
	// block-entity block placed so far (its DUMMY placeholder).
	be := c.Region.blockEntity(pos)
	if be == nil || !isRandomizable(c.Region.at(pos)) {
		return
	}
	setLootTable(be, table, r.NextLong())
}

func rotationFromName(s string) int {
	for i, n := range rotationEnumNames {
		if n == s {
			return i
		}
	}
	return rotNone
}

// StructureRandomLongs returns the longs the random a structure's
// pieces draw from while chunk (cx, cz) is decorated (the decoration
// seed, feature-seeded by the structure's index in its step) yields
// starting at each of its first n steps. Tests use it to check a loot seed came from some chunk's
// pass when the decoration order decides which one a container keeps.
func (g *Generator) StructureRandomLongs(cx, cz int32, name string, n int) []int64 {
	st := g.structureState()
	d := g.structure(name)
	idx := int32(0)
	for _, o := range st.structOrder {
		if o == name {
			break
		}
		if g.structure(o).step == d.step {
			idx++
		}
	}
	r := newWGRandom(0)
	deco := setDecorationSeed(r, g.RS.Seed, cx*16, cz*16)
	setFeatureSeed(r, deco, idx, int32(d.step))
	// A long drawn after any mix of int, float and long draws starts
	// at some step of the LCG: one candidate per step.
	ints := make([]int32, n+1)
	for i := range ints {
		ints[i] = r.NextInt()
	}
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(ints[i])<<32 + int64(ints[i+1])
	}
	return out
}

// structLootBlockEntity gives the block entity at pos a loot table and
// seed (RandomizableContainer.setLootTable on a freshly placed
// container). All structure-piece block entity writes go through this
// and setContainerLoot.
func structLootBlockEntity(rg *Region, pos BlockPos, table string, seed int64) {
	if be := rg.blockEntity(pos); be != nil {
		setLootTable(be, table, seed)
	}
}

// structSpawnerBlockEntity configures a spawner placed by a piece
// (SpawnerBlockEntity.setEntityId with an empty potentials list, which
// draws nothing).
func structSpawnerBlockEntity(rg *Region, pos BlockPos, entity string) {
	if be := rg.blockEntity(pos); be != nil {
		be["SpawnData"] = map[string]any{"entity": map[string]any{"id": entity}}
	}
}
