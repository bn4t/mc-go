package server

import (
	"sort"
	"strconv"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// Block entity persistence, in the real server's own format.
//
// A chunk's block entities travel in the chunk itself, in its
// block_entities list, and that list is the only place a chest's
// contents or a sign's text is kept: the blocks say where the chest is
// and nothing about what is inside it. Every field name here was read
// off a chunk the real 26.2 server saved, and the shapes that look
// arbitrary are not:
//
//   - each entry repeats its own x/y/z alongside the chunk that holds
//     it, and carries an `id` naming the block entity type, which for a
//     sign is "minecraft:sign" however many wood types and wall
//     variants the block has;
//   - every one of them carries an empty `components` compound: the
//     block entity's own data components, which 26.2 writes even when
//     there are none, for a sign as much as for a chest;
//   - a stack is `Slot` (a byte), `count` (an int) and `id`, plus a
//     `components` compound that is written only when the stack
//     actually carries one - an empty one on the stack is not the same
//     file, and the real server drops it on the next save;
//   - a shulker box's stored items are a component, not slots of the
//     block entity: the box travels as one item carrying
//     minecraft:container, and the nested stacks carry no Slot and
//     leave count out when it is 1.
type blockEntitySpec struct {
	id   int32  // minecraft:block_entity_type registry id
	name string // the id the saved compound carries
}

// blockEntitySpecs maps a block name to the block entity the real
// server keeps for it. Blocks missing from the table have no block
// entity at all, however much state their menu shows (an anvil's
// rename is not saved, and neither is a stonecutter's input).
var blockEntitySpecs = map[string]blockEntitySpec{
	"furnace":                 {0, "minecraft:furnace"},
	"chest":                   {1, "minecraft:chest"},
	"trapped_chest":           {2, "minecraft:trapped_chest"},
	"dispenser":               {5, "minecraft:dispenser"},
	"dropper":                 {6, "minecraft:dropper"},
	"sign":                    {7, "minecraft:sign"},
	"hanging_sign":            {8, "minecraft:hanging_sign"},
	"brewing_stand":           {12, "minecraft:brewing_stand"},
	"hopper":                  {18, "minecraft:hopper"},
	"beacon":                  {15, "minecraft:beacon"},
	"shulker_box":             {24, "minecraft:shulker_box"},
	"barrel":                  {26, "minecraft:barrel"},
	"smoker":                  {27, "minecraft:smoker"},
	"blast_furnace":           {28, "minecraft:blast_furnace"},
	"lectern":                 {29, "minecraft:lectern"},
	"comparator":              {19, "minecraft:comparator"},
	"crafter":                 {42, "minecraft:crafter"},
	"sculk_sensor":            {34, "minecraft:sculk_sensor"},
	"calibrated_sculk_sensor": {35, "minecraft:calibrated_sculk_sensor"},
}

// blockEntityOf resolves a block state to its block entity type. A
// sign is saved and loaded as "minecraft:sign" whatever its wood or
// whether it stands on a wall, and a shulker box as "minecraft:
// shulker_box" whatever its colour.
func blockEntityOf(b world.Block) (blockEntitySpec, bool) {
	name, ok := world.StateOwner(b)
	if !ok {
		return blockEntitySpec{}, false
	}
	if spec, ok := blockEntitySpecs[name]; ok {
		return spec, true
	}
	switch {
	case strings.HasSuffix(name, "_hanging_sign") || strings.HasSuffix(name, "_wall_hanging_sign"):
		return blockEntitySpecs["hanging_sign"], true
	case isSignBlock(b) || strings.HasSuffix(name, "_wall_sign"):
		return blockEntitySpecs["sign"], true
	case name == "shulker_box" || strings.HasSuffix(name, "_shulker_box"):
		return blockEntitySpecs["shulker_box"], true
	}
	// Every other 26.2 block entity type (genblockentity.go).
	if typ, ok := blockEntityTypeName(name); ok {
		return blockEntitySpecForType(typ)
	}
	return blockEntitySpec{}, false
}

// chunkBlockEntities builds the block_entities list for one chunk of
// dimension d, ordered by position so the written chunk is a function
// of the world and not of a map's iteration order. The real server
// writes them in no particular order and reads them by position.
func (s *Server) chunkBlockEntities(d *dimension, cx, cz int) []map[string]any {
	type entry struct {
		pos world.Pos
		nbt map[string]any
	}
	s.installGenBlockEntities(d, cx, cz)
	var out []entry
	add := func(pos world.Pos, v map[string]any) {
		if pos.X>>4 != cx || pos.Z>>4 != cz {
			return
		}
		out = append(out, entry{pos, v})
	}
	// A block whose block entity type the real server does not keep -
	// an anvil, a stonecutter - is never written, even though mc-go
	// holds its slots while the server runs.
	keep := func(pos world.Pos) bool {
		_, ok := blockEntityOf(d.w.GetBlock(pos))
		return ok
	}

	d.signs.mu.Lock()
	for pos, st := range d.signs.signs {
		if !keep(pos) {
			continue
		}
		spec, _ := blockEntityOf(d.w.GetBlock(pos))
		add(pos, blockEntityNBT(pos, spec, func(extra map[string]any) {
			extra["front_text"] = signTextNBT(st.Front, st.frontColor, st.frontGlowing)
			extra["back_text"] = signTextNBT(st.Back, st.backColor, st.backGlowing)
			extra["is_waxed"] = boolByte(st.waxed)
		}))
	}
	d.signs.mu.Unlock()

	d.chests.mu.Lock()
	for pos, c := range d.chests.chests {
		if !keep(pos) {
			continue
		}
		spec, _ := blockEntityOf(d.w.GetBlock(pos))
		slots := make([]ItemStack, len(c.slots))
		copy(slots, c.slots[:])
		lt, seed := c.lootTable, c.lootSeed
		add(pos, blockEntityNBT(pos, spec, func(extra map[string]any) {
			if name := d.beNames[pos]; name != "" {
				extra["CustomName"] = name
			}
			// trySaveLootTable: an unrolled loot table (a dungeon
			// chest) is saved instead of the items.
			if lt != "" {
				extra["LootTable"] = lt
				if seed != 0 {
					extra["LootTableSeed"] = seed
				}
				return
			}
			extra["Items"] = itemsNBT(slots)
		}))
	}
	d.chests.mu.Unlock()

	d.hoppers.mu.Lock()
	for pos, h := range d.hoppers.hoppers {
		if !keep(pos) {
			continue
		}
		spec, _ := blockEntityOf(d.w.GetBlock(pos))
		slots := make([]ItemStack, len(h.slots))
		copy(slots, h.slots[:])
		cooldown := h.cooldown
		add(pos, blockEntityNBT(pos, spec, func(extra map[string]any) {
			extra["Items"] = itemsNBT(slots)
			extra["TransferCooldown"] = cooldown
		}))
	}
	d.hoppers.mu.Unlock()

	d.furnaces.mu.Lock()
	for pos, f := range d.furnaces.furnaces {
		if !keep(pos) {
			continue
		}
		spec, _ := blockEntityOf(d.w.GetBlock(pos))
		slots := make([]ItemStack, len(f.slots))
		copy(slots, f.slots[:])
		burn, burnTotal, cook, cookTotal := f.burn, f.burnTotal, f.cook, f.cookTotal
		used := make(map[string]any, len(f.used))
		for id, n := range f.used {
			used[id] = n
		}
		add(pos, blockEntityNBT(pos, spec, func(extra map[string]any) {
			extra["Items"] = itemsNBT(slots)
			extra["lit_time_remaining"] = int16(burn)
			extra["lit_total_time"] = int16(burnTotal)
			extra["cooking_time_spent"] = int16(cook)
			extra["cooking_total_time"] = int16(cookTotal)
			// Which recipes this furnace has used, for the recipe
			// book's "you have made this" tracking: a count per recipe
			// id, and an empty compound for a furnace that has smelted
			// nothing. The id names the variant that was cooked, so a
			// smoker's beef is counted under cooked_beef_from_smoking
			// while a furnace's is counted under cooked_beef.
			extra["RecipesUsed"] = used
		}))
	}
	d.furnaces.mu.Unlock()

	d.containers.mu.Lock()
	for pos, c := range d.containers.containers {
		if !keep(pos) {
			continue
		}
		spec, _ := blockEntityOf(d.w.GetBlock(pos))
		slots := make([]ItemStack, len(c.slots))
		copy(slots, c.slots[:])
		brew, fuel, disabled := c.brewTime, c.fuel, c.disabled
		trg, ctr := c.triggered, c.craftingTicks
		add(pos, blockEntityNBT(pos, spec, func(extra map[string]any) {
			extra["Items"] = itemsNBT(slots)
			if spec.name == "minecraft:brewing_stand" {
				extra["BrewTime"] = int16(brew)
				extra["Fuel"] = int8(fuel)
			}
			if spec.name == "minecraft:crafter" {
				crafterNBTExtra(extra, disabled, trg, ctr)
			}
		}))
	}
	d.containers.mu.Unlock()

	// Sculk sensors save their vibration state — the last frequency
	// unconditionally, the listener's selector/travel data when one
	// is pending or traveling.
	for pos, sen := range d.sculk {
		if sen == nil || !keep(pos) {
			continue
		}
		spec, _ := blockEntityOf(d.w.GetBlock(pos))
		add(pos, blockEntityNBT(pos, spec, func(extra map[string]any) {
			sculkNBT(extra, sen)
		}))
	}

	for pos := range d.jukeboxes {
		if name, _ := world.StateOwner(d.w.GetBlock(pos)); name == "jukebox" {
			spec, _ := blockEntityOf(d.w.GetBlock(pos))
			add(pos, blockEntityNBT(pos, spec, func(extra map[string]any) { s.jukeboxSaveFields(d, pos, extra) }))
		}
	}

	// Block entities kept as their saved fields, where no store above
	// already wrote the position.
	have := make(map[world.Pos]bool, len(out))
	for _, e := range out {
		have[e.pos] = true
	}
	for pos := range d.genericBE {
		if have[pos] {
			continue
		}
		if g, spec, ok := genericBlockEntityAt(d, pos); ok {
			add(pos, genericSaveNBT(d, pos, g, spec))
		}
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].pos, out[j].pos
		if a.X != b.X {
			return a.X < b.X
		}
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.Z < b.Z
	})
	list := make([]map[string]any, 0, len(out))
	for _, e := range out {
		list = append(list, e.nbt)
	}
	return list
}

