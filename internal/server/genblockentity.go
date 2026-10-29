package server

import (
	"bytes"
	"strings"
	"sync"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// Block entities beyond the ones mc-go models with their own stores,
// and the block entities a generated chunk arrives with.
//
// The block entity type of every 26.2 block was measured from
// BlockEntityTypes' static initializer (javap): each type lists its
// valid blocks, and the registry id is the type's index in the
// minecraft:block_entity_type registry. A block entity mc-go has no
// store for (a spawner, an end gateway, a bee nest, a sculk catalyst)
// is kept as its saved fields: it round-trips through the chunk file,
// travels in the chunk packet with the type's own update tag, and dies
// when its block is replaced.
//
// Generation (worldgen's ProtoChunk) hands over each block entity with
// the fields features wrote into it; the rest is the type's fresh
// default, measured from chunks the real server generated and saved
// with ticking frozen:
//
//   - mob_spawner: Delay 20, MinSpawnDelay 200, MaxSpawnDelay 800,
//     SpawnCount 4, MaxNearbyEntities 6, RequiredPlayerRange 16,
//     SpawnRange 4 (all shorts), an empty SpawnPotentials list, and the
//     dungeon's SpawnData {entity:{id}} when the feature set one;
//   - sculk_sensor / calibrated_sculk_sensor: last_vibration_frequency 0
//     and an idle listener {event_delay 0, selector {tick -1L}};
//   - sculk_shrieker: warning_level 0 and the same idle listener;
//   - sculk_catalyst: an empty cursors list;
//   - potent_sulfur: countdown -1;
//   - beehive (bee nests and hives): the bees list;
//   - end_gateway: Age 0L, plus exit_portal / ExactTeleport when the
//     feature configured an exit;
//   - creaking_heart, brushable_block, chests: nothing beyond the frame
//     (a dungeon chest or a desert well's sand carries its LootTable and
//     LootTableSeed instead of items).
type genericBlockEntity struct {
	id     string         // the saved id ("minecraft:mob_spawner")
	fields map[string]any // the saved fields without the id/x/y/z frame
}

// blockEntityTypeNames maps a block to its block entity type (no
// namespace) for the blocks that are not one of the families
// blockEntityTypeName handles by suffix.
var blockEntityTypeNames = map[string]string{
	"furnace": "furnace", "chest": "chest", "trapped_chest": "trapped_chest",
	"ender_chest": "ender_chest", "jukebox": "jukebox", "dispenser": "dispenser",
	"dropper": "dropper", "spawner": "mob_spawner", "creaking_heart": "creaking_heart",
	"moving_piston": "piston", "brewing_stand": "brewing_stand",
	"enchanting_table": "enchanting_table", "end_portal": "end_portal", "beacon": "beacon",
	"skeleton_skull": "skull", "skeleton_wall_skull": "skull",
	"wither_skeleton_skull": "skull", "wither_skeleton_wall_skull": "skull",
	"zombie_head": "skull", "zombie_wall_head": "skull", "player_head": "skull",
	"player_wall_head": "skull", "creeper_head": "skull", "creeper_wall_head": "skull",
	"dragon_head": "skull", "dragon_wall_head": "skull", "piglin_head": "skull",
	"piglin_wall_head":  "skull",
	"daylight_detector": "daylight_detector", "hopper": "hopper", "comparator": "comparator",
	"structure_block": "structure_block", "end_gateway": "end_gateway",
	"command_block": "command_block", "chain_command_block": "command_block",
	"repeating_command_block": "command_block", "conduit": "conduit", "barrel": "barrel",
	"smoker": "smoker", "blast_furnace": "blast_furnace", "lectern": "lectern",
	"bell": "bell", "jigsaw": "jigsaw", "campfire": "campfire", "soul_campfire": "campfire",
	"bee_nest": "beehive", "beehive": "beehive", "sculk_sensor": "sculk_sensor",
	"calibrated_sculk_sensor": "calibrated_sculk_sensor", "sculk_catalyst": "sculk_catalyst",
	"sculk_shrieker": "sculk_shrieker", "chiseled_bookshelf": "chiseled_bookshelf",
	"suspicious_sand": "brushable_block", "suspicious_gravel": "brushable_block",
	"decorated_pot": "decorated_pot", "crafter": "crafter", "trial_spawner": "trial_spawner",
	"vault": "vault", "test_block": "test_block", "test_instance_block": "test_instance_block",
	"potent_sulfur": "potent_sulfur",
}

// blockEntityTypeName is the block entity type a block owns, if any:
// the table above plus the colour, wood and oxidation families.
func blockEntityTypeName(block string) (string, bool) {
	if t, ok := blockEntityTypeNames[block]; ok {
		return t, true
	}
	switch {
	case strings.HasSuffix(block, "_hanging_sign"):
		return "hanging_sign", true
	case strings.HasSuffix(block, "_sign"):
		return "sign", true
	case strings.HasSuffix(block, "_banner"):
		return "banner", true
	case strings.HasSuffix(block, "_shulker_box"), block == "shulker_box":
		return "shulker_box", true
	case strings.HasSuffix(block, "_shelf"):
		return "shelf", true
	case strings.HasSuffix(block, "copper_chest"):
		// Every copper chest is a minecraft:chest block entity.
		return "chest", true
	case strings.HasSuffix(block, "copper_golem_statue"):
		return "copper_golem_statue", true
	}
	return "", false
}

var blockEntityTypeIDs = sync.OnceValue(func() map[string]int32 {
	m := map[string]int32{}
	for i, n := range builtinRegistryNames(ProtocolVersion, "minecraft:block_entity_type") {
		m[n] = int32(i)
	}
	return m
})

// blockEntitySpecForType is the spec of a block entity type by its
// short name.
func blockEntitySpecForType(typ string) (blockEntitySpec, bool) {
	id, ok := blockEntityTypeIDs()["minecraft:"+typ]
	if !ok {
		return blockEntitySpec{}, false
	}
	return blockEntitySpec{id: id, name: "minecraft:" + typ}, true
}

// typedBlockEntityIDs are the saved ids mc-go restores into a store of
// its own; every other id is kept as a genericBlockEntity.
var typedBlockEntityIDs = map[string]bool{
	"minecraft:jukebox": true,
	"minecraft:chest":   true, "minecraft:trapped_chest": true, "minecraft:sign": true,
	"minecraft:hanging_sign": true, "minecraft:hopper": true, "minecraft:furnace": true,
	"minecraft:blast_furnace": true, "minecraft:smoker": true, "minecraft:lectern": true,
	"minecraft:comparator": true, "minecraft:sculk_sensor": true,
	"minecraft:calibrated_sculk_sensor": true, "minecraft:barrel": true,
	"minecraft:dispenser": true, "minecraft:dropper": true, "minecraft:shulker_box": true,
	"minecraft:brewing_stand": true, "minecraft:crafter": true,
}

// idleListenerNBT is VibrationSystem.Data with nothing selected or
// travelling, as a fresh sensor or shrieker saves it.
func idleListenerNBT() map[string]any {
	return map[string]any{"event_delay": int32(0), "selector": map[string]any{"tick": int64(-1)}}
}

// genBlockEntityDefaults is a fresh block entity's saved fields, for the
// types generation places (see the comment on genericBlockEntity).
func genBlockEntityDefaults(typ string) map[string]any {
	switch typ {
	case "mob_spawner":
		return map[string]any{
			"Delay": int16(20), "MinSpawnDelay": int16(200), "MaxSpawnDelay": int16(800),
			"SpawnCount": int16(4), "MaxNearbyEntities": int16(6), "RequiredPlayerRange": int16(16),
			"SpawnRange": int16(4), "SpawnPotentials": nbt.List{Elem: nbt.TagEnd},
		}
	case "sculk_sensor", "calibrated_sculk_sensor":
		return map[string]any{"last_vibration_frequency": int32(0), "listener": idleListenerNBT()}
	case "sculk_shrieker":
		return map[string]any{"warning_level": int32(0), "listener": idleListenerNBT()}
	case "sculk_catalyst":
		return map[string]any{"cursors": nbt.List{Elem: nbt.TagEnd}}
	case "potent_sulfur":
		return map[string]any{"countdown": int32(-1)}
	case "beehive":
		return map[string]any{"bees": nbt.List{Elem: nbt.TagEnd}}
	case "end_gateway":
		return map[string]any{"Age": int64(0)}
	case "chest", "trapped_chest", "barrel", "dispenser", "dropper", "hopper", "shulker_box", "crafter":
		return map[string]any{"Items": nbt.List{Elem: nbt.TagEnd}}
	}
	return map[string]any{}
}

// installGenBlockEntities puts a generated chunk's block entities into
// the level, once: the chunk's own list, each at its type's defaults
// overlaid with what generation wrote, restored the way a saved chunk's
// block entities are. A position whose block changed since generation
// (to one with another block entity type, or none) is skipped. It runs
// on the simulation goroutine when the chunk is first streamed, saved
// or post-processed, whichever comes first.
func (s *Server) installGenBlockEntities(d *dimension, cx, cz int) {
	if d.terrain == nil {
		return
	}
	k := [2]int{cx, cz}
	if d.genBEDone[k] {
		return
	}
	c := d.w.PeekBaseChunk(cx, cz)
	if c == nil {
		return
	}
	if d.genBEDone == nil {
		d.genBEDone = map[[2]int]bool{}
	}
	d.genBEDone[k] = true
	for _, be := range c.BlockEntities {
		gen, ok := blockEntityOf(c.At(be.Pos.X&15, be.Pos.Y, be.Pos.Z&15))
		if !ok {
			continue
		}
		if cur, ok := blockEntityOf(d.w.GetBlock(be.Pos)); !ok || cur.name != gen.name {
			continue
		}
		typ := strings.TrimPrefix(gen.name, "minecraft:")
		tag := blockEntityNBT(be.Pos, gen, func(m map[string]any) {
			for k, v := range genBlockEntityDefaults(typ) {
				m[k] = v
			}
			for k, v := range be.Data {
				m[k] = cloneNBTValue(v)
			}
		})
		s.restoreBlockEntity(d, tag)
	}
}

// restoreGenericBlockEntity keeps a saved block entity mc-go has no
// store for as its fields. A hive's occupants also set the count the
// bee code reads.
func (s *Server) restoreGenericBlockEntity(d *dimension, pos world.Pos, id string, be map[string]any) {
	spec, ok := blockEntityOf(d.w.GetBlock(pos))
	if !ok || spec.name != id {
		return
	}
	fields := map[string]any{}
	for k, v := range be {
		switch k {
		case "id", "x", "y", "z", "keepPacked":
			continue
		case "components":
			if m, ok := v.(map[string]any); ok && len(m) == 0 {
				continue
			}
		}
		fields[k] = cloneNBTValue(v)
	}
	if d.genericBE == nil {
		d.genericBE = map[world.Pos]*genericBlockEntity{}
	}
	if id == "minecraft:command_block" {
		fields = commandBlockSavedFields(fields)
		if current := d.genericBE[pos]; current != nil && current.id == id {
			current.fields = fields
			return
		}
	}
	if id == "minecraft:structure_block" {
		fields = structureBlockSavedFields(fields)
		s.structureBlockMode(d, pos, fields["mode"].(string))
	}
	if id == jigsawEntityID {
		fields = jigsawSavedFields(d.w.GetBlock(pos), fields)
	}
	if id == testBlockEntityID {
		fields = testBlockSavedFields(fields)
	}
	d.genericBE[pos] = &genericBlockEntity{id: id, fields: fields}
	if id == "minecraft:beehive" {
		if l, ok := fields["bees"].(nbt.List); ok && len(l.Items) > 0 {
			if d.bees == nil {
				d.bees = map[world.Pos]int32{}
			}
			d.bees[pos] = int32(len(l.Items))
		} else {
			delete(d.bees, pos)
		}
	}
}

// genericBlockEntityAt is the generic block entity at pos while its
// block still owns that type.
func genericBlockEntityAt(d *dimension, pos world.Pos) (*genericBlockEntity, blockEntitySpec, bool) {
	g := d.genericBE[pos]
	if g == nil {
		return nil, blockEntitySpec{}, false
	}
	spec, ok := blockEntityOf(d.w.GetBlock(pos))
	if !ok || spec.name != g.id {
		return nil, blockEntitySpec{}, false
	}
	return g, spec, true
}

// genericSaveFields is the generic block entity's saved fields as of
// now: a hive's occupant list follows the count the bee code keeps
// (bees that left drop off the end; bees that entered join with the
// default occupant record).
func genericSaveFields(d *dimension, pos world.Pos, g *genericBlockEntity) map[string]any {
	if g.id == jigsawEntityID {
		return jigsawSavedFields(d.w.GetBlock(pos), g.fields)
	}
	if g.id == testBlockEntityID {
		return testBlockSavedFields(g.fields)
	}
	if g.id == "minecraft:command_block" {
		return commandBlockSavedFields(g.fields)
	}
	out := make(map[string]any, len(g.fields))
	for k, v := range g.fields {
		out[k] = cloneNBTValue(v)
	}
	if g.id == "minecraft:beehive" {
		l, _ := out["bees"].(nbt.List)
		n := int(d.bees[pos])
		if len(l.Items) > n {
			l.Items = l.Items[:n]
		}
		for len(l.Items) < n {
			l.Elem = nbt.TagCompound
			l.Items = append(l.Items, map[string]any{
				"entity_data":       map[string]any{"id": "minecraft:bee"},
				"ticks_in_hive":     int32(0),
				"min_ticks_in_hive": int32(600),
			})
		}
		if len(l.Items) == 0 {
			l = nbt.List{Elem: nbt.TagEnd}
		}
		out["bees"] = l
	}
	return out
}

// genericSaveNBT is a generic block entity's saved compound.
func genericSaveNBT(d *dimension, pos world.Pos, g *genericBlockEntity, spec blockEntitySpec) map[string]any {
	fields := genericSaveFields(d, pos, g)
	return blockEntityNBT(pos, spec, func(m map[string]any) {
		for k, v := range fields {
			m[k] = v
		}
	})
}

// blockEntityUpdateTag is BlockEntity.getUpdateTag for the types that
// override it, from the block entity's saved fields (javap, 26.2):
// most types send nothing; saveCustomOnly types send their saved
// fields; a spawner leaves out SpawnPotentials; a banner adds its
// components (saveWithoutMetadata); a campfire sends its Items, a shelf
// its Items and alignment, a brushable block its item, a vault its
// shared_data. An empty tag is sent as none at all. The trial spawner's
// state-dependent tag is not modelled (it sends none here).
func blockEntityUpdateTag(typ string, f map[string]any) []byte {
	var tag map[string]any
	pick := func(keys ...string) {
		tag = map[string]any{}
		for _, k := range keys {
			if v, ok := f[k]; ok {
				tag[k] = v
			}
		}
	}
	all := func() {
		tag = map[string]any{}
		for k, v := range f {
			if k != "components" {
				tag[k] = v
			}
		}
	}
	switch typ {
	case "mob_spawner":
		all()
		delete(tag, "SpawnPotentials")
	case "end_gateway", "creaking_heart", "beacon", "conduit", "decorated_pot", "jigsaw",
		"skull", "structure_block", "test_block", "test_instance_block":
		all()
	case "banner":
		all()
		if c, ok := f["components"]; ok {
			tag["components"] = c
		} else {
			tag["components"] = map[string]any{}
		}
	case "campfire":
		pick("Items")
		if _, ok := tag["Items"]; !ok {
			tag["Items"] = nbt.List{Elem: nbt.TagEnd}
		}
	case "shelf":
		pick("Items", "align_items_to_bottom")
	case "brushable_block":
		pick("item")
	case "vault":
		pick("shared_data")
	}
	if len(tag) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := nbt.WriteUnnamed(&buf, tag); err != nil {
		return nil
	}
	return buf.Bytes()
}

// genericBEOnWrite is LevelChunk.setBlockState's block entity rule for
// the generic store: the block entity survives a state change of the
// same block and dies with any other write.
func (s *Server) genericBEOnWrite(d *dimension, pos world.Pos, blk, old world.Block) {
	g := d.genericBE[pos]
	if g == nil || blk == old {
		return
	}
	nn, _ := world.StateOwner(blk)
	on, _ := world.StateOwner(old)
	if spec, ok := blockEntityOf(blk); !ok || spec.name != g.id || nn != on {
		delete(d.genericBE, pos)
	}
}

// DimensionWorld returns a dimension's world by id
// ("minecraft:the_end"), nil for an unknown one. Generating a chunk
// through it (BaseChunk) is safe from any goroutine.
func (s *Server) DimensionWorld(id string) *world.World {
	if d := s.dims[id]; d != nil {
		return d.w
	}
	return nil
}

// cloneNBTValue deep-copies a decoded NBT value.
func cloneNBTValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[k] = cloneNBTValue(x)
		}
		return m
	case nbt.List:
		l := nbt.List{Elem: t.Elem, Items: make([]any, len(t.Items))}
		for i, x := range t.Items {
			l.Items[i] = cloneNBTValue(x)
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
