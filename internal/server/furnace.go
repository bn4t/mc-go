package server

import (
	"sync"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Furnace menu layout: 0 input, 1 fuel, 2 output, then the player rows.
// The blast furnace and the smoker are the same block entity with their
// own menu type and a cook time that is half the furnace's, which is
// what the vanilla recipe types bake in (smelting 200 ticks, blasting
// and smoking 100; the bundled data pack leaves cookingtime unset, so
// the recipe type's default applies).
const (
	furnaceSlots     = 3
	furnaceInput     = 0
	furnaceFuel      = 1
	furnaceOutput    = 2
	menuFurnace      = 14 // minecraft:furnace in the menu registry
	furnaceCookTicks = 200
	blastCookTicks   = 100
	smokerCookTicks  = 100
	// furnaceNoRecipeTicks is what the real server reports as a cook
	// total for an input its recipe type has no recipe for - and for an
	// input that has just been emptied, which is the same lookup
	// answered by the same fallback.
	furnaceNoRecipeTicks = 200
)

// furnaceData is one furnace block entity's state.
type furnaceData struct {
	slots     [furnaceSlots]ItemStack
	burn      int32 // remaining burn ticks
	burnTotal int32
	cook      int32
	cookTotal int32
	// kind and cookTicks are the menu type and cook duration of the
	// block this furnace belongs to (plain, blast, or smoker).
	kind      int32
	cookTicks int32
	title     string
	// used counts how many times each recipe has been cooked here, by
	// recipe id, which is the RecipesUsed compound the block saves.
	used map[string]int32
}

// furnaceStore keeps furnace state by position.
type furnaceStore struct {
	mu       sync.Mutex
	furnaces map[world.Pos]*furnaceData
}

// isFurnaceBlock reports whether a block state belongs to the furnace
// family, which shares one block entity and one smelting tick.
func isFurnaceBlock(b world.Block) bool {
	name, ok := world.StateOwner(b)
	if !ok {
		return false
	}
	return name == "furnace" || name == "blast_furnace" || name == "smoker"
}

// cookRecipe is one furnace-family recipe: the item an input turns into,
// how long the block takes over it, and the recipe's own id - which the
// block entity records in RecipesUsed for the recipe book's "you have
// made this" tracking, and which names the variant that was actually
// used (a cooked beef from a smoker is `cooked_beef_from_smoking`, not
// the plain smoke-oven-agnostic `cooked_beef` a furnace writes).
type cookRecipe struct {
	result    string
	cookTicks int32
	id        string
}

// cookRecipes maps a recipe type - and so one member of the furnace
// family - to what that block cooks, by input item name. It is built
// from the bundled data pack rather than from a hand-written table, so
// the accept and reject edges are the real server's: the furnace takes
// everything the smelting type lists, the blast furnace only what
// blasting lists (ores, tools, armour) and the smoker only what smoking
// lists (food). A hand table had thirteen entries and so accepted
// thirteen inputs in all three blocks, which is wrong in both
// directions - it smelted iron pickaxes nowhere and beef everywhere.
//
// Tag ingredients are expanded to their member items by the generator,
// so an input is found by name alone.
var cookRecipes = buildCookRecipes()

func buildCookRecipes() map[string]map[string]cookRecipe {
	out := map[string]map[string]cookRecipe{}
	for _, r := range data.Recipes {
		var def int32
		switch r.Type {
		case data.Smelting:
			def = furnaceCookTicks
		case data.Blasting:
			def = blastCookTicks
		case data.Smoking:
			def = smokerCookTicks
		default:
			continue
		}
		// The recipe's own cookingtime wins where the pack sets one; the
		// bundled pack leaves it unset, so the recipe type's default
		// applies, which is what the real server bakes into each type.
		ticks := def
		if r.CookingTime > 0 {
			ticks = int32(r.CookingTime)
		}
		byItem := out[r.Type]
		if byItem == nil {
			byItem = map[string]cookRecipe{}
			out[r.Type] = byItem
		}
		id := "minecraft:" + r.ID
		for _, ing := range r.Ingredients {
			for _, item := range ing.Items {
				byItem[item] = cookRecipe{result: r.Result.Item, cookTicks: ticks, id: id}
			}
		}
	}
	return out
}

// recipeType is the recipe type a furnace-family block cooks with, and
// so which block's recipe set applies to it.
func recipeType(kind int32) string {
	switch kind {
	case menuBlastFurnace:
		return data.Blasting
	case menuSmoker:
		return data.Smoking
	}
	return data.Smelting
}

// cookFor finds the recipe a furnace-family block has for an item.
func cookFor(kind int32, item string) (cookRecipe, bool) {
	r, ok := cookRecipes[recipeType(kind)][item]
	return r, ok
}

// furnaceAccepts reports whether a menu click may place the stack in a
// furnace slot: the input takes what the block's recipe set smelts,
// the fuel takes what burns.
func furnaceAccepts(f *furnaceData, slot int, it ItemStack) bool {
	if slot == furnaceFuel {
		return isFuelItem(it.ID)
	}
	name, ok := itemNameOf(it.ID)
	if !ok {
		return false
	}
	_, ok = cookFor(f.kind, name)
	return ok
}

// fuelTicks is the burn duration of the common fuels.
var fuelTicks = map[string]int32{
	"coal":        1600,
	"charcoal":    1600,
	"oak_planks":  300,
	"stick":       100,
	"oak_log":     300,
	"lava_bucket": 20000,
}

// burnTicks is how long a fuel burns in this block. The smoker and the
// blast furnace burn fuel at twice the rate - a coal lasts 800 ticks in
// them where it lasts 1600 in a furnace, measured in lit_total_time as
// well as lit_time_remaining, so it is the duration that is halved and
// not a second tick consumed per tick.
func (f *furnaceData) burnTicks(fuel int32) int32 {
	if f.kind == menuSmoker || f.kind == menuBlastFurnace {
		return fuel / 2
	}
	return fuel
}

// furnaceVariant resolves the menu type, cook time, and Open Screen
// translation key of the furnace family block at a position.
func furnaceVariant(blk world.Block) (kind, cookTicks int32, title string) {
	name, _ := world.StateOwner(blk)
	switch name {
	case "blast_furnace":
		return menuBlastFurnace, blastCookTicks, "container.blast_furnace"
	case "smoker":
		return menuSmoker, smokerCookTicks, "container.smoker"
	}
	return menuFurnace, furnaceCookTicks, "container.furnace"
}

func (s *Server) ensureFurnace(d *dimension, pos world.Pos) {
	kind, ticks, title := furnaceVariant(d.w.GetBlock(pos))
	d.furnaces.mu.Lock()
	if d.furnaces.furnaces == nil {
		d.furnaces.furnaces = make(map[world.Pos]*furnaceData)
	}
	if f, ok := d.furnaces.furnaces[pos]; ok {
		// The three furnace-family blocks share one store, so a block
		// replaced by another member of the family keeps its contents
		// and only changes menu and cook time.
		f.kind, f.cookTicks, f.title = kind, ticks, title
	} else {
		d.furnaces.furnaces[pos] = &furnaceData{kind: kind, cookTicks: ticks, title: title}
	}
	d.furnaces.mu.Unlock()
}

// furnaceAt returns the live furnace state, creating it if needed.
func (s *Server) furnaceAt(d *dimension, pos world.Pos) *furnaceData {
	s.ensureFurnace(d, pos)
	d.furnaces.mu.Lock()
	defer d.furnaces.mu.Unlock()
	return d.furnaces.furnaces[pos]
}

// removeFurnace drops a furnace's contents when its block goes away.
func (s *Server) removeFurnace(d *dimension, pos world.Pos) {
	d.furnaces.mu.Lock()
	data, ok := d.furnaces.furnaces[pos]
	delete(d.furnaces.furnaces, pos)
	d.furnaces.mu.Unlock()
	if !ok {
		return
	}
	for i := 0; i < furnaceSlots; i++ {
		if data.slots[i].Empty() {
			continue
		}
		e := s.entities.Spawn(&Entity{
			Type:        mustEntityType("item"),
			Dim:         d.name,
			X:           float64(pos.X) + 0.5,
			Y:           float64(pos.Y) + 0.5,
			Z:           float64(pos.Z) + 0.5,
			Item:        data.slots[i],
			PickupDelay: 10,
		})
		data.slots[i] = ItemStack{}
		s.broadcastEntitySpawn(nil, *e)
	}
}

// openFurnace shows the furnace window, contents, and properties.
func (s *Server) openFurnace(sess *Session, pos world.Pos) {
	f := s.furnaceAt(sess.dimension(), pos)
	id := sess.server.nextWindow.Add(1)
	sess.windowMu.Lock()
	sess.window = &openWindow{id: id, pos: pos, dim: sess.dimension(), furnace: f, kind: f.kind}
	sess.windowState = 0
	// Furnaces emit no container_open — no ContainerOpenersCounter.
	sess.windowMu.Unlock()

	var b protocol.Buffer
	b.VarInt(id)
	b.VarInt(f.kind)
	b.Raw(menuTitle(f.title))
	_ = sess.write(sess.ids().PlayOpenScreen, b.Bytes())
	s.sendFurnaceContent(sess)
	s.sendFurnaceProperties(sess)
}

// sendFurnaceContent syncs the furnace menu.
func (s *Server) sendFurnaceContent(sess *Session) {
	sess.windowMu.Lock()
	w := sess.window
	state := sess.windowState
	sess.windowMu.Unlock()
	if w == nil || w.furnace == nil {
		return
	}
	var b protocol.Buffer
	b.VarInt(w.id)
	b.VarInt(state)
	b.VarInt(furnaceSlots + 36)
	for i := 0; i < furnaceSlots+36; i++ {
		sess.writeSlot(&b, sess.menuStack(i))
	}
	sess.writeSlot(&b, sess.carried)
	_ = sess.write(sess.ids().PlayContainerContent, b.Bytes())
}

// sendFurnaceProperties syncs the burn/cook indicators.
func (s *Server) sendFurnaceProperties(sess *Session) {
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.furnace == nil {
		return
	}
	w.dim.furnaces.mu.Lock()
	f := w.furnace
	props := [4]int32{f.burn, f.burnTotal, f.cook, f.cookTotal}
	w.dim.furnaces.mu.Unlock()
	for i, v := range props {
		var b protocol.Buffer
		b.VarInt(w.id)
		b.Short(int16(i))
		b.Short(int16(v))
		_ = sess.write(sess.ids().PlayContainerProperty, b.Bytes())
	}
}

// refreshCookTotal recomputes the cook total from what is in the input
// slot. The real server does this when the input slot is *written* -
// through a menu click, a hopper, or a command - and not at any other
// time, which is what makes the field look sticky:
//
//   - a furnace given an item with no recipe for its own recipe type
//     reads 200, the real server's own fallback for a recipe it cannot
//     find, and so does one whose input has just been emptied: measured
//     with a furnace holding stone, then beef, then coal, and finally
//     emptied by a command, each writing the input slot;
//   - a furnace that smelts its own input down to nothing keeps the
//     recipe's time instead: a smoker given two beef, cooked through
//     while still burning, reads 100 and not 200, because the smelting
//     path shrinks the stack in place rather than writing the slot;
//   - a furnace whose input a *hopper* fills reads 200 for beef even
//     though nothing has ticked yet: the insert is a slot write, and
//     this is the path the hopper test measures;
//   - a world load does not recompute it: a furnace saved with 0 and an
//     input that has a recipe still reads 0 when the world comes back.
//
// The time itself is the recipe's, which for the furnace family is the
// recipe type's own default, since the data pack leaves cookingtime
// unset: 200 for smelting, 100 for blasting and smoking. An item the
// recipe type has no recipe for reads 200 whatever the block is - a
// smoker holding stone reads 200 and not its own 100.
func (f *furnaceData) refreshCookTotal() {
	f.cookTotal = furnaceNoRecipeTicks
	input := f.slots[furnaceInput]
	if input.Empty() {
		return
	}
	name, ok := itemNameOf(input.ID)
	if !ok {
		return
	}
	recipe, ok := cookFor(f.kind, name)
	if !ok {
		return
	}
	f.cookTotal = recipe.cookTicks
}

// smeltable reports whether the furnace's input can smelt and the
// output has room. The recipe is this block's own recipe type, so a
// smoker given sand has nothing to cook and a blast furnace given beef
// has none either, though both are smelting recipes.
func smeltable(f *furnaceData) (input ItemStack, recipe cookRecipe, result int32, ok bool) {
	input = f.slots[furnaceInput]
	if input.Empty() {
		return ItemStack{}, cookRecipe{}, 0, false
	}
	itemName, ok := itemNameOf(input.ID)
	if !ok {
		return ItemStack{}, cookRecipe{}, 0, false
	}
	recipe, ok = cookFor(f.kind, itemName)
	if !ok {
		return ItemStack{}, cookRecipe{}, 0, false
	}
	outID, ok := world.ItemID(recipe.result)
	if !ok {
		return ItemStack{}, cookRecipe{}, 0, false
	}
	out := f.slots[furnaceOutput]
	if !out.Empty() && (out.ID != outID || out.Count >= maxStack) {
		return ItemStack{}, cookRecipe{}, 0, false
	}
	return input, recipe, outID, true
}

// itemNameOf resolves an item id back to its registry name.
func itemNameOf(id int32) (string, bool) {
	for name, v := range world.ItemIDs {
		if v == id {
			return name, true
		}
	}
	return "", false
}

// furnaceTickStep advances every furnace one tick, in every dimension.
func (s *Server) furnaceTickStep() {
	for _, d := range s.dims {
		d.furnaces.mu.Lock()
		type change struct {
			pos world.Pos
			f   *furnaceData
		}
		var current []change
		for pos, f := range d.furnaces.furnaces {
			current = append(current, change{pos, f})
		}
		d.furnaces.mu.Unlock()
		for _, c := range current {
			s.furnaceTick(d, c.pos, c.f)
		}
	}
}

// furnaceTick advances one furnace and syncs viewers when values
// change.
func (s *Server) furnaceTick(d *dimension, pos world.Pos, f *furnaceData) {
	d.furnaces.mu.Lock()
	_, recipe, result, canSmelt := smeltable(f)
	changed := false
	slotsDirty := false
	if f.burn > 0 {
		f.burn--
		changed = true
	}
	if f.burn == 0 && canSmelt {
		if fuelID := f.slots[furnaceFuel]; !fuelID.Empty() {
			if name, ok := itemNameOf(fuelID.ID); ok {
				if ticks, ok := fuelTicks[name]; ok {
					ticks = f.burnTicks(ticks)
					f.slots[furnaceFuel].Count--
					if f.slots[furnaceFuel].Count <= 0 {
						f.slots[furnaceFuel] = ItemStack{}
					}
					f.burn, f.burnTotal = ticks, ticks
					changed = true
					slotsDirty = true
				}
			}
		}
	}
	// The recipe carries the time: the block's own type default where
	// the pack leaves cookingtime unset, which is every bundled recipe.
	cookTicks := recipe.cookTicks
	if cookTicks <= 0 {
		cookTicks = furnaceCookTicks
	}
	if canSmelt && f.burn > 0 {
		f.cook++
		if f.cook >= cookTicks {
			f.cook = 0
			if recipe.id != "" {
				if f.used == nil {
					f.used = map[string]int32{}
				}
				f.used[recipe.id]++
			}
			f.slots[furnaceInput].Count--
			if f.slots[furnaceInput].Count <= 0 {
				f.slots[furnaceInput] = ItemStack{}
			}
			if f.slots[furnaceOutput].Empty() {
				f.slots[furnaceOutput] = ItemStack{ID: result, Count: 1}
			} else {
				f.slots[furnaceOutput].Count++
			}
			slotsDirty = true
		}
		changed = true
	} else if f.cook != 0 {
		f.cook = 0
		changed = true
	}
	props := [4]int32{f.burn, f.burnTotal, f.cook, f.cookTotal}
	slots := f.slots
	d.furnaces.mu.Unlock()
	if slotsDirty {
		s.rsContainerChanged(d, pos)
	}
	if !changed {
		return
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		sess.windowMu.Lock()
		w := sess.window
		sess.windowMu.Unlock()
		if w == nil || w.furnace != f {
			return true
		}
		for i, val := range props {
			var b protocol.Buffer
			b.VarInt(w.id)
			b.Short(int16(i))
			b.Short(int16(val))
			_ = sess.write(sess.ids().PlayContainerProperty, b.Bytes())
		}
		for i := 0; i < furnaceSlots; i++ {
			var b protocol.Buffer
			b.VarInt(w.id)
			b.VarInt(sess.windowState)
			b.Short(int16(i))
			sess.writeSlot(&b, slots[i])
			_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
		}
		return true
	})
}