// blockEntitySaveMap renders the save-form NBT for the block entity
// at pos — the same map chunkBlockEntities writes into chunk saves —
// or reports nil when the block owns no block entity mc-go models.
// /data reads and rewrites it; keepPacked is dropped like the wire
// form drops it (the real server's save tag keeps it but its command
// view does not print it — measured 26.2: a chest query prints
// components,x,y,Items,z,id and no keepPacked).
func (s *Server) blockEntitySaveMap(d *dimension, pos world.Pos) (map[string]any, bool) {
	blk := d.w.GetBlock(pos)
	s.structureBlockAt(d, pos)
	s.jigsawAt(d, pos)
	s.testBlockAt(d, pos)
	s.testInstanceAt(d, pos)
	spec, ok := blockEntityOf(blk)
	if !ok {
		return nil, false
	}
	if g, gspec, ok := genericBlockEntityAt(d, pos); ok && !typedBlockEntityIDs[g.id] {
		return genericSaveNBT(d, pos, g, gspec), true
	}
	switch spec.name {
	case "minecraft:jukebox":
		return blockEntityNBT(pos, spec, func(extra map[string]any) { s.jukeboxSaveFields(d, pos, extra) }), true
	case "minecraft:comparator":
		// ComparatorBlockEntity saves its last published analog level
		// unconditionally - OutputSignal is there even when it is 0.
		return blockEntityNBT(pos, spec, func(extra map[string]any) {
			extra["OutputSignal"] = int32(d.comparatorOut[pos])
		}), true
	case "minecraft:lectern":
		// A lectern owns its block entity empty or full: the tag is
		// the position frame alone until a Book is stored, then Book +
		// Page under LecternBlockEntity.saveAdditional's !book.isEmpty().
		d.containers.mu.Lock()
		defer d.containers.mu.Unlock()
		var book ItemStack
		var pg int32
		if c := d.containers.containers[pos]; c != nil {
			book, pg = c.slots[lecternSlot], c.page
		}
		return blockEntityNBT(pos, spec, func(extra map[string]any) {
			if !book.Empty() {
				extra["Book"] = itemNBT(book)
				extra["Page"] = pg
			}
		}), true
	case "minecraft:sign", "minecraft:hanging_sign":
		d.signs.mu.Lock()
		defer d.signs.mu.Unlock()
		st, ok := d.signs.signs[pos]
		if !ok {
			return nil, false
		}
		snap := *st
		return blockEntityNBT(pos, spec, func(extra map[string]any) {
			extra["front_text"] = signTextNBT(snap.Front, snap.frontColor, snap.frontGlowing)
			extra["back_text"] = signTextNBT(snap.Back, snap.backColor, snap.backGlowing)
			extra["is_waxed"] = boolByte(snap.waxed)
		}), true
	case "minecraft:chest", "minecraft:trapped_chest":
		if c := s.peekChest(d, pos); c != nil {
			slots := make([]ItemStack, len(c.slots))
			copy(slots, c.slots[:])
			lt, seed := c.lootTable, c.lootSeed
			return blockEntityNBT(pos, spec, func(extra map[string]any) {
				// trySaveLootTable: an armed table replaces Items in
				// the saved tag.
				if lt == "" {
					extra["Items"] = itemsNBT(slots)
				} else {
					extra["LootTable"] = lt
					if seed != 0 {
						extra["LootTableSeed"] = seed
					}
				}
				if n := d.beNames[pos]; n != "" {
					extra["CustomName"] = n
				}
			}), true
		}
		return nil, false
	case "minecraft:sculk_sensor", "minecraft:calibrated_sculk_sensor":
		// A sensor block always carries its block entity — a
		// never-triggered one saves the -1 default frequency and an
		// idle listener.
		sen := d.sculk[pos]
		if sen == nil {
			sen = newSculkSensor()
		}
		return blockEntityNBT(pos, spec, func(extra map[string]any) {
			sculkNBT(extra, sen)
		}), true
	case "minecraft:hopper":
		if h := s.peekHopper(d, pos); h != nil {
			slots := make([]ItemStack, len(h.slots))
			copy(slots, h.slots[:])
			cooldown := h.cooldown
			return blockEntityNBT(pos, spec, func(extra map[string]any) {
				extra["Items"] = itemsNBT(slots)
				extra["TransferCooldown"] = cooldown
				if n := d.beNames[pos]; n != "" {
					extra["CustomName"] = n
				}
			}), true
		}
		return nil, false
	case "minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker":
		if f := s.peekFurnace(d, pos); f != nil {
			d.furnaces.mu.Lock()
			slots := make([]ItemStack, len(f.slots))
			copy(slots, f.slots[:])
			burn, burnTotal, cook, cookTotal := f.burn, f.burnTotal, f.cook, f.cookTotal
			used := make(map[string]any, len(f.used))
			for id, n := range f.used {
				used[id] = n
			}
			d.furnaces.mu.Unlock()
			return blockEntityNBT(pos, spec, func(extra map[string]any) {
				extra["Items"] = itemsNBT(slots)
				extra["lit_time_remaining"] = int16(burn)
				extra["lit_total_time"] = int16(burnTotal)
				extra["cooking_time_spent"] = int16(cook)
				extra["cooking_total_time"] = int16(cookTotal)
				extra["RecipesUsed"] = used
				if n := d.beNames[pos]; n != "" {
					extra["CustomName"] = n
				}
			}), true
		}
		return nil, false
	}
	d.containers.mu.Lock()
	defer d.containers.mu.Unlock()
	if c, ok := d.containers.containers[pos]; ok {
		slots := make([]ItemStack, len(c.slots))
		copy(slots, c.slots[:])
		brew, fuel, disabled := c.brewTime, c.fuel, c.disabled
		trg, ctr := c.triggered, c.craftingTicks
		lt, seed := c.lootTable, c.lootSeed
		return blockEntityNBT(pos, spec, func(extra map[string]any) {
			if spec.name == "minecraft:beacon" {
				// A beacon has no Items: its payment slot lives on the
				// menu, and the block entity saves only its pyramid
				// level and chosen powers.
				be := d.beacons[pos]
				levels := int32(0)
				if be != nil {
					levels = int32(be.levels)
					if be.primary != "" {
						extra["primary_effect"] = "minecraft:" + be.primary
					}
					if be.secondary != "" {
						extra["secondary_effect"] = "minecraft:" + be.secondary
					}
				}
				extra["Levels"] = levels
				if n := d.beNames[pos]; n != "" {
					extra["CustomName"] = n
				}
				return
			}
			if spec.name == "minecraft:brewing_stand" {
				extra["BrewTime"] = int16(brew)
				extra["Fuel"] = int8(fuel)
			}
			if spec.name == "minecraft:crafter" {
				crafterNBTExtra(extra, disabled, trg, ctr)
			}
			if lt != "" {
				extra["LootTable"] = lt
				if seed != 0 {
					extra["LootTableSeed"] = seed
				}
			} else {
				extra["Items"] = itemsNBT(slots)
			}
			if n := d.beNames[pos]; n != "" {
				extra["CustomName"] = n
			}
		}), true
	}
	return nil, false
}

