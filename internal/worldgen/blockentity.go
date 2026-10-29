package worldgen

import (
	"sort"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// Generation-time block entities.
//
// Measured on 26.2 (WorldGenRegion.setBlock / getBlockEntity, ProtoChunk
// and LevelChunk's promotion of pending tags):
//
//   - every write of a state that has a block entity leaves a pending
//     "DUMMY" tag at the position (a write of a state without one drops
//     whatever the position held, pending tag and block entity alike);
//   - a feature that configures the block entity (a dungeon chest's loot
//     table, a nest's bees, a gateway's exit) reads it back through
//     getBlockEntity, which turns a DUMMY tag into the type's fresh block
//     entity and keeps that object from then on;
//   - when the chunk becomes a level chunk its block entity objects
//     move over, and LevelChunk.postProcessGeneration promotes the
//     pending tags through getBlockEntity, which prefers an existing
//     object: a DUMMY write over a configured block entity (a template's
//     shape update rewriting a chest's state) keeps the configuration;
//   - a position whose state no longer has a block entity is skipped.
//
// The generator records exactly that: positions with a pending DUMMY,
// and positions holding a configured block entity with the fields
// generation wrote into it (in the block entity's own saved field
// names). The type id and the type's defaults are the server's to fill
// in: a GenBlockEntity with nil Data is a fresh block entity.

// GenBlockEntity is one block entity of a generated chunk.
type GenBlockEntity struct {
	Pos BlockPos
	// Data holds the fields generation wrote, in save form (no id/x/y/z
	// frame); nil for a block entity left at its type's defaults.
	Data map[string]any
}

// noteBlockWrite is the block entity half of WorldGenRegion.setBlock
// for a write at pos that replaced old with nb.
func (p *ProtoChunk) noteBlockWrite(pos BlockPos, old, nb world.Block) {
	if hasBlockEntity(nb) {
		if p.bePending == nil {
			p.bePending = map[BlockPos]bool{}
		}
		p.bePending[pos] = true
		return
	}
	if hasBlockEntity(old) {
		delete(p.bePending, pos)
		delete(p.beReal, pos)
		delete(p.beOwner, pos)
	}
}

// blockEntity is WorldGenRegion.getBlockEntity: the configured block
// entity at pos, a pending DUMMY tag promoted to a fresh one (only while
// the state still has a block entity), or nil. The returned map is the
// block entity's field set; writing into it configures the block entity.
func (rg *Region) blockEntity(pos BlockPos) map[string]any {
	c := rg.chunkAt(pos[0], pos[2])
	if c == nil {
		return nil
	}
	if d, ok := c.beReal[pos]; ok {
		return d
	}
	if !c.bePending[pos] || !hasBlockEntity(c.Get(pos[0]&15, pos[1], pos[2]&15)) {
		return nil
	}
	// ChunkAccess.setBlockEntity drops the pending tag it replaces.
	delete(c.bePending, pos)
	if c.beReal == nil {
		c.beReal = map[BlockPos]map[string]any{}
	}
	d := map[string]any{}
	c.beReal[pos] = d
	if c.beOwner == nil {
		c.beOwner = map[BlockPos]string{}
	}
	c.beOwner[pos], _ = world.StateOwner(c.Get(pos[0]&15, pos[1], pos[2]&15))
	return d
}

// BlockEntities lists the chunk's block entities as the finished chunk
// holds them, in position order (x, then y, then z).
func (p *ProtoChunk) BlockEntities() []GenBlockEntity {
	var out []GenBlockEntity
	add := func(pos BlockPos, d map[string]any) {
		if !hasBlockEntity(p.Get(pos[0]&15, pos[1], pos[2]&15)) {
			return
		}
		out = append(out, GenBlockEntity{Pos: pos, Data: d})
	}
	// LevelChunk.setBlockEntity refuses an object whose type no longer
	// fits the state (a configured chest later overwritten by another
	// block entity block); the pending tag then promotes a fresh one.
	valid := func(pos BlockPos) bool {
		if _, ok := p.beReal[pos]; !ok {
			return false
		}
		n, _ := world.StateOwner(p.Get(pos[0]&15, pos[1], pos[2]&15))
		return n == p.beOwner[pos]
	}
	for pos, d := range p.beReal {
		if valid(pos) {
			add(pos, d)
		}
	}
	for pos := range p.bePending {
		if !valid(pos) {
			add(pos, nil)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Pos, out[j].Pos
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		if a[1] != b[1] {
			return a[1] < b[1]
		}
		return a[2] < b[2]
	})
	return out
}

// HasBlockEntity reports whether a state has a block entity
// (BlockState.hasBlockEntity).
func HasBlockEntity(b world.Block) bool { return hasBlockEntity(b) }

// setLootTable is RandomizableContainer.setLootTable (and
// BrushableBlockEntity.setLootTable): the table id and its seed, which
// the saved form leaves out when it is 0 (trySaveLootTable).
func setLootTable(be map[string]any, table string, seed int64) {
	be["LootTable"] = table
	if seed != 0 {
		be["LootTableSeed"] = seed
	} else {
		delete(be, "LootTableSeed")
	}
}

// setBlockEntityLootTable is RandomizableContainer.setBlockEntityLootTable:
// a container block entity at pos takes the table and a seed drawn from
// random; anything else draws nothing.
func setBlockEntityLootTable(rg *Region, r Random, pos BlockPos, table string) {
	be := rg.blockEntity(pos)
	if be == nil || !isRandomizable(rg.at(pos)) {
		return
	}
	setLootTable(be, table, r.NextLong())
}

// blockPosAsLong is BlockPos.asLong: 26 bits of x, 12 of y, 26 of z
// packed x<<38 | z<<12 | y.
func blockPosAsLong(p BlockPos) int64 {
	return (int64(p[0])&0x3FFFFFF)<<38 | (int64(p[2])&0x3FFFFFF)<<12 | int64(p[1])&0xFFF
}

// beeOccupant is BeehiveBlockEntity.Occupant.create(ticksInHive) in its
// saved form: a bee with no entity data, min_ticks_in_hive 600.
func beeOccupant(ticks int32) map[string]any {
	return map[string]any{
		"entity_data":       map[string]any{"id": "minecraft:bee"},
		"ticks_in_hive":     ticks,
		"min_ticks_in_hive": int32(600),
	}
}

// storeBee is BeehiveBlockEntity.storeBee: the occupant joins the
// stored list.
func storeBee(be map[string]any, ticks int32) {
	l, _ := be["bees"].(nbt.List)
	l.Elem = nbt.TagCompound
	l.Items = append(l.Items, beeOccupant(ticks))
	be["bees"] = l
}

// loadTemplateNBT is BlockEntity.loadWithComponents with a template
// block's tag: the tag's fields replace the block entity's (the frame
// it carries - id and position - is the block entity's own).
func loadTemplateNBT(be map[string]any, tag map[string]any) {
	for k, v := range tag {
		switch k {
		case "id", "x", "y", "z":
			continue
		}
		be[k] = cloneNBT(v)
	}
}

// cloneNBT deep-copies an NBT value so a template's cached tag is never
// shared with a chunk's block entity.
func cloneNBT(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[k] = cloneNBT(x)
		}
		return m
	case nbt.List:
		l := nbt.List{Elem: t.Elem, Items: make([]any, len(t.Items))}
		for i, x := range t.Items {
			l.Items[i] = cloneNBT(x)
		}
		return l
	case []int32:
		return append([]int32(nil), t...)
	case []int64:
		return append([]int64(nil), t...)
	case []byte:
		return append([]byte(nil), t...)
	}
	return v
}
