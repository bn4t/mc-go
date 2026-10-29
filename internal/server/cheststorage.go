package server

import "mc-go/internal/world"

// hasChestStorage is the block-entity family, independently of menu behavior.
// Copper variants share the chest block entity and its27 inventory slots.
func hasChestStorage(state world.Block) bool {
	spec, ok := blockEntityOf(state)
	return ok && (spec.name == "minecraft:chest" || spec.name == "minecraft:trapped_chest")
}