// clientBlockEntity is one entry of the block entity array a chunk
// packet carries: where the block entity is, which type it is, and the
// NBT the real server sends for it. A nil nbt is the empty payload the
// real server sends for a block entity whose data the client does not
// need.
type clientBlockEntity struct {
	pos  world.Pos
	spec blockEntitySpec
	nbt  []byte
}

// chunkClientBlockEntities gathers the block entity array for column
// (cx, cz): every block entity the chunk holds, in position order.
//
// What travels is not the block entity's saved form. Each one is sent as
// the data a client needs to draw it, and for everything mc-go models
// except a sign that data is nothing at all - measured on 26.2 by
// joining it and reading the chunk packet back: a chest holding three
// diamonds, a barrel holding five apples, a furnace mid-cook with a lit
// time and a cook time, a hopper, a dropper, a dispenser, a shulker box,
// a brewing stand and a crafter all arrive as a single TAG_End byte,
// while a sign and a hanging sign arrive as a nameless compound of
// front_text, back_text and is_waxed (the same compound the sign's own
// block entity data update carries). The entry itself is always sent: a
// client that is never told about a chest still draws one, but a sign
// with no entry keeps blank text.
//
// The real server's order is its own block entity map's, which is hash
// order and not a property of the world; mc-go orders by position so the
// packet is a function of the world.
func (s *Server) chunkClientBlockEntities(d *dimension, cx, cz int) []clientBlockEntity {
	if d.terrain != nil {
		// The packet is built from the generated column; its block
		// entities join the level first.
		d.w.BaseChunk(cx, cz)
		s.installGenBlockEntities(d, cx, cz)
	}
	var out []clientBlockEntity
	add := func(pos world.Pos, nbt []byte) {
		if pos.X>>4 != cx || pos.Z>>4 != cz {
			return
		}
		spec, ok := blockEntityOf(d.w.GetBlock(pos))
		if !ok {
			return
		}
		out = append(out, clientBlockEntity{pos: pos, spec: spec, nbt: nbt})
	}

	d.signs.mu.Lock()
	for pos, st := range d.signs.signs {
		add(pos, signNBT(*st))
	}
	d.signs.mu.Unlock()

	d.chests.mu.Lock()
	for pos := range d.chests.chests {
		add(pos, nil)
	}
	d.chests.mu.Unlock()

	d.furnaces.mu.Lock()
	for pos := range d.furnaces.furnaces {
		add(pos, nil)
	}
	d.furnaces.mu.Unlock()

	d.hoppers.mu.Lock()
	for pos := range d.hoppers.hoppers {
		add(pos, nil)
	}
	d.hoppers.mu.Unlock()

	d.containers.mu.Lock()
	for pos := range d.containers.containers {
		add(pos, nil)
	}
	d.containers.mu.Unlock()

	// Sculk sensor block entities carry no client payload — the
	// chunk packet's entry exists so the client registers the
	// listener on a freshly streamed sensor.
	for pos := range d.sculk {
		add(pos, nil)
	}

	for pos := range d.jukeboxes {
		add(pos, nil)
	}

	// Generic block entities send their type's update tag.
	have := make(map[world.Pos]bool, len(out))
	for _, e := range out {
		have[e.pos] = true
	}
	for pos := range d.genericBE {
		if have[pos] || pos.X>>4 != cx || pos.Z>>4 != cz {
			continue
		}
		if g, spec, ok := genericBlockEntityAt(d, pos); ok {
			out = append(out, clientBlockEntity{pos: pos, spec: spec,
				nbt: blockEntityUpdateTag(strings.TrimPrefix(g.id, "minecraft:"), genericSaveFields(d, pos, g))})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].pos, out[j].pos
		if a.X != b.X {
			return a.X < b.X
		}
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.Z < b.Z
	})
	return out
}

