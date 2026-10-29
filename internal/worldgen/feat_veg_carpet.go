package worldgen

import "mc-go/internal/world"

// Pale moss carpet placement (MossyCarpetBlock.placeAt), reached from
// simple_block: the base carpet takes LOW sides wherever a horizontal
// neighbour can carry it, then a side-only topper may go above it with
// each side kept on a coin flip from the region's random.

var horizontalSideProps = map[int]string{dirNorth: "north", dirEast: "east", dirSouth: "south", dirWest: "west"}

func carpetWith(st world.Block, prop, v string) world.Block {
	b, _ := world.WithProperty(st, prop, v)
	return b
}

func carpetBase(st world.Block) bool {
	v, _ := world.StateProperty(st, "bottom")
	return v == "true"
}

// mossyCarpetUpdated is MossyCarpetBlock.getUpdatedState.
func mossyCarpetUpdated(rg *Region, st world.Block, p BlockPos, tall bool) world.Block {
	tall = tall || carpetBase(st)
	var above, below world.Block
	haveAbove, haveBelow := false, false
	for _, d := range horizontalDirs {
		prop := horizontalSideProps[d]
		side := "none"
		// canSupportAtFace: never UP; otherwise MultifaceBlock.canAttachTo.
		if multifaceCanAttach(rg, p, d) {
			if tall {
				side = "low"
			} else {
				side, _ = world.StateProperty(st, prop)
			}
		}
		if side == "low" {
			if !haveAbove {
				above, haveAbove = rg.Block(p[0], p[1]+1, p[2]), true
			}
			if isBlock(above, "pale_moss_carpet") && !carpetBase(above) {
				if v, _ := world.StateProperty(above, prop); v != "none" {
					side = "tall"
				}
			}
			if !carpetBase(st) {
				if !haveBelow {
					below, haveBelow = rg.Block(p[0], p[1]-1, p[2]), true
				}
				if isBlock(below, "pale_moss_carpet") {
					if v, _ := world.StateProperty(below, prop); v == "none" {
						side = "none"
					}
				}
			}
		}
		st = carpetWith(st, prop, side)
	}
	return st
}

func mossyCarpetHasFaces(st world.Block) bool {
	if carpetBase(st) {
		return true
	}
	for _, prop := range horizontalSideProps {
		if v, _ := world.StateProperty(st, prop); v != "none" {
			return true
		}
	}
	return false
}

// mossyCarpetTopper is MossyCarpetBlock.createTopperWithSideChance.
func mossyCarpetTopper(rg *Region, p BlockPos, keep func() bool) world.Block {
	up := offset(p, 0, 1, 0)
	cur := rg.Block(up[0], up[1], up[2])
	isCarpet := isBlock(cur, "pale_moss_carpet")
	if (isCarpet && carpetBase(cur)) || (!isCarpet && !isReplaceable(cur)) {
		return world.Air
	}
	st := mossyCarpetUpdated(rg, carpetWith(blockNamed("pale_moss_carpet"), "bottom", "false"), up, true)
	for _, d := range horizontalDirs {
		prop := horizontalSideProps[d]
		if v, _ := world.StateProperty(st, prop); v != "none" && !keep() {
			st = carpetWith(st, prop, "none")
		}
	}
	if mossyCarpetHasFaces(st) && st != cur {
		return st
	}
	return world.Air
}

// mossyCarpetPlaceAt is MossyCarpetBlock.placeAt with the region's
// random (WorldGenLevel.getRandom).
func mossyCarpetPlaceAt(rg *Region, p BlockPos) {
	st := mossyCarpetUpdated(rg, blockNamed("pale_moss_carpet"), p, true)
	rg.SetBlock(p[0], p[1], p[2], st)
	r := regionRandom(rg)
	top := mossyCarpetTopper(rg, p, r.NextBool)
	if !isAir(top) {
		rg.SetBlock(p[0], p[1]+1, p[2], top)
		rg.SetBlock(p[0], p[1], p[2], mossyCarpetUpdated(rg, st, p, true))
	}
}
