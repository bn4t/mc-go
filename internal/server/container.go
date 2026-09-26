package server

import (
	"math"
	"math/rand"
	"strings"
	"sync"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// The generic container family: every vanilla menu whose container side
// is a fixed list of slots. Chests, hoppers, furnaces, and crafting
// tables keep their own files because their menus carry state this
// model does not (double-chest merging, the transfer tick, burn
// progress, grid recomputation); everything else is the spec table
// below plus one shared click path.
//
// Menu ids come from the official report's "menu" registry; slot counts
// are the vanilla container-side slot counts, which the client's own
// menu class fixes, so a wrong count desynchronizes the window.

// Menu registry ids (official report) owned by the generic family.
// menuGeneric9x3 (barrels), menuFurnace, menuHopper, and menuCrafting
// live with their own implementations.
const (
	menuGeneric3x3       = 6  // dispensers and droppers
	menuCrafter3x3       = 7  // crafter
	menuAnvil            = 8  // anvil, chipped_anvil, damaged_anvil
	menuBeacon           = 9  // beacon
	menuBlastFurnace     = 10 // blast furnace
	menuBrewingStand     = 11 // brewing stand
	menuEnchantment      = 13 // enchanting table
	menuGrindstone       = 15 // grindstone
	menuLectern          = 17 // lectern
	menuLoom             = 18 // loom
	menuShulkerBox       = 20 // shulker box, all 17 colors
	menuSmithing         = 21 // smithing table
	menuSmoker           = 22 // smoker
	menuCartographyTable = 23 // cartography table
	menuStonecutter      = 24 // stonecutter
)

// Container-side slot roles. These are the vanilla menu slot indices,
// which is why they are pinned separately from the spec table: the
// click path and the tick both address slots by role, and a typo here
// would silently write into the wrong slot.
const (
	anvilLeft   = 0
	anvilRight  = 1
	anvilResult = 2

	beaconPayment = 0

	brewPotionFirst = 0
	brewPotionLast  = 2
	brewIngredient  = 3
	brewFuelSlot    = 4

	stonecutterInput  = 0
	stonecutterResult = 1

	smithingTemplate = 0
	smithingBase     = 1
	smithingAddition = 2
	smithingResult   = 3

	grindstoneTop    = 0
	grindstoneBottom = 1
	grindstoneResult = 2

	loomBanner  = 0
	loomDye     = 1
	loomPattern = 2
	loomResult  = 3

	cartographyMap        = 0
	cartographyAdditional = 1
	cartographyResult     = 2

	enchantItem  = 0
	enchantLapis = 1

	lecternBook = 0
)

// containerSpec is one container family: its menu type, its slot count,
// and the two rules a generic click needs — which slots accept items,
// and which are take-only results.
type containerSpec struct {
	menu    int32  // menu registry id
	slots   int    // container-side slots, before the 36 player slots
	title   string // Open Screen translation key, e.g. container.chest
	brew    bool   // brewing stand: own tick and progress properties
	shulker bool   // shulker box: the item form carries the contents
	crafter bool   // crafter: per-slot lock toggles
	// inputs are the slots a quick move from the player side may fill,
	// in the order vanilla scans them. Result slots never appear here.
	inputs []int
	// results are take-only: vanilla's result slots accept no placement.
	results []int
}

// slotMax is a slot's stack limit — vanilla's Slot.getMaxStackSize,
// which most menus inherit as the item's own maximum but a few cap
// lower (the beacon payment slot is 1).
func (spec *containerSpec) slotMax(slot int, it ItemStack) int32 {
	if spec.menu == menuBeacon && slot == beaconPayment {
		return 1
	}
	return maxStackOfID(it.ID)
}

// accepts reports whether the slot takes a stack placed by the player.
// The default is the whole container side minus the result slots; the
// menus whose slots are typed override this below.
func (spec *containerSpec) accepts(slot int, it ItemStack) bool {
	if slot < 0 || slot >= spec.slots || it.Empty() {
		return false
	}
	for _, r := range spec.results {
		if r == slot {
			return false
		}
	}
	switch {
	case spec.brew:
		switch {
		case slot >= brewPotionFirst && slot <= brewPotionLast:
			return potionSlotAccepts(it)
		case slot == brewIngredient:
			return brewIngredientItem(it)
		case slot == brewFuelSlot:
			return itemNamed(it, "blaze_powder")
		}
		return false
	case spec.menu == menuEnchantment && slot == enchantLapis:
		return itemNamed(it, "lapis_lazuli")
	case spec.menu == menuEnchantment && slot == enchantItem:
		// The enchant slot takes only stacks with an enchantable
		// component (vanilla's mayPlace reads the same field).
		return enchantableOf(it) > 0
	case spec.menu == menuLectern:
		// LecternBlockEntity's bookAccess.canPlaceItem is a flat
		// false: the menu slot is display-only, books land via the
		// block's useItemOn.
		return false
	case spec.menu == menuBeacon:
		return beaconPaymentItem(it)
	}
	return true
}

// dests lists the container slots a quick move out of the player side
// targets, in vanilla's scan order. Every container that is not a
// brewing stand takes the plain input list; the brewing stand checks
// the fuel slot first, then the ingredient slot, then the potions, the
// order its own quick-move uses.
func (spec *containerSpec) dests(it ItemStack) []int {
	if !spec.brew {
		return spec.inputs
	}
	switch {
	case itemNamed(it, "blaze_powder"):
		return []int{brewFuelSlot, brewIngredient}
	case brewIngredientItem(it):
		return []int{brewIngredient}
	case potionSlotAccepts(it):
		return []int{brewPotionFirst, brewPotionFirst + 1, brewPotionLast}
	}
	return nil
}

// slotsUpTo lists 0..n-1, the plain container-side slot list.
func slotsUpTo(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// shulkerSlots is how many items a shulker box holds, and therefore how
// many travel inside a broken box's item.
const shulkerSlots = 27

// shulkerSpec is shared by all 17 shulker box colors; the block name
// only decides which item the broken box drops.
var shulkerSpec = &containerSpec{
	menu: menuShulkerBox, slots: shulkerSlots, title: "block.minecraft.shulker_box",
	shulker: true, inputs: slotsUpTo(shulkerSlots),
}

// containerSpecs maps a block name to its container family. Every menu
// in this table is complete as storage: slot layout, click and
// quick-move rules, persistence, and drop-on-break. What is still
// missing is the specialized action that turns the input slots into a
// result, because each one needs item or recipe data mc-go does not
// carry yet. The remaining work, menu by menu:
//
//	dispenser, dropper  dispense behavior per item, and the dropper's
//	                    container-to-container transfer
//	stonecutter         the stonecutting recipe table (one input, many
//	                    outputs)
//	smithing_table      the smithing_transform and smithing_trim
//	                    recipes
//	anvil               the repair and rename rules: the item_name
//	                    component, repair costs, and anvil damage
//	enchanting_table    the enchantment registry, the seeded offer
//	                    rolls, and player experience levels
//	grindstone          the enchantments component, to strip and merge
//	loom                the banner_pattern recipes and banner data
//	cartography_table   the map cloning, zooming, and extension recipes
//	                    and the map data itself
//	lectern             the written_book_content component (page counts)
//	                    and the block-entity broadcast of the stored book
//	beacon              the mob-effect registry, the beam block state,
//	                    and the pyramid scan (see handleSetBeacon)
//	crafter             redstone-driven auto-crafting and the
//	                    block-entity broadcast of the disabled slots
//	brewing_stand       the potion_contents component and the
//	                    potion-brewing mix table (see brewingTick)
var containerSpecs = map[string]*containerSpec{
	"barrel":    {menu: menuGeneric9x3, slots: 27, title: "container.barrel", inputs: slotsUpTo(27)},
	"dispenser": {menu: menuGeneric3x3, slots: 9, title: "container.dispenser", inputs: slotsUpTo(9)},
	"dropper":   {menu: menuGeneric3x3, slots: 9, title: "container.dropper", inputs: slotsUpTo(9)},
	"brewing_stand": {
		menu: menuBrewingStand, slots: 5, title: "container.brewing",
		brew: true, inputs: slotsUpTo(5),
	},
	"stonecutter": {
		menu: menuStonecutter, slots: 2, title: "container.stonecutter",
		inputs: []int{stonecutterInput}, results: []int{stonecutterResult},
	},
	"smithing_table": {
		menu: menuSmithing, slots: 4, title: "container.upgrade",
		inputs:  []int{smithingTemplate, smithingBase, smithingAddition},
		results: []int{smithingResult},
	},
	"anvil": {
		menu: menuAnvil, slots: 3, title: "container.repair",
		inputs: []int{anvilLeft, anvilRight}, results: []int{anvilResult},
	},
	"chipped_anvil": {
		menu: menuAnvil, slots: 3, title: "container.repair",
		inputs: []int{anvilLeft, anvilRight}, results: []int{anvilResult},
	},
	"damaged_anvil": {
		menu: menuAnvil, slots: 3, title: "container.repair",
		inputs: []int{anvilLeft, anvilRight}, results: []int{anvilResult},
	},
	"enchanting_table": {
		menu: menuEnchantment, slots: 2, title: "container.enchant",
		inputs: []int{enchantItem, enchantLapis},
	},
	"grindstone": {
		menu: menuGrindstone, slots: 3, title: "container.grindstone_title",
		inputs: []int{grindstoneTop, grindstoneBottom}, results: []int{grindstoneResult},
	},
	"loom": {
		menu: menuLoom, slots: 4, title: "container.loom",
		inputs: []int{loomBanner, loomDye, loomPattern}, results: []int{loomResult},
	},
	"cartography_table": {
		menu: menuCartographyTable, slots: 3, title: "container.cartography_table",
		inputs: []int{cartographyMap, cartographyAdditional}, results: []int{cartographyResult},
	},
	"lectern": {
		menu: menuLectern, slots: 1, title: "container.lectern", inputs: []int{lecternBook},
	},
	"beacon": {
		menu: menuBeacon, slots: 1, title: "container.beacon", inputs: []int{beaconPayment},
	},
	"crafter": {
		menu: menuCrafter3x3, slots: 9, title: "container.crafter",
		crafter: true, inputs: slotsUpTo(9),
	},
}

// containerSpecFor resolves a block state to its container family.
// Non-default states resolve too, which keeps container detection
// working if a container block ever gains a property (a facing, a
// waterlogged bit) that placement did not spell out.
func containerSpecFor(blk world.Block) (*containerSpec, bool) {
	name, ok := world.StateOwner(blk)
	if !ok {
		return nil, false
	}
	if spec, ok := containerSpecs[name]; ok {
		return spec, true
	}
	if name == "shulker_box" || strings.HasSuffix(name, "_shulker_box") {
		return shulkerSpec, true
	}
	return nil, false
}

// isContainerBlock reports whether a block state opens a generic
// container menu.
func isContainerBlock(blk world.Block) bool {
	_, ok := containerSpecFor(blk)
	return ok
}

// isShulkerBoxBlock reports whether a block state is a shulker box,
// whose item form carries what was stored inside.
func isShulkerBoxBlock(blk world.Block) bool {
	spec, ok := containerSpecFor(blk)
	return ok && spec.shulker
}

// itemNamed reports whether a stack is the named item.
func itemNamed(it ItemStack, name string) bool {
	got, ok := itemNameOf(it.ID)
	return ok && got == name
}

// isPotionItem reports whether a stack is any of the three potion
// items (the brewing stand's potion slots accept all of them).
func isPotionItem(it ItemStack) bool {
	name, ok := itemNameOf(it.ID)
	if !ok {
		return false
	}
	return name == "potion" || name == "splash_potion" || name == "lingering_potion"
}

// potionSlotAccepts reports whether a stack may sit in one of the three
// potion slots. Vanilla's potion slot takes every potion item and an
// empty glass bottle, which is what a brew fills.
func potionSlotAccepts(it ItemStack) bool {
	return isPotionItem(it) || itemNamed(it, "glass_bottle")
}

// brewIngredientItem reports whether a stack can start a brew, vanilla's
// ingredient slot.
func brewIngredientItem(it ItemStack) bool {
	name, ok := itemNameOf(it.ID)
	return ok && brewingIngredientSet[name]
}

// beaconPaymentItems is the vanilla beacon payment tag
// (data/minecraft/tags/item/beacon_payment_items.json in the bundled
// data pack), which is the only thing the beacon's slot accepts.
var beaconPaymentItems = map[string]bool{
	"netherite_ingot": true,
	"emerald":         true,
	"diamond":         true,
	"gold_ingot":      true,
	"iron_ingot":      true,
}

func beaconPaymentItem(it ItemStack) bool {
	name, ok := itemNameOf(it.ID)
	return ok && beaconPaymentItems[name]
}

// containerData is one generic container block entity's storage.
type containerData struct {
	spec  *containerSpec
	slots []ItemStack
	// block is the block name that owns this container and item the
	// block item it drops; a shulker box needs the item to hand a
	// broken box's contents over, and the name is what a reload uses to
	// rebuild the menu spec before any chunk exists.
	block string
	item  int32
	// brewTime counts the brew down to zero and fuel is the blaze
	// powder charges left, the brewing stand's two menu properties.
	brewTime int32
	fuel     int32
	// brewed is the ingredient a running brew started with, so the brew
	// aborts when the player swaps the ingredient, as vanilla does.
	brewed int32
	// lootTable/lootSeed are LootTableBlockEntity's ungenerated-loot
	// fields — a shulker box carries them onto its drop as the
	// container_loot component, and placement puts them back.
	lootTable string
	lootSeed  int64
	// disabled is the crafter's per-slot lock bitmap; triggered is its
	// TRIGGERED latch and craftTicks the pending craft countdown.
	disabled   uint16
	triggered  bool
	craftTicks int32
	// anvilCost is the anvil's current level cost (Container Property
	// 0), recomputed from the inputs on every change; anvilUse is how
	// many items the take consumes from the left and right inputs.
	anvilCost int32
	anvilUse  [2]int32
	// page is the lectern's Page — the book slot's current page,
	// LecternBlockEntity's field and the menu's only property.
	page int32
}

// containerStore keeps generic container state by position.
type containerStore struct {
	mu         sync.Mutex
	containers map[world.Pos]*containerData
}

// ensureContainer creates the storage a container block needs and
// leaves an existing one alone, so reloading a block keeps contents.
func (s *Server) ensureContainer(d *dimension, pos world.Pos, blk world.Block) *containerData {
	spec, ok := containerSpecFor(blk)
	if !ok {
		return nil
	}
	name, _ := world.StateOwner(blk)
	item, _ := world.ItemIDForBlockState(blk)
	d.containers.mu.Lock()
	defer d.containers.mu.Unlock()
	if d.containers.containers == nil {
		d.containers.containers = make(map[world.Pos]*containerData)
	}
	c, ok := d.containers.containers[pos]
	if !ok {
		c = &containerData{spec: spec, slots: make([]ItemStack, spec.slots), block: name, item: item}
		d.containers.containers[pos] = c
	}
	return c
}

// containerAt returns the live storage for a position, creating it from
// the block that is actually there.
func (s *Server) containerAt(d *dimension, pos world.Pos) *containerData {
	return s.ensureContainer(d, pos, d.w.GetBlock(pos))
}

// removeContainer drops a container's contents when its block goes
// away. A shulker box is special: vanilla hands the whole box, contents
// included, back as a single item, so the box item is spawned here with
// its contents attached rather than spilling the slots on the floor.
func (s *Server) removeContainer(d *dimension, pos world.Pos) {
	d.containers.mu.Lock()
	c, ok := d.containers.containers[pos]
	if ok {
		delete(d.containers.containers, pos)
	}
	d.containers.mu.Unlock()
	if !ok {
		return
	}
	if c.spec.shulker {
		s.dropShulkerBox(d, pos, c)
		return
	}
	for i := range c.slots {
		if c.slots[i].Empty() {
			continue
		}
		st := c.slots[i]
		c.slots[i] = ItemStack{}
		s.spawnItemDrop(d.name, pos, st)
	}
}

// dropShulkerBox spawns the broken box as an item carrying what was
// stored inside, vanilla's minecraft:container data component.
func (s *Server) dropShulkerBox(d *dimension, pos world.Pos, c *containerData) {
	if c.item == 0 {
		return
	}
	// Vanilla's ShulkerBoxBlock.playerWillDestroy: be.isEmpty() unpacks
	// an armed LootTable into the container slots first (wiping any
	// pre-existing contents), then collectComponents emits
	// custom_name/container — container_loot is consumed, never on the
	// drop.
	box := new([shulkerSlots]ItemStack)
	if c.lootTable != "" {
		roll := rollLootTable(strings.TrimPrefix(c.lootTable, "minecraft:"), c.lootSeed)
		rng := rand.New(rand.NewSource(c.lootSeed))
		for _, st := range roll {
			if st.Empty() {
				continue
			}
			for tries := 0; tries < shulkerSlots; tries++ {
				slot := rng.Intn(shulkerSlots)
				if box[slot].Empty() {
					box[slot] = st
					break
				}
			}
		}
	} else {
		copy(box[:], c.slots)
	}
	stored := box
	any := false
	for i := range stored {
		if !stored[i].Empty() {
			any = true
			break
		}
	}
	if !any {
		stored = nil
	}
	s.spawnItemDrop(d.name, pos, ItemStack{
		ID:       c.item,
		Count:    1,
		Contents: stored,
		Name:     d.beNames[pos],
	})
}

// openContainer shows one generic container's menu and contents.
func (s *Server) openContainer(sess *Session, pos world.Pos) {
	c := s.containerAt(sess.dimension(), pos)
	if c == nil {
		return
	}
	id := sess.server.nextWindow.Add(1)
	w := &openWindow{id: id, pos: pos, dim: sess.dimension(), container: c, kind: c.spec.menu}
	sess.windowMu.Lock()
	sess.window = w
	sess.windowState = 0
	sess.windowMu.Unlock()

	var b protocol.Buffer
	b.VarInt(id)
	b.VarInt(c.spec.menu)
	b.Raw(menuTitle(c.spec.title))
	_ = sess.write(sess.ids().PlayOpenScreen, b.Bytes())
	s.sendContainerMenuContent(sess)
	if c.spec.brew {
		s.sendBrewProperties(sess)
	}
	if c.spec.menu == menuAnvil {
		s.sendAnvilCost(sess, w, c)
	}
	if c.spec.menu == menuEnchantment {
		s.enchantSlotsChanged(sess, w)
	}
	if c.spec.menu == menuBeacon {
		s.sendBeaconProperties(sess, w)
	}
	if c.spec.menu == menuLectern {
		// sendAllDataToRemote on open: the lectern's one data slot is
		// the book's current page.
		w.dim.containers.mu.Lock()
		pg := c.page
		w.dim.containers.mu.Unlock()
		s.sendContainerProperty(sess, id, 0, pg)
	}
}

// sendContainerMenuContent syncs a whole generic container menu.
func (s *Server) sendContainerMenuContent(sess *Session) {
	sess.windowMu.Lock()
	w := sess.window
	state := sess.windowState
	sess.windowMu.Unlock()
	if w == nil || w.container == nil {
		return
	}
	var b protocol.Buffer
	b.VarInt(w.id)
	b.VarInt(state)
	b.VarInt(int32(w.menuSize()))
	for i := 0; i < w.menuSize(); i++ {
		writeSlot(&b, sess.menuStack(i))
	}
	writeSlot(&b, sess.carried)
	_ = sess.write(sess.ids().PlayContainerContent, b.Bytes())
}

// sendBrewProperties syncs the brewing stand's brew time and fuel.
func (s *Server) sendBrewProperties(sess *Session) {
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.container == nil || !w.container.spec.brew {
		return
	}
	c := w.container
	w.dim.containers.mu.Lock()
	props := [2]int32{c.brewTime, c.fuel}
	w.dim.containers.mu.Unlock()
	for i, v := range props {
		s.sendContainerProperty(sess, w.id, int16(i), v)
	}
}

// sendContainerProperty writes one Container Property value.
func (s *Server) sendContainerProperty(sess *Session, window int32, prop int16, value int32) {
	var b protocol.Buffer
	b.VarInt(window)
	b.Short(prop)
	b.Short(int16(value))
	_ = sess.write(sess.ids().PlayContainerProperty, b.Bytes())
}

// handleContainerClick implements click modes 0 (pickup/place), 1
// (quick move), 2 (hotbar swap), and 4 (throw) for a generic container
// menu. Result slots are take-only; a click on one runs the menu's
// specialized action, and everything else behaves like storage.
func (s *Server) handleContainerClick(sess *Session, w *openWindow, c *containerData, slot int16, button byte, mode int32) {
	if slot < 0 || int(slot) >= w.menuSize() {
		return
	}
	containerSide := int(slot) < c.spec.slots
	isResult := containerSide && c.spec.isResult(int(slot))

	// The brewing stand's potion slots fire brewed_potion in onTake:
	// any click that removes a potion-bearing stack counts, whatever
	// the mode. Snapshot the three slots so the post-click diff can
	// see what left.
	var brewTaken [3]ItemStack
	if c.spec.brew {
		for i := brewPotionFirst; i <= brewPotionLast; i++ {
			brewTaken[i] = sess.menuStack(i)
		}
	}

	sess.invMu.RLock()
	cur := sess.carried
	sess.invMu.RUnlock()

	var touched []int
	switch mode {
	case 0:
		if isResult {
			// Result slots are take-only; producing a result needs the
			// menu's own recipe data (see containerSpecs).
			if cur.Empty() {
				if taken := sess.menuStack(int(slot)); !taken.Empty() {
					sess.setMenuStack(int(slot), ItemStack{})
					s.setCarriedLocked(sess, taken)
					touched = append(touched, int(slot))
				}
			}
			break
		}
		target := sess.menuStack(int(slot))
		switch {
		case button != 0:
			cur, target = clickRight(cur, target, c.spec, int(slot))
		default:
			cur, target = clickLeft(cur, target, c.spec, int(slot))
		}
		sess.setMenuStack(int(slot), target)
		s.setCarriedLocked(sess, cur)
		touched = append(touched, int(slot))
	case 1:
		moving := sess.menuStack(int(slot))
		if moving.Empty() {
			break
		}
		touched = append(touched, int(slot))
		if containerSide {
			quickMoveIntoPlayer(sess, c, &moving, &touched)
		} else {
			quickMoveIntoContainer(sess, c, &moving, &touched)
		}
		sess.setMenuStack(int(slot), moving)
	case 2:
		swap := w.menuSize() - 9 + int(button)
		if isResult || swap < w.menuSize()-9 || swap >= w.menuSize() {
			break
		}
		a := sess.menuStack(int(slot))
		b := sess.menuStack(swap)
		// doClick's slot.mayPlace gate: a swap into a slot that
		// refuses the incoming stack does not happen.
		if !menuAccepts(c.spec, int(slot), b) {
			break
		}
		sess.setMenuStack(int(slot), b)
		sess.setMenuStack(swap, a)
		touched = append(touched, int(slot), swap)
	case 3:
		// Creative middle-click clone: a full stack onto the cursor,
		// the slot unchanged. A clone carries the slot's update so the
		// client stays live; anything else resyncs like an
		// unsupported mode instead of diverging.
		if out, ok := creativeClone(sess, sess.menuStack(int(slot)), button); ok {
			s.setCarriedLocked(sess, out)
			touched = append(touched, int(slot))
			break
		}
		s.sendContainerMenuContent(sess)
		return
	case 4:
		it := sess.menuStack(int(slot))
		if it.Empty() {
			break
		}
		drop := it
		if button == 0 {
			drop = it.withCount(1)
		}
		it.Count -= drop.Count
		if it.Count <= 0 {
			it = ItemStack{}
		}
		sess.setMenuStack(int(slot), it)
		s.dropFromMenu(sess, drop)
		touched = append(touched, int(slot))
	default:
		// Unsupported mode: resync the menu instead of diverging.
		s.sendContainerMenuContent(sess)
		return
	}
	s.syncContainerMenuSlots(sess, w, touched)
	if c.spec.brew {
		for i := brewPotionFirst; i <= brewPotionLast; i++ {
			old := brewTaken[i]
			if old.Empty() || old.Potion == "" {
				continue
			}
			now := sess.menuStack(i)
			if !now.Stackable(old) || now.Count < old.Count {
				s.fireBrewedPotion(sess, old)
			}
		}
		s.updateBrewBlockState(w.dim, w.pos, brewPotionBits(c))
	}
	if c.spec.menu == menuEnchantment {
		s.enchantSlotsChanged(sess, w)
	}
	if c.spec.menu == menuLectern {
		s.lecternMenuBookCheck(w)
	}
}

// isResult reports whether a container slot is take-only.
func (spec *containerSpec) isResult(slot int) bool {
	for _, r := range spec.results {
		if r == slot {
			return true
		}
	}
	return false
}

// menuAccepts reports whether a hand click may place the stack into
// the menu slot: the spec's filters (result slots, brewing stands,
// beacons) guard the container side, while the player side (main
// inventory and hotbar) always accepts. accepts() alone refuses any
// slot past the container, which stranded the cursor when parking a
// repaired pick into the hotbar (TestAnvilParity ch-park38).
func menuAccepts(spec *containerSpec, slot int, it ItemStack) bool {
	if spec == nil || slot >= spec.slots {
		return true
	}
	return spec.accepts(slot, it)
}

// clickLeft applies a left click to a menu slot: pick the whole stack
// up, drop the whole stack in, merge, or swap.
func clickLeft(cur, target ItemStack, spec *containerSpec, slot int) (carried, placed ItemStack) {
	switch {
	case cur.Empty():
		return target, ItemStack{}
	case target.Empty():
		if !menuAccepts(spec, slot, cur) {
			return cur, target
		}
		move := cur.Count
		if max := spec.slotMax(slot, cur); move > max {
			move = max
		}
		placed := cur.withCount(move)
		cur.Count -= move
		if cur.Count <= 0 {
			cur = ItemStack{}
		}
		return cur, placed
	case target.Stackable(cur):
		room := spec.slotMax(slot, target) - target.Count
		move := cur.Count
		if move > room {
			move = room
		}
		target.Count += move
		cur.Count -= move
		if cur.Count <= 0 {
			cur = ItemStack{}
		}
		return cur, target
	case !menuAccepts(spec, slot, cur):
		// A typed slot (lapis, blaze powder, a beacon payment) refuses
		// the swap rather than taking the wrong item.
		return cur, target
	default:
		return target, cur
	}
}

// clickRight applies a right click: split the slot onto the cursor, or
// place one item from the cursor — the deposit arm obeys the slot's
// mayPlace the way vanilla's doClick does.
func clickRight(cur, target ItemStack, spec *containerSpec, slot int) (carried, placed ItemStack) {
	switch {
	case cur.Empty():
		if target.Empty() {
			return cur, target
		}
		half := (target.Count + 1) / 2
		carried = target.withCount(half)
		target.Count -= half
		if target.Count <= 0 {
			target = ItemStack{}
		}
		return carried, target
	case target.Empty():
		if !menuAccepts(spec, slot, cur) {
			return cur, target
		}
		target = cur.withCount(1)
		cur.Count--
		if cur.Count <= 0 {
			cur = ItemStack{}
		}
		return cur, target
	case target.Stackable(cur) && target.Count < maxStackOfID(target.ID):
		if !menuAccepts(spec, slot, cur) {
			return cur, target
		}
		target.Count++
		cur.Count--
		if cur.Count <= 0 {
			cur = ItemStack{}
		}
		return cur, target
	}
	return cur, target
}

// quickMoveIntoPlayer moves a container stack into the player side,
// merging first and then filling empty slots, both backward from the
// hotbar's last slot. Measured on 26.2.
func quickMoveIntoPlayer(sess *Session, c *containerData, moving *ItemStack, touched *[]int) {
	// Vanilla fills the player side backward: hotbar last slot first,
	// then the main rows backward. Measured on 26.2.
	for i := c.spec.slots + 35; i >= c.spec.slots && !moving.Empty(); i-- {
		other := sess.menuStack(i)
		if !other.Stackable(*moving) {
			continue
		}
		room := maxStackOfID(other.ID) - other.Count
		move := moving.Count
		if move > room {
			move = room
		}
		other.Count += move
		moving.Count -= move
		sess.setMenuStack(i, other)
		*touched = append(*touched, i)
	}
	for i := c.spec.slots + 35; i >= c.spec.slots && !moving.Empty(); i-- {
		if !sess.menuStack(i).Empty() {
			continue
		}
		placeCapped(sess, c.spec, i, moving)
		*touched = append(*touched, i)
	}
}

// quickMoveIntoContainer moves a player stack into the container slots
// the spec allows, merging first and then filling empty slots.
func quickMoveIntoContainer(sess *Session, c *containerData, moving *ItemStack, touched *[]int) {
	dests := c.spec.dests(*moving)
	for _, i := range dests {
		if moving.Empty() {
			break
		}
		if !c.spec.accepts(i, *moving) {
			continue
		}
		other := sess.menuStack(i)
		if !other.Stackable(*moving) {
			continue
		}
		room := c.spec.slotMax(i, *moving) - other.Count
		move := moving.Count
		if move > room {
			move = room
		}
		other.Count += move
		moving.Count -= move
		sess.setMenuStack(i, other)
		*touched = append(*touched, i)
	}
	for _, i := range dests {
		if moving.Empty() {
			break
		}
		if !c.spec.accepts(i, *moving) || !sess.menuStack(i).Empty() {
			continue
		}
		placeCapped(sess, c.spec, i, moving)
		*touched = append(*touched, i)
	}
}

// placeCapped moves a quick-move remainder into an empty menu slot, one
// capped stack at a time: 64 pickaxes fill 64 slots of one, the way the
// real server's shift-click spreads what a slot cannot take.
func placeCapped(sess *Session, spec *containerSpec, i int, moving *ItemStack) {
	put := moving.Count
	if limit := spec.slotMax(i, *moving); put > limit {
		put = limit
	}
	sess.setMenuStack(i, moving.withCount(put))
	moving.Count -= put
	if moving.Count <= 0 {
		*moving = ItemStack{}
	}
}

// setCarriedLocked stores the cursor stack.
func (s *Server) setCarriedLocked(sess *Session, it ItemStack) {
	sess.invMu.Lock()
	sess.carried = it
	sess.invMu.Unlock()
}

// dropFromMenu spawns the thrown stack as an item entity tossed in
// the player's look direction, vanilla's throw click: the thrower's
// item starts at eye level, flies forward, and settles about a block
// and a half ahead — far enough that a stationary player does not
// immediately collect it again. The entity gets vanilla's thrown-item
// pickup delay of 40 ticks.
func (s *Server) dropFromMenu(sess *Session, it ItemStack) {
	if it.Empty() {
		return
	}
	x, y, z, yaw, pitch, _ := sess.Pos()
	d := sess.dimension()
	yr := float64(yaw) * math.Pi / 180
	pr := float64(pitch) * math.Pi / 180
	dx := -math.Sin(yr) * math.Cos(pr)
	dz := math.Cos(yr) * math.Cos(pr)
	pos := world.Pos{
		X: int(math.Floor(x + dx*1.7)),
		Y: int(math.Floor(y + 1.4)),
		Z: int(math.Floor(z + dz*1.7)),
	}
	e := s.spawnItemDrop(d.name, pos, it)
	if e != nil {
		e.PickupDelay = 40
	}
}

// syncContainerMenuSlots sends Container Slot updates for touched menu
// slots and the cursor.
func (s *Server) syncContainerMenuSlots(sess *Session, w *openWindow, touched []int) {
	sess.windowMu.Lock()
	sess.windowState++
	state := sess.windowState
	sess.windowMu.Unlock()
	seen := map[int]bool{}
	for _, slot := range touched {
		if seen[slot] {
			continue
		}
		seen[slot] = true
		var b protocol.Buffer
		b.VarInt(w.id)
		b.VarInt(state)
		b.Short(int16(slot))
		writeSlot(&b, sess.menuStack(slot))
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
	}
	_ = s.sendCarriedItem(sess, sess.carriedBuffer())
}

// handleContainerClickPacket parses and applies a click on a generic
// container menu.
func (s *Server) handleContainerClickPacket(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	win, err := r.VarInt()
	if err != nil {
		return
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || (w.container == nil && w.merchant == nil && w.mount == nil) || w.id != win {
		return
	}
	st, err := r.VarInt() // state id
	if err != nil {
		return
	}
	if !s.checkClickState(sess, w, st) {
		return
	}
	slot, err := r.Short()
	if err != nil {
		return
	}
	button, err := r.Byte()
	if err != nil {
		return
	}
	mode, err := r.VarInt()
	if err != nil {
		return
	}
	readChangedSlots(r)
	if _, err := readSlot(r); err != nil { // carried item
		return
	}
	if w.merchant != nil {
		s.handleMerchantClick(sess, w, slot, button, mode)
		return
	}
	if w.mount != nil {
		s.handleMountClick(sess, w, slot, button, mode)
		return
	}
	if openWindowKindAnvil(w) {
		s.handleAnvilClick(sess, w, w.container, slot, button, mode)
		return
	}
	s.handleContainerClick(sess, w, w.container, slot, button, mode)
}

// openBlockMenu opens the menu a clicked block owns, reporting whether
// the interaction was consumed. Signs are not menus and stay in the
// placement path.
func (s *Server) openBlockMenu(sess *Session, blk world.Block, pos world.Pos) bool {
	switch {
	case isChestBlock(blk):
		s.openChest(sess, pos)
	case isEnderChestBlock(blk):
		s.openEnderChest(sess, pos)
	case isHopperBlock(blk):
		s.openHopper(sess, pos)
	case isCraftingTableBlock(blk):
		s.openCraftingTable(sess, pos)
	case isFurnaceBlock(blk):
		s.openFurnace(sess, pos)
	case isContainerBlock(blk):
		// A lectern with no book never reaches here — its useItemOn
		// consumes the click first (see lecternUse). The slot check
		// guards the case where has_book and the stored book disagree.
		if name, _ := world.StateOwner(blk); name == "lectern" {
			c := s.containerAt(sess.dimension(), pos)
			if c == nil || c.slots[lecternBook].Empty() {
				return false
			}
		}
		s.openContainer(sess, pos)
	default:
		return false
	}
	return true
}

// --- brewing stand -------------------------------------------------

// vanilla's brew duration in ticks, the value the brew-time property
// counts down from, and the number of brews one blaze powder powers.
const (
	brewingStandTicks    = 400
	brewingFuelPerPowder = 20
)

// brewingIngredientSet is the ingredient slot's accept set, generated
// from the mix table — every item that is an ingredient in at least
// one potion or container mix.
var brewingIngredientSet = func() map[string]bool {
	m := make(map[string]bool, len(data.BrewingIngredientItems))
	for _, n := range data.BrewingIngredientItems {
		m[n] = true
	}
	return m
}()

// brewMixResult applies one slot's mix for an ingredient: the
// container mix first (potion->splash_potion->lingering_potion),
// then the potion mix on the (new) stack's potion_contents. The
// stack is unchanged when neither matches, which is also how
// PotionBrewing.mix leaves it.
func brewMixResult(ingredient string, it ItemStack) ItemStack {
	name, ok := itemNameOf(it.ID)
	if !ok {
		return it
	}
	if to, ok := data.ContainerMixes[[2]string{name, ingredient}]; ok {
		it.ID = world.ItemIDs["minecraft:"+to]
		name = to
	}
	if it.Potion != "" {
		if to, ok := data.PotionMixes[[2]string{it.Potion, ingredient}]; ok {
			it.Potion = to
		}
	}
	return it
}

// brewHasMix reports whether the ingredient can brew into this stack:
// a container mix (item type change) or a potion mix (potion_contents
// change). Potions only live in container items, so anything else
// never mixes — vanilla's isBrewable checks the same pair.
func brewHasMix(ingredient string, it ItemStack) bool {
	name, ok := itemNameOf(it.ID)
	if !ok || it.Empty() || !isPotionItem(it) {
		return false
	}
	_, pm := data.PotionMixes[[2]string{it.Potion, ingredient}]
	_, cm := data.ContainerMixes[[2]string{name, ingredient}]
	return pm || cm
}

// brewable reports whether the stand has an ingredient that can brew
// into at least one of the potion slots.
func brewable(c *containerData, ingredient ItemStack) bool {
	if ingredient.Empty() || !brewIngredientItem(ingredient) {
		return false
	}
	name, _ := itemNameOf(ingredient.ID)
	for i := brewPotionFirst; i <= brewPotionLast; i++ {
		if brewHasMix(name, c.slots[i]) {
			return true
		}
	}
	return false
}

// containerStillValid mirrors AbstractContainerMenu.stillValid: the
// menu's packets are honored only while the player stays within 8
// blocks of the block the menu opened on (measured from the player's
// feet, not eye height).
func containerStillValid(sess *Session, pos world.Pos) bool {
	x, y, z, _, _, _ := sess.Pos()
	dx := x - (float64(pos.X) + 0.5)
	dy := y - (float64(pos.Y) + 0.5)
	dz := z - (float64(pos.Z) + 0.5)
	return dx*dx+dy*dy+dz*dz <= 64
}

// containerTickStep advances every brewing stand one tick, in every
// dimension.
func (s *Server) containerTickStep() {
	for _, d := range s.dims {
		d.containers.mu.Lock()
		type entry struct {
			pos world.Pos
			c   *containerData
		}
		var brewing []entry
		for pos, c := range d.containers.containers {
			if c.spec.brew {
				brewing = append(brewing, entry{pos, c})
			}
		}
		d.containers.mu.Unlock()
		for _, e := range brewing {
			s.brewingTick(d, e.pos, e.c)
		}
	}
}

// brewingTick advances one brewing stand with vanilla's rules: fuel is
// topped up from a blaze powder whenever it runs out, a brew costs one
// fuel charge up front and 400 ticks of brew time, and the ingredient
// is consumed when the brew finishes.
func (s *Server) brewingTick(d *dimension, pos world.Pos, c *containerData) {
	d.containers.mu.Lock()
	before := append([]ItemStack(nil), c.slots...)
	changed := false
	if c.fuel <= 0 {
		if f := c.slots[brewFuelSlot]; !f.Empty() && itemNamed(f, "blaze_powder") {
			f.Count--
			if f.Count <= 0 {
				f = ItemStack{}
			}
			c.slots[brewFuelSlot] = f
			c.fuel = brewingFuelPerPowder
			changed = true
		}
	}
	ingredient := c.slots[brewIngredient]
	canBrew := brewable(c, ingredient)
	brewDone := false
	var remainder ItemStack
	remainderDrop := false
	switch {
	case c.brewTime > 0:
		c.brewTime--
		changed = true
		switch {
		case c.brewTime == 0 && canBrew:
			// doBrew: apply the ingredient's mixes to each potion
			// slot (container mix first, then the potion mix), then
			// shrink the ingredient — a remainder replaces the slot
			// when it emptied, otherwise it drops at the stand.
			ingName, _ := itemNameOf(ingredient.ID)
			for i := brewPotionFirst; i <= brewPotionLast; i++ {
				c.slots[i] = brewMixResult(ingName, c.slots[i])
			}
			ingredient.Count--
			if ingredient.Count <= 0 {
				ingredient = ItemStack{}
			}
			if rem := brewRemainder(ingName); rem != "" {
				remainder = ItemStack{ID: world.ItemIDs["minecraft:"+rem], Count: 1}
				remainderDrop = !ingredient.Empty()
				if ingredient.Empty() {
					ingredient = remainder
					remainderDrop = false
				}
			}
			c.slots[brewIngredient] = ingredient
			c.brewed = 0
			brewDone = true
		case !canBrew || ingredient.ID != c.brewed:
			c.brewTime = 0
			c.brewed = 0
		}
	case canBrew && c.fuel > 0:
		c.fuel--
		c.brewTime = brewingStandTicks
		c.brewed = ingredient.ID
		changed = true
	}
	props := [2]int32{c.brewTime, c.fuel}
	slots := append([]ItemStack(nil), c.slots...)
	var touched []int
	for i := range slots {
		if !slots[i].Stackable(before[i]) || slots[i].Count != before[i].Count {
			touched = append(touched, i)
		}
	}
	potionBits := brewPotionBits(c)
	d.containers.mu.Unlock()
	if brewDone {
		s.brewFinished(d, pos, slots, remainderDrop, remainder)
	}
	if len(touched) > 0 {
		s.rsContainerChanged(d, pos)
	}
	if bitsChanged := s.updateBrewBlockState(d, pos, potionBits); bitsChanged {
		changed = true
	}
	if !changed {
		return
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		sess.windowMu.Lock()
		w := sess.window
		sess.windowMu.Unlock()
		if w == nil || w.container != c || !c.spec.brew {
			return true
		}
		for i, val := range props {
			s.sendContainerProperty(sess, w.id, int16(i), val)
		}
		for _, i := range touched {
			var b protocol.Buffer
			b.VarInt(w.id)
			b.VarInt(sess.windowState)
			b.Short(int16(i))
			writeSlot(&b, slots[i])
			_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
		}
		return true
	})
}

// brewRemainder is the crafting-remainder item an ingredient leaves
// behind: only dragon's breath hands back its bottle.
func brewRemainder(ingredient string) string {
	if ingredient == "dragon_breath" {
		return "glass_bottle"
	}
	return ""
}

// brewPotionBits returns which of the three potion slots hold a
// stack — the has_bottle_0..2 block properties vanilla mirrors.
func brewPotionBits(c *containerData) (bits [3]bool) {
	for i := brewPotionFirst; i <= brewPotionLast; i++ {
		bits[i] = !c.slots[i].Empty()
	}
	return
}

// brewingStandBaseState is the all-empty brewing stand block state;
// has_bottle_0..2 shift it up (b0 is the high bit — measured against
// vanilla/reports/blocks.json's state list).
const brewingStandBaseState = world.Block(9459)

// updateBrewBlockState mirrors the potion-slot occupancy into the
// stand's has_bottle_0..2 states, broadcasting a Block Update when it
// changes. Returns whether the block changed.
func (s *Server) updateBrewBlockState(d *dimension, pos world.Pos, bits [3]bool) bool {
	cur := d.w.GetBlock(pos)
	name, ok := world.StateOwner(cur)
	if !ok || name != "brewing_stand" {
		return false
	}
	// bits are the has_bottle_0..2 flags: 9452 all true, 9459 all
	// false, each bottle dropping the high/mid/low bit in that order.
	want := brewingStandBaseState
	if bits[0] {
		want -= 4
	}
	if bits[1] {
		want -= 2
	}
	if bits[2] {
		want--
	}
	if cur == want {
		return false
	}
	d.w.SetBlock(pos, want)
	s.broadcastBlockState(d, pos, want)
	return true
}

// brewFinished is the post-doBrew fan-out: level event 1035 (the brew
// sound+particles) at the stand, and the ingredient remainder drop
// when the slot stayed occupied.
func (s *Server) brewFinished(d *dimension, pos world.Pos, slots []ItemStack, dropRem bool, rem ItemStack) {
	var b protocol.Buffer
	b.Int(1035)
	b.Position(int32(pos.X), int32(pos.Y), int32(pos.Z))
	b.Int(0)
	b.Bool(false)
	body := b.Bytes()
	cx, cz := pos.X>>4, pos.Z>>4
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dimName() != d.name || !sess.hasChunk(cx, cz) {
			return true
		}
		_ = sess.write(sess.ids().PlayLevelEvent, body)
		return true
	})
	if dropRem {
		s.spawnItemDrop(d.name, pos, rem)
	}
	_ = slots // slots already resynced by the caller's broadcast
}