// blockEntityNBT is the frame every saved block entity shares: its
// position, its type, and the fields the real server writes on every
// one of them - keepPacked, and the empty components compound the
// block entity's own data components live in.
func blockEntityNBT(pos world.Pos, spec blockEntitySpec, fill func(map[string]any)) map[string]any {
	m := map[string]any{
		"id":         spec.name,
		"x":          int32(pos.X),
		"y":          int32(pos.Y),
		"z":          int32(pos.Z),
		"keepPacked": int8(0),
		"components": map[string]any{},
	}
	fill(m)
	return m
}

// signTextNBT renders one side of a sign the way the real server stores
// it and sends it. The four lines are the text components the client
// sent, held verbatim as strings; the colour and the glow are the side's
// own styling, and a side that has never been dyed is black.
func signTextNBT(lines [4]string, color string, glowing bool) map[string]any {
	msgs := nbt.List{Elem: nbt.TagString, Items: make([]any, 0, 4)}
	for _, l := range lines {
		msgs.Items = append(msgs.Items, l)
	}
	return map[string]any{
		"messages":         msgs,
		"color":            signColor(color),
		"has_glowing_text": boolByte(glowing),
	}
}

// itemsNBT renders a slot list, skipping the empty slots - the real
// server writes only the slots that hold something, and each entry
// names its own slot.
func itemsNBT(slots []ItemStack) nbt.List {
	list := nbt.List{Elem: nbt.TagCompound}
	for i, it := range slots {
		if it.Empty() {
			continue
		}
		list.Items = append(list.Items, itemNBTSlotted(it, int8(i)))
	}
	return list
}

