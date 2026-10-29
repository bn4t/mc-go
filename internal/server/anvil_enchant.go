package server

import (
	"mc-go/internal/data"
	"mc-go/internal/world"
)

// Rules measured using JDK 25 javap on AnvilMenu.createResult and pinned
// on the wire. Anvil costs/support sets come from the official 26.2 datapack
// (server jar SHA1 823e2250d24b3ddac457a60c92a6a941943fcd6a), via genenchant.
// anvilMergeEnchantments prices accepted additions independently of repair
// and rename work. Equal levels upgrade once, and all results cap at the
// enchantment maximum. Conflicts charge one per conflicting enchantment;
// a sacrifice whose enchantments are all rejected invalidates the operation.
func anvilMergeEnchantments(target, sacrifice ItemStack, creative bool) (ItemStack, int32, bool) {
	levels := map[string]int{}
	for _, e := range grindstoneEnchEntries(grindstoneEnchOf(target)) {
		levels[e.name] = e.level
	}
	name, _ := itemNameOf(target.ID)
	book := sacrifice.ID == world.ItemIDs["enchanted_book"]
	var cost int32
	accepted, rejected := false, false
	for _, e := range grindstoneEnchEntries(grindstoneEnchOf(sacrifice)) {
		meta, known := data.Enchantments[e.name]
		allowed := known && (creative || name == "enchanted_book" || itemIsMember(meta.Supported, name))
		for other := range levels {
			if other != e.name && !enchantsCompatible(other, e.name) {
				allowed = false
				cost++
			}
		}
		if !allowed {
			rejected = true
			continue
		}
		level := e.level
		if levels[e.name] == level {
			level++
		} else if levels[e.name] > level {
			level = levels[e.name]
		}
		if level > meta.MaxLevel {
			level = meta.MaxLevel
		}
		levels[e.name] = level
		weight := int32(meta.AnvilCost)
		if book {
			weight /= 2
			if weight < 1 {
				weight = 1
			}
		}
		cost += weight * int32(level)
		if target.Count > 1 {
			cost = 40
		}
		accepted = true
	}
	if name == "enchanted_book" {
		target.StoredEnch = grindstoneWriteEnch(levels)
	} else {
		target.Ench = grindstoneWriteEnch(levels)
	}
	return target, cost, rejected && !accepted
}
