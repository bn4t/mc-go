package server

import (
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// Enchantment table menu (measured on 26.2 EnchantmentMenu):
//
// Open sends all ten Container Property slots: costs[0..2], the menu
// seed (the player's enchantment seed — 0 until the first enchant
// rolls it), enchant clues[0..2] and level clues[0..2] (-1 for none).
// Every slotsChanged recomputes the three offers; the props that
// changed broadcast. A button click on index i costs i+1 levels and
// i+1 lapis when the player has level >= i+1 and >= costs[i] (or is
// creative), enchants slot 0's stack with the same seed+i-rolled list
// the clue previewed, reseeds the player, and slotsChanged again.
const (
	enchPropCost0  = 0
	enchPropSeed   = 3
	enchPropClue0  = 4
	enchPropLevel0 = 7
	enchPropCount  = 10
)

// enchantMenuState is one open enchantment menu's offer state. It lives
// on openWindow because the rolls depend on the viewing player's
// enchantment seed — a shared container would leak one player's seed
// into another's offers.
type enchantMenuState struct {
	seed    int32                // DataSlot 3; the player's enchantment seed
	costs   [3]int32             // props 0-2: level requirement per offer
	clues   [3]int32             // props 4-6: first enchant's registry id, -1 none
	levels  [3]int32             // props 7-9: that enchant's level, -1 none
	sent    [enchPropCount]int32 // last broadcast per property
	sentAny bool
}

// bookshelfOffsets mirrors vanilla's BOOKSHELF_OFFSETS:
// BlockPos.betweenClosedStream(-2,0,-2, 2,1,2) — every cell of the
// 5x2x5 volume (50 candidates, interior cells included).
func bookshelfOffsets() []world.Pos {
	var out []world.Pos
	for x := -2; x <= 2; x++ {
		for y := 0; y <= 1; y++ {
			for z := -2; z <= 2; z++ {
				out = append(out, world.Pos{X: x, Y: y, Z: z})
			}
		}
	}
	return out
}

// enchantPower counts the usable bookshelves around pos: a block in the
// enchantment_power_provider tag at the offset with an
// enchantment_power_transmitter block halfway to it.
func enchantPower(d *dimension, pos world.Pos) int {
	providers := map[string]bool{}
	for _, n := range data.BlockTags["enchantment_power_provider"] {
		providers[n] = true
	}
	transmitters := map[string]bool{}
	for _, n := range data.BlockTags["enchantment_power_transmitter"] {
		transmitters[n] = true
	}
	power := 0
	for _, off := range bookshelfOffsets() {
		at := world.Pos{X: pos.X + off.X, Y: pos.Y + off.Y, Z: pos.Z + off.Z}
		name, ok := world.StateOwner(d.w.GetBlock(at))
		if !ok || !providers[name] {
			continue
		}
		// vanilla offsets the transmitter check by (off.X/2, off.Y,
		// off.Z/2) — the Y component is NOT halved.
		half := world.Pos{X: pos.X + off.X/2, Y: pos.Y + off.Y, Z: pos.Z + off.Z/2}
		mid, ok := world.StateOwner(d.w.GetBlock(half))
		if ok && transmitters[mid] {
			power++
		}
	}
	return power
}

// enchantable is the item's minecraft:enchantable component value,
// zero when the item cannot take a table enchantment.
func enchantableOf(it ItemStack) int {
	name, ok := itemNameOf(it.ID)
	if !ok {
		return 0
	}
	return data.ItemEnchantable[name]
}

// enchantCost mirrors EnchantmentHelper.getEnchantmentCost: the level
// an option asks for.
func enchantCost(r *javaRand, slot, power, enchantable int) int {
	if enchantable <= 0 {
		return 0
	}
	if power > 15 {
		power = 15
	}
	j := int(r.nextIntN(8)) + 1 + (power >> 1) + int(r.nextIntN(int32(power+1)))
	switch slot {
	case 0:
		return max(j/3, 1)
	case 1:
		return j*2/3 + 1
	default:
		return max(j, power*2)
	}
}

type enchantInstance struct {
	name  string
	level int
}

// enchantMinCost/enchantMaxCost are vanilla's LevelBasedValue costs:
// base + per_level_above_first*(level-1).
func enchantMinCost(e data.EnchantmentMeta, level int) int {
	return e.MinBase + e.MinPerLvl*(level-1)
}
func enchantMaxCost(e data.EnchantmentMeta, level int) int {
	return e.MaxBase + e.MaxPerLvl*(level-1)
}

// inTableEnchants is the in_enchanting_table member list in DECLARED
// tag order — the iteration order vanilla's HolderSet stream walks
// (and the candidate order behind every weighted pick).
var inTableEnchants = data.InEnchantingTableOrder

// enchantsCompatible mirrors Enchantment.areCompatible: different
// enchantments, neither contained in the other's exclusive set.
func enchantsCompatible(a, b string) bool {
	if a == b {
		return false
	}
	for _, m := range data.ExclusiveSets[data.Enchantments[a].Exclusive] {
		if m == b {
			return false
		}
	}
	for _, m := range data.ExclusiveSets[data.Enchantments[b].Exclusive] {
		if m == a {
			return false
		}
	}
	return true
}

// itemNameOnly strips the namespace for the generated item sets.
func itemIsMember(list []string, name string) bool {
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}

// enchantAvailable mirrors getAvailableEnchantmentResults: in-table
// enchantments where the item is primary (books accept all), each at
// the highest level whose cost range straddles the offer level.
func enchantAvailable(level int, itemName string) []enchantInstance {
	isBook := itemName == "book"
	var out []enchantInstance
	for _, name := range inTableEnchants {
		meta := data.Enchantments[name]
		if !isBook && !itemIsMember(meta.Primary, itemName) {
			continue
		}
		for lvl := meta.MaxLevel; lvl >= 1; lvl-- {
			if enchantMinCost(meta, lvl) <= level && level <= enchantMaxCost(meta, lvl) {
				out = append(out, enchantInstance{name, lvl})
				break
			}
		}
	}
	return out
}

// enchantPick is WeightedRandom.getRandomItem over the candidates.
func enchantPick(r *javaRand, cands []enchantInstance) (enchantInstance, bool) {
	total := 0
	for _, c := range cands {
		total += data.Enchantments[c.name].Weight
	}
	if total <= 0 {
		return enchantInstance{}, false
	}
	roll := r.nextIntN(int32(total))
	for _, c := range cands {
		roll -= int32(data.Enchantments[c.name].Weight)
		if roll < 0 {
			return c, true
		}
	}
	return cands[len(cands)-1], true
}

// selectEnchantment mirrors EnchantmentHelper.selectEnchantment: the
// level is bumped by the item's enchantability and jittered ±15%, the
// candidate pool is computed once, then weighted picks repeat while a
// halving coin keeps landing.
func selectEnchantment(r *javaRand, it ItemStack, level int) []enchantInstance {
	enchVal := enchantableOf(it)
	if enchVal <= 0 {
		return nil
	}
	name, _ := itemNameOf(it.ID)
	e4 := int32(enchVal)/4 + 1
	level += 1 + int(r.nextIntN(e4)) + int(r.nextIntN(e4))
	f := (r.nextFloat() + r.nextFloat() - 1) * 0.15
	level = max(1, int(float64(float32(level)*(1+f)+0.5))) // Math.round
	cands := enchantAvailable(level, name)
	var out []enchantInstance
	if len(cands) == 0 {
		return out
	}
	if inst, ok := enchantPick(r, cands); ok {
		out = append(out, inst)
	}
	for r.nextIntN(50) <= int32(level) {
		if len(cands) == 0 {
			break
		}
		var keep []enchantInstance
		last := out[len(out)-1]
		for _, c := range cands {
			if enchantsCompatible(last.name, c.name) {
				keep = append(keep, c)
			}
		}
		cands = keep
		if len(cands) == 0 {
			break
		}
		if inst, ok := enchantPick(r, cands); ok {
			out = append(out, inst)
		}
		level /= 2
	}
	return out
}

// enchantOfferList mirrors EnchantmentMenu.getEnchantmentList: a fresh
// seed of playerSeed+slot drives the selection, and a book sheds one
// random pick when it rolled more than one.
func (m *enchantMenuState) enchantOfferList(it ItemStack, slot int, cost int) ([]enchantInstance, *javaRand) {
	r := newJavaRand(int64(m.seed) + int64(slot))
	list := selectEnchantment(r, it, cost)
	if name, _ := itemNameOf(it.ID); name == "book" && len(list) > 1 {
		i := r.nextIntN(int32(len(list)))
		list = append(list[:i], list[i+1:]...)
	}
	return list, r
}

// enchantApply merges a rolled list into a stack: books transmute to
// enchanted_book holding stored_enchantments; anything else merges into
// the enchantments component (upgrade semantics: same enchant takes the
// higher level).
func enchantApply(it ItemStack, list []enchantInstance) ItemStack {
	name, _ := itemNameOf(it.ID)
	if name == "book" {
		it.ID = world.ItemIDs["enchanted_book"]
		it.StoredEnch = mergeEnch(it.StoredEnch, list)
		return it
	}
	it.Ench = mergeEnch(it.Ench, list)
	return it
}

// mergeEnch folds "name:lvl" pairs into the canonical sorted string.
func mergeEnch(cur string, list []enchantInstance) string {
	m := map[string]int{}
	for _, e := range strings.Split(cur, ",") {
		if n, lv, ok := strings.Cut(e, ":"); ok && n != "" {
			if v, err := strconv.Atoi(lv); err == nil {
				m[n] = v
			}
		}
	}
	for _, inst := range list {
		if inst.level > m[inst.name] || m[inst.name] == 0 {
			m[inst.name] = inst.level
		}
	}
	var names []string
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var parts []string
	for _, n := range names {
		parts = append(parts, n+":"+strconv.Itoa(m[n]))
	}
	return strings.Join(parts, ",")
}

// enchantSlotsChanged mirrors EnchantmentMenu.slotsChanged: recompute
// the three offers from the player's seed and broadcast the props that
// changed.
func (s *Server) enchantSlotsChanged(sess *Session, w *openWindow) {
	c := w.container
	if c == nil || c.spec == nil || c.spec.menu != menuEnchantment {
		return
	}
	if w.ench == nil {
		w.ench = &enchantMenuState{seed: sess.enchantSeed}
	}
	m := w.ench
	item := sess.menuStack(enchantItem)
	power := enchantPower(w.dim, w.pos)
	r := newJavaRand(int64(m.seed))
	var costs, clues, levels [3]int32
	for i := 0; i < 3; i++ {
		costs[i] = int32(enchantCost(r, i, power, enchantableOf(item)))
		clues[i] = -1
		levels[i] = -1
		if costs[i] < int32(i+1) {
			costs[i] = 0
		}
	}
	for i := 0; i < 3; i++ {
		if costs[i] <= 0 {
			continue
		}
		list, rr := m.enchantOfferList(item, i, int(costs[i]))
		if len(list) == 0 {
			continue
		}
		pick := list[rr.nextIntN(int32(len(list)))]
		clues[i] = registryEntryID("minecraft:enchantment", "minecraft:"+pick.name)
		levels[i] = int32(pick.level)
	}
	m.costs, m.clues, m.levels = costs, clues, levels
	props := [enchPropCount]int32{
		costs[0], costs[1], costs[2], m.seed,
		clues[0], clues[1], clues[2],
		levels[0], levels[1], levels[2],
	}
	// The gameplay offer retains canonical enchantment handles; the three
	// displayed clues are holder IDs from this viewer's synced registry.
	profile, err := sess.javaProfile()
	if err != nil {
		sess.closeQueue()
		return
	}
	for i := 4; i <= 6; i++ {
		if props[i] < 0 {
			continue
		}
		props[i], err = profile.projectRegistryID("minecraft:enchantment", props[i])
		if err != nil {
			sess.closeQueue()
			return
		}
	}
	for i, v := range props {
		if !m.sentAny || m.sent[i] != v {
			s.sendContainerProperty(sess, w.id, int16(i), v)
		}
	}
	m.sent = props
	m.sentAny = true
}

// enchantButton mirrors EnchantmentMenu.clickMenuButton for option i:
// it needs i+1 lapis (unless creative), xp level >= i+1 and >= the
// displayed cost, and a non-empty item slot; on success it enchants,
// consumes lapis and i+1 levels, rolls the player's new enchantment
// seed, re-syncs the offers, fires enchanted_item, and plays the table
// sound.
func (s *Server) enchantButton(sess *Session, w *openWindow, btn int32) {
	c := w.container
	if c == nil || c.spec == nil || c.spec.menu != menuEnchantment || w.ench == nil {
		return
	}
	if btn < 0 || btn > 2 {
		return
	}
	m := w.ench
	cost := int(btn) + 1
	item := sess.menuStack(enchantItem)
	lapis := sess.menuStack(enchantLapis)
	creative := sess.mode() == GameModeCreative
	if int(lapis.Count) < cost && !creative {
		return
	}
	if m.costs[btn] <= 0 || item.Empty() {
		return
	}
	if !creative && (int(sess.xpLevel) < cost || sess.xpLevel < m.costs[btn]) {
		return
	}
	list, _ := m.enchantOfferList(item, int(btn), int(m.costs[btn]))
	if len(list) == 0 {
		return
	}
	// onEnchantmentPerformed: deduct i+1 levels; a negative clamp also
	// zeroes progress and total, then reroll the seed.
	sess.xpLevel -= int32(cost)
	if sess.xpLevel < 0 {
		sess.xpLevel = 0
		sess.xpProgress = 0
		sess.xpTotal = 0
	}
	sess.enchantSeed = int32(rand.Uint32())
	m.seed = sess.enchantSeed
	out := enchantApply(item, list)
	sess.setMenuStack(enchantItem, out)
	if !creative {
		lapis.Count -= int32(cost)
		if lapis.Count <= 0 {
			lapis = ItemStack{}
		}
		sess.setMenuStack(enchantLapis, lapis)
	}
	sess.addCustomStat("enchant_item", 1)
	s.fireEnchantedItem(sess, out, int32(cost))
	s.sendSetExperience(sess, sess.xpProgress, sess.xpLevel, sess.xpTotal)
	s.syncContainerMenuSlots(sess, w, []int{enchantItem, enchantLapis})
	s.enchantSlotsChanged(sess, w)
	// level.playSound(null, tablePos, ENCHANTMENT_TABLE_USE, BLOCKS,
	// 1, rand*0.1+0.9) — a positional sound at the table.
	if id, ok := SoundEventID("block.enchantment_table.use"); ok {
		s.playSoundInPitch(w.dim.name, w.pos, id, rand.Float32()*0.1+0.9)
	}
}