// itemNBTSlotted is one stack inside a container: the slot it sits in
// is part of the entry.
func itemNBTSlotted(it ItemStack, slot int8) map[string]any {
	m := itemNBT(it)
	m["Slot"] = slot
	return m
}

// itemNBT is one stack the way 26.2 stores it: the registry name, the
// count, and the components the stack carries - the last of which is
// left out entirely when the stack carries none, so a plain diamond in
// a chest is three fields and not four.
func itemNBT(it ItemStack) map[string]any {
	name, ok := world.ItemName(it.ID)
	if !ok {
		name = "air"
	}
	m := map[string]any{
		"id":    "minecraft:" + name,
		"count": it.Count,
	}
	if comps := itemComponentsNBT(it); len(comps) > 0 {
		m["components"] = comps
	}
	return m
}

// itemComponentsNBT renders a stack's data components the way 26.2
// stores them: damage, enchantments, and the items inside a shulker
// box, which travel with the item and are what a dropped box holds.
func itemComponentsNBT(it ItemStack) map[string]any {
	comps := map[string]any{}
	if it.Bundle != "" {
		comps["minecraft:bundle_contents"] = bundleContentsNBT(it)
	}
	// Vanilla serializes the durability and enchantment components
	// into the stack's NBT as minecraft:damage (int) and
	// minecraft:enchantments (flat name->level map).
	if it.Damage > 0 {
		comps["minecraft:damage"] = it.Damage
	}
	if it.RepairCost > 0 {
		comps["minecraft:repair_cost"] = it.RepairCost
	}
	if it.Ench != "" {
		lvls := map[string]any{}
		for _, e := range strings.Split(it.Ench, ",") {
			n, lv, ok := strings.Cut(e, ":")
			if !ok || n == "" {
				continue
			}
			if v, err := strconv.Atoi(lv); err == nil {
				lvls["minecraft:"+n] = int32(v)
			}
		}
		if len(lvls) > 0 {
			comps["minecraft:enchantments"] = lvls
		}
	}
	if it.StoredEnch != "" {
		if levels := itemComponentsNBT(ItemStack{Ench: it.StoredEnch})["minecraft:enchantments"]; levels != nil {
			comps["minecraft:stored_enchantments"] = levels
		}
	}
	if it.PotionScaleSet {
		comps["minecraft:potion_duration_scale"] = it.PotionScale
	}
	if it.Potion != "" || it.PotionExtra != "" {
		comps["minecraft:potion_contents"] = potionContentsValue(it)
	}
	if it.Instrument != "" {
		comps["minecraft:instrument"] = "minecraft:" + it.Instrument
	}
	if it.Trim != "" {
		mat, pat, _ := strings.Cut(it.Trim, "/")
		comps["minecraft:trim"] = map[string]any{
			"material": "minecraft:" + mat,
			"pattern":  "minecraft:" + pat,
		}
	}
	if it.Bees > 0 {
		// Bees serializes as a list of Occupant records: the
		// TypedEntityData's type field folds back into entity_data
		// as the id key; the count-only model ships default ticks.
		list := nbt.List{Elem: nbt.TagCompound}
		for i := int32(0); i < it.Bees; i++ {
			list.Items = append(list.Items, map[string]any{
				"entity_data":       map[string]any{"id": "minecraft:bee"},
				"ticks_in_hive":     int32(0),
				"min_ticks_in_hive": int32(600),
			})
		}
		comps["minecraft:bees"] = list
	}
	if it.BlockState != "" {
		// BlockItemStateProperties is the flat property map —
		// string keys to string values ({honey_level:"4"}).
		bs := map[string]any{}
		for _, kv := range strings.Split(it.BlockState, ",") {
			k, v, ok := strings.Cut(kv, "=")
			if ok && k != "" {
				bs[k] = v
			}
		}
		if len(bs) > 0 {
			comps["minecraft:block_state"] = bs
		}
	}
	// custom_name is the literal component JSON string verbatim (the
	// component CODEC stores the serialized text component).
	if it.Name != "" {
		comps["minecraft:custom_name"] = it.Name
	}
	// The book-content components emit in their save shape — the
	// codec's omitted-at-default fields are already stripped.
	if v := bookContentNBT(it, false); v != nil {
		comps["minecraft:writable_book_content"] = v
	}
	if v := bookContentNBT(it, true); v != nil {
		comps["minecraft:written_book_content"] = v
	}
	// container_loot = SeededContainerLoot{loot_table, seed} — seed is
	// an optionalFieldOf(0), which the encoder omits at the default.
	if it.LootTable != "" {
		cl := map[string]any{"loot_table": it.LootTable}
		if it.LootSeed != 0 {
			cl["seed"] = it.LootSeed
		}
		comps["minecraft:container_loot"] = cl
	}
	if it.Contents == nil {
		return comps
	}
	list := nbt.List{Elem: nbt.TagCompound}
	for i := range it.Contents {
		inner := it.Contents[i]
		if inner.Empty() {
			continue
		}
		list.Items = append(list.Items, map[string]any{
			"slot": int32(i),
			"item": itemNBTInner(inner),
		})
	}
	if len(list.Items) == 0 {
		return comps
	}
	comps["minecraft:container"] = list
	return comps
}

