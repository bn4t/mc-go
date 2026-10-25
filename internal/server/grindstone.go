package server

// GrindstoneMenu parity: two input slots (top 0, bottom 1), one
// result slot (2). The result is computed inside slotsChanged, same
// as the smithing table: either a merge of two identical items or a
// disenchant of a single enchanted one. All arithmetic below is
// transcribed from the 26.2 jar's GrindstoneMenu bytecode.

import (
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// grindstoneCurses is the minecraft:curse enchantment tag in 26.2:
// the enchantments grinding keeps.
var grindstoneCurses = map[string]bool{
	"binding_curse":   true,
	"vanishing_curse": true,
}

// grindstoneEnchOf picks the crafting enchantment list the way
// EnchantmentHelper.getEnchantmentsForCrafting does: an enchanted_book
// carries stored_enchantments; everything else uses enchantments.
func grindstoneEnchOf(it ItemStack) string {
	if name, ok := itemNameOf(it.ID); ok && name == "enchanted_book" {
		return it.StoredEnch
	}
	return it.Ench
}

// grindstoneEnchEntries parses the canonical "name:level,..." list.
func grindstoneEnchEntries(ench string) []enchantInstance {
	var out []enchantInstance
	for _, e := range strings.Split(ench, ",") {
		n, lv, ok := strings.Cut(e, ":")
		if !ok || n == "" {
			continue
		}
		v, err := strconv.Atoi(lv)
		if err != nil || v <= 0 {
			continue
		}
		out = append(out, enchantInstance{name: n, level: v})
	}
	return out
}

// grindstoneWriteEnch serialises an enchantment map canonical-sorted.
func grindstoneWriteEnch(entries map[string]int) string {
	var names []string
	for n, lv := range entries {
		if lv > 0 {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var parts []string
	for _, n := range names {
		parts = append(parts, n+":"+strconv.Itoa(entries[n]))
	}
	return strings.Join(parts, ",")
}

// grindstoneCompute mirrors GrindstoneMenu.computeResult: a count>1
// stack never produces output, a lone item needs any enchantment to
// disenchant, and a pair goes through mergeItems.
func grindstoneCompute(top, bot ItemStack) ItemStack {
	if top.Empty() && bot.Empty() {
		return ItemStack{}
	}
	if top.Count > 1 || bot.Count > 1 {
		return ItemStack{}
	}
	if !top.Empty() && !bot.Empty() {
		return grindstoneMerge(top, bot)
	}
	it := top
	if it.Empty() {
		it = bot
	}
	if it.Ench == "" && it.StoredEnch == "" {
		return ItemStack{}
	}
	return grindstoneStrip(it)
}

// grindstoneMerge mirrors GrindstoneMenu.mergeItems: same item only;
// damageable results get the summed-remaining-plus-5% durability
// merge; non-damageable ones need stackability and produce a count-2
// stack.
func grindstoneMerge(top, bot ItemStack) ItemStack {
	if top.ID != bot.ID {
		return ItemStack{}
	}
	max1 := MaxDamageOfID(top.ID)
	max2 := MaxDamageOfID(bot.ID)
	maxD := max(max1, max2)
	combined := max1 - top.Damage + max2 - bot.Damage + maxD*5/100
	count := int32(1)
	if max1 <= 0 {
		if maxStackOfID(top.ID) < 2 || !top.Stackable(bot) {
			return ItemStack{}
		}
		count = 2
	}
	out := top
	out.Count = count
	if max1 > 0 {
		// ItemStack.set(MAX_DAMAGE, maxD): equal inputs can only carry
		// the item's own max, so the set is a no-op on the wire here.
		out.Damage = max(0, maxD-combined)
	}
	grindstoneMergeEnchs(&out, bot)
	return grindstoneStrip(out)
}

// grindstoneMergeEnchs mirrors the mergeEnchantsFrom lambda: every
// crafting enchantment on bot is added unless it is a curse already
// present on the target — Mutable.upgrade merges by sum.
func grindstoneMergeEnchs(out *ItemStack, bot ItemStack) {
	entries := map[string]int{}
	for _, e := range grindstoneEnchEntries(grindstoneEnchOf(*out)) {
		entries[e.name] = e.level
	}
	for _, e := range grindstoneEnchEntries(grindstoneEnchOf(bot)) {
		if grindstoneCurses[e.name] && entries[e.name] != 0 {
			continue
		}
		add := min(e.level, 255)
		if _, ok := entries[e.name]; ok {
			entries[e.name] += add
		} else {
			entries[e.name] = add
		}
	}
	if name, ok := itemNameOf(out.ID); ok && name == "enchanted_book" {
		out.StoredEnch = grindstoneWriteEnch(entries)
	} else {
		out.Ench = grindstoneWriteEnch(entries)
	}
}

// grindstoneStrip mirrors removeNonCursesFrom: non-curse enchantments
// come off the crafting component, an enchanted_book with nothing
// left transmutes to book, and repair_cost becomes
// calculateIncreasedRepairCost applied once per kept enchantment —
// folded 2c+1 from 0, so one curse yields 1, two yield 3.
func grindstoneStrip(it ItemStack) ItemStack {
	out := it
	if name, ok := itemNameOf(out.ID); ok && name == "enchanted_book" {
		out.StoredEnch = grindstoneKeepCurses(out.StoredEnch)
		if out.StoredEnch == "" {
			if book, ok2 := world.ItemID("book"); ok2 {
				out = ItemStack{ID: book, Count: out.Count}
			}
		}
	} else {
		out.Ench = grindstoneKeepCurses(out.Ench)
	}
	cost := 0
	for range grindstoneEnchEntries(grindstoneEnchOf(out)) {
		cost = 2*cost + 1
	}
	out.RepairCost = int32(cost)
	return out
}

// grindstoneKeepCurses filters an enchantment list to the curse tag.
func grindstoneKeepCurses(ench string) string {
	var parts []string
	for _, e := range grindstoneEnchEntries(ench) {
		if grindstoneCurses[e.name] {
			parts = append(parts, e.name+":"+strconv.Itoa(e.level))
		}
	}
	return strings.Join(parts, ",")
}

// grindstoneXPOfItem mirrors getExperienceFromItem: the sum of
// getMinCost(level) over every non-curse crafting enchantment.
func grindstoneXPOfItem(it ItemStack) int {
	if it.Empty() {
		return 0
	}
	total := 0
	for _, e := range grindstoneEnchEntries(grindstoneEnchOf(it)) {
		if grindstoneCurses[e.name] {
			continue
		}
		if meta, ok := data.Enchantments[e.name]; ok {
			total += meta.MinBase + meta.MinPerLvl*(e.level-1)
		}
	}
	return total
}

// xpOrbDenominations is ExperienceOrb.getExperienceValue's ladder:
// an award splits into these sizes, largest-first.
var xpOrbDenominations = []int32{2477, 1237, 617, 307, 149, 73, 37, 17, 7, 3, 1}

// awardXPOrbs mirrors ExperienceOrb.award: the value splits into the
// vanilla denominations, one entity per chunk, at the block's center.
// tryMergeToExisting is a no-op where no orbs already sit nearby.
func (s *Server) awardXPOrbs(dim string, pos world.Pos, xp int32) {
	for xp > 0 {
		v := int32(1)
		for _, d := range xpOrbDenominations {
			if xp >= d {
				v = d
				break
			}
		}
		s.spawnXPOrb(dim, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, v)
		xp -= v
	}
}

// grindstoneTake mirrors GrindstoneMenu$4.onTake: both inputs are
// consumed, the measured XP (sum of removed enchantment costs, then
// ceil(half) plus a random draw below it) is awarded as orbs at the
// block, and level event 1042 plays the grindstone-use sound.
func (s *Server) grindstoneTake(sess *Session, w *openWindow) {
	xp := grindstoneXPOfItem(sess.menuStack(grindstoneTop)) +
		grindstoneXPOfItem(sess.menuStack(grindstoneBottom))
	sess.setMenuStack(grindstoneTop, ItemStack{})
	sess.setMenuStack(grindstoneBottom, ItemStack{})
	if xp > 0 {
		i3 := int32((xp + 1) / 2)
		s.awardXPOrbs(w.dim.name, w.pos, i3+rand.Int31n(i3))
	}
	s.broadcastLevelEvent(w.dim.name, 1042, w.pos, 0)
}

// transientMenuReturn is the vanilla clearContainer path every
// non-persistent menu takes on close (grindstone, smithing, loom,
// cartography, stonecutter): input slots come back to the player and
// the transient result clears without returning.
func (s *Server) transientMenuReturn(sess *Session, w *openWindow, c *containerData, inputs []int) {
	for _, sl := range inputs {
		w.dim.containers.mu.Lock()
		st := c.slots[sl]
		w.dim.containers.mu.Unlock()
		if st.Empty() {
			continue
		}
		w.dim.containers.mu.Lock()
		c.slots[sl] = ItemStack{}
		w.dim.containers.mu.Unlock()
		rest, _ := sess.addStack(st)
		if !rest.Empty() {
			w.dim.containers.mu.Lock()
			c.slots[sl] = rest
			w.dim.containers.mu.Unlock()
			continue
		}
		s.sendAnvilReturnSlot(sess, st)
	}
	w.dim.containers.mu.Lock()
	for sl := range c.spec.slots {
		c.slots[sl] = ItemStack{}
	}
	w.dim.containers.mu.Unlock()
}