// itemNBTInner is a stack nested inside a component, where the slot is
// the component's own field and a count of one is left out.
func itemNBTInner(it ItemStack) map[string]any {
	m := map[string]any{"id": itemNBT(it)["id"]}
	if it.Count != 1 {
		m["count"] = it.Count
	}
	if comps := itemComponentsNBT(it); len(comps) > 0 {
		m["components"] = comps
	}
	return m
}

// restoreBlockEntities turns the block entities a load read out of the
// region files back into the per-dimension stores the server runs on.
// It runs once, after a world directory's chunks are loaded, and
// nothing in it can fail: a block entity mc-go cannot model is one it
// was not modelling before the save either.
func (s *Server) restoreBlockEntities() {
	for _, d := range s.dims {
		for _, be := range d.w.BlockEntities() {
			s.restoreBlockEntity(d, be)
		}
	}
}

// restoreBlockEntity restores one saved block entity into d.
func (s *Server) restoreBlockEntity(d *dimension, be map[string]any) {
	id, _ := be["id"].(string)
	x, okx := nbtNumber(be["x"])
	y, oky := nbtNumber(be["y"])
	z, okz := nbtNumber(be["z"])
	if !okx || !oky || !okz {
		return
	}
	pos := world.Pos{X: int(x), Y: int(y), Z: int(z)}

	switch id {
	case "minecraft:jukebox":
		s.restoreJukebox(d, pos, be)
	case "minecraft:chest", "minecraft:trapped_chest":
		if name, ok := be["CustomName"].(string); ok && name != "" {
			if d.beNames == nil {
				d.beNames = map[world.Pos]string{}
			}
			d.beNames[pos] = name
		} else {
			delete(d.beNames, pos)
		}
		c := s.chestAt(d, pos)
		d.chests.mu.Lock()
		clear(c.slots[:])
		itemsInto(be["Items"], c.slots[:])
		// RandomizableContainer.tryLoadLootTable: an unrolled table and
		// its seed wait for the first opening.
		c.lootTable, c.lootSeed = "", 0
		if lt, ok := be["LootTable"].(string); ok && lt != "" {
			c.lootTable = lt
			if seed, ok := be["LootTableSeed"].(int64); ok {
				c.lootSeed = seed
			}
		}
		d.chests.mu.Unlock()
	case "minecraft:sign", "minecraft:hanging_sign":
		s.ensureSign(d, pos)
		d.signs.mu.Lock()
		st := d.signs.signs[pos]
		st.Front, st.frontColor, st.frontGlowing = signSideFromNBT(be["front_text"])
		st.Back, st.backColor, st.backGlowing = signSideFromNBT(be["back_text"])
		if w, ok := nbtNumber(be["is_waxed"]); ok {
			st.waxed = w != 0
		}
		d.signs.mu.Unlock()
	case "minecraft:hopper":
		h := s.hopperAt(d, pos)
		d.hoppers.mu.Lock()
		clear(h.slots[:])
		itemsInto(be["Items"], h.slots[:])
		if cd, ok := nbtNumber(be["TransferCooldown"]); ok {
			h.cooldown = int32(cd)
		}
		d.hoppers.mu.Unlock()
	case "minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker":
		// The block decides the menu and the cook time, so the store
		// is created from the world rather than from the saved id.
		f := s.furnaceAt(d, pos)
		d.furnaces.mu.Lock()
		clear(f.slots[:])
		itemsInto(be["Items"], f.slots[:])
		f.burn = nbtNumberOr(be["lit_time_remaining"], 0)
		f.burnTotal = nbtNumberOr(be["lit_total_time"], 0)
		f.cook = nbtNumberOr(be["cooking_time_spent"], 0)
		f.cookTotal = nbtNumberOr(be["cooking_total_time"], 0)
		if used, ok := be["RecipesUsed"].(map[string]any); ok {
			for id, v := range used {
				if n, ok := nbtNumber(v); ok {
					if f.used == nil {
						f.used = map[string]int32{}
					}
					f.used[id] = int32(n)
				}
			}
		}
		d.furnaces.mu.Unlock()
	case "minecraft:lectern":
		// LecternBlockEntity.load: the Book stack (resolved on the way
		// in) plus Page clamped to [0, pageCount-1] — a bookless lectern
		// clamps against pageCount 0 and lands on -1.
		c := s.containerAt(d, pos)
		if c == nil {
			return
		}
		d.containers.mu.Lock()
		clear(c.slots)
		var pages int32
		if book, ok := be["Book"].(map[string]any); ok {
			c.slots[lecternSlot] = bookResolve(itemFromNBT(book))
			pages = lecternPageCount(c.slots[lecternSlot])
		}
		pg := int32(nbtNumberOr(be["Page"], 0))
		if pg < 0 {
			pg = 0
		}
		if pg > pages-1 {
			pg = pages - 1
		}
		c.page = pg
		d.containers.mu.Unlock()
	case "minecraft:comparator":
		// ComparatorBlockEntity.load reads the published level back.
		if d.comparatorOut == nil {
			d.comparatorOut = map[world.Pos]int{}
		}
		d.comparatorOut[pos] = int(nbtNumberOr(be["OutputSignal"], 0))
	case "minecraft:sculk_sensor", "minecraft:calibrated_sculk_sensor":
		if d.sculk == nil {
			d.sculk = map[world.Pos]*sculkSensor{}
		}
		d.sculk[pos] = sculkFromNBT(be)
	case "minecraft:barrel", "minecraft:dispenser", "minecraft:dropper",
		"minecraft:shulker_box", "minecraft:brewing_stand", "minecraft:crafter":
		c := s.containerAt(d, pos)
		if c == nil {
			return
		}
		d.containers.mu.Lock()
		clear(c.slots)
		itemsInto(be["Items"], c.slots)
		if bt, ok := nbtNumber(be["BrewTime"]); ok {
			c.brewTime = int32(bt)
		}
		if fu, ok := nbtNumber(be["Fuel"]); ok {
			c.fuel = int32(fu)
		}
		if ds, ok := nbtNumber(be["disabled_slots"]); ok {
			c.disabled = uint16(ds)
		} else if arr, ok := be["disabled_slots"].([]int32); ok {
			// vanilla writes disabled_slots as an IntArray of indices.
			for _, i := range arr {
				if i >= 0 && i < 9 {
					c.disabled |= 1 << uint(i)
				}
			}
		}
		if tr, ok := nbtNumber(be["triggered"]); ok {
			c.triggered = tr != 0
		}
		if ct, ok := nbtNumber(be["crafting_ticks_remaining"]); ok {
			c.craftingTicks = int32(ct)
		}
		if lt, ok := be["LootTable"].(string); ok && lt != "" {
			c.lootTable = lt
			if seed, ok := be["LootTableSeed"].(int64); ok {
				c.lootSeed = seed
			}
		}
		d.containers.mu.Unlock()
	default:
		if !typedBlockEntityIDs[id] {
			s.restoreGenericBlockEntity(d, pos, id, be)
		}
	}
}

// itemsInto reads a saved slot list into the storage a block entity
// was created with. Each entry names its own slot, and an entry whose
// slot is outside that storage is dropped rather than shifting the
// rest: a block written by a server with a bigger menu loses the slots
// this one does not have.
func itemsInto(v any, dst []ItemStack) {
	list, ok := v.(nbt.List)
	if !ok {
		return
	}
	for _, anyItem := range list.Items {
		m, ok := anyItem.(map[string]any)
		if !ok {
			continue
		}
		slot, ok := nbtNumber(m["Slot"])
		if !ok || slot < 0 || int(slot) >= len(dst) {
			continue
		}
		dst[slot] = itemFromNBT(m)
	}
}

// itemFromNBT reads one saved stack, components included.
func itemFromNBT(m map[string]any) ItemStack {
	name, _ := m["id"].(string)
	var it ItemStack
	if id, ok := world.ItemID(strings.TrimPrefix(name, "minecraft:")); ok {
		it.ID = id
	}
	it.Count = int32(nbtNumberOr(m["count"], 1))
	if comps, ok := m["components"].(map[string]any); ok {
		if v, ok := comps["minecraft:potion_duration_scale"]; ok {
			_ = setPotionScale(&it, v)
		}
		if v, ok := comps["minecraft:potion_contents"]; ok {
			_ = setPotionContents(&it, v)
		}
		it.Contents = shulkerContentsFromNBT(comps)
		if v, ok := comps["minecraft:bundle_contents"]; ok {
			it.Bundle, _ = bundleContentsFromNBT(v)
		}
		it.BookWritable, it.BookWritten = bookContentFromNBT(comps)
		if tr, ok := comps["minecraft:trim"].(map[string]any); ok {
			mat, _ := tr["material"].(string)
			pat, _ := tr["pattern"].(string)
			if mat != "" && pat != "" {
				it.Trim = strings.TrimPrefix(mat, "minecraft:") + "/" + strings.TrimPrefix(pat, "minecraft:")
			}
		}
		decoded := modeledItemComponents(it, comps)
		// Preserve the dedicated native readers, especially recursive
		// container entries and trim, which have a richer save shape than
		// the item command parser. Common fields come from the same decoder
		// as item commands and direct setblock inventory tags.
		decoded.Contents, decoded.Bundle = it.Contents, it.Bundle
		decoded.Trim = it.Trim
		decoded.BookWritable, decoded.BookWritten = it.BookWritable, it.BookWritten
		decoded.Potion, decoded.PotionExtra = it.Potion, it.PotionExtra
		decoded.PotionScaleSet, decoded.PotionScale = it.PotionScaleSet, it.PotionScale
		it = decoded
	}
	return it
}

// shulkerContentsFromNBT reads the minecraft:container component, the
// items stored inside a shulker box, into the box's own storage.
func shulkerContentsFromNBT(comps map[string]any) *[shulkerSlots]ItemStack {
	list, ok := comps["minecraft:container"].(nbt.List)
	if !ok {
		return nil
	}
	var box [shulkerSlots]ItemStack
	stored := false
	for _, anyEntry := range list.Items {
		entry, ok := anyEntry.(map[string]any)
		if !ok {
			continue
		}
		slot, ok := nbtNumber(entry["slot"])
		if !ok || slot < 0 || int(slot) >= shulkerSlots {
			continue
		}
		inner, ok := entry["item"].(map[string]any)
		if !ok {
			continue
		}
		box[slot] = itemFromNBT(inner)
		stored = stored || !box[slot].Empty()
	}
	if !stored {
		return nil
	}
	return &box
}

// signSideFromNBT reads one side of a saved or transmitted sign: its
// lines, which are the text components the client typed, held as strings,
// and its styling. A sign with no side at all, or fewer lines than a sign
// shows, reads as blank.
func signSideFromNBT(v any) ([4]string, string, bool) {
	var lines [4]string
	var color string
	var glowing bool
	side, ok := v.(map[string]any)
	if !ok {
		return lines, color, glowing
	}
	if c, ok := side["color"].(string); ok {
		color = c
	}
	if g, ok := nbtNumber(side["has_glowing_text"]); ok {
		glowing = g != 0
	}
	msgs, ok := side["messages"].(nbt.List)
	if !ok {
		return lines, color, glowing
	}
	for i, m := range msgs.Items {
		if i >= len(lines) {
			break
		}
		if s, ok := m.(string); ok {
			lines[i] = s
		}
	}
	return lines, color, glowing
}

// nbtNumber reads any of the integer tags a saved field may carry.
func nbtNumber(v any) (int64, bool) {
	switch n := v.(type) {
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float32:
		return int64(n), true
	case float64:
		return int64(n), true
	}
	return 0, false
}

// nbtNumberOr is nbtNumber with a default for a missing field.
func nbtNumberOr(v any, def int32) int32 {
	if n, ok := nbtNumber(v); ok {
		return int32(n)
	}
	return def
}
