package worldgen

import "mc-go/internal/world"

// updateShape is BlockState.updateShape for the families a tree's edge
// update can touch: the tree's own blocks (logs, leaves, vines, cocoa,
// propagules, moss) and the terrain or plants next to them. Families
// whose updateShape only schedules ticks (leaves, water, mangrove
// roots, creaking hearts) or keeps the state return it unchanged.
func updateShape(rg *Region, st world.Block, p BlockPos, dir int, q BlockPos, nb world.Block) world.Block {
	name, _ := world.StateOwner(st)
	cls := blockClass(name)
	switch cls[0] {
	case "VineBlock":
		if dir == dirDown {
			return st
		}
		ns := vineUpdatedState(rg, st, p)
		if !vineHasFaces(ns) {
			return world.Air
		}
		return ns
	case "CocoaBlock":
		if f, _ := world.StateProperty(st, "facing"); f == dirName[dir] && !cocoaSurvives(rg, st, p) {
			return world.Air
		}
		return st
	case "HangingMossBlock":
		tip := "true"
		if isBlock(blockAt(rg, offset(p, 0, -1, 0)), name) {
			tip = "false"
		}
		return withProp(st, "tip", tip)
	case "CarpetBlock":
		if isAir(blockAt(rg, offset(p, 0, -1, 0))) {
			return world.Air
		}
		return st
	case "MossyCarpetBlock":
		if !canSurvive(rg, st, p) {
			return world.Air
		}
		ns := mossyCarpetUpdated(rg, st, p, false)
		if !mossyCarpetHasFaces(ns) {
			return world.Air
		}
		return ns
	case "DoublePlantBlock":
		half, _ := world.StateProperty(st, "half")
		if dir == dirUp || dir == dirDown {
			if (half == "lower") == (dir == dirUp) {
				nn, _ := world.StateOwner(nb)
				nh, _ := world.StateProperty(nb, "half")
				if nn != name || nh == half {
					return world.Air
				}
			}
		}
		if half == "lower" && dir == dirDown && !canSurvive(rg, st, p) {
			return world.Air
		}
		if !canSurvive(rg, st, p) {
			return world.Air
		}
		return st
	}
	for _, c := range cls {
		if c == "SnowyBlock" {
			if dir == dirUp && world.HasProperty(st, "snowy") {
				v := "false"
				if stateInTag(nb, "snow") {
					v = "true"
				}
				return withProp(st, "snowy", v)
			}
			return st
		}
	}
	if plainVegetation(cls) {
		if !canSurvive(rg, st, p) {
			return world.Air
		}
	}
	return st
}

// plainVegetation reports VegetationBlock families whose updateShape is
// VegetationBlock's (or adds only a survival check).
func plainVegetation(cls []string) bool {
	switch cls[0] {
	case "TallGrassBlock", "FlowerBlock", "MushroomBlock", "LeafLitterBlock", "SaplingBlock",
		"MangrovePropaguleBlock", "DryVegetationBlock", "BushBlock", "FireflyBushBlock",
		"TallDryGrassBlock", "ShortDryGrassBlock", "WitherRoseBlock", "EyeblossomBlock":
		return true
	}
	return false
}

func cocoaSurvives(rg *Region, st world.Block, p BlockPos) bool {
	f, _ := world.StateProperty(st, "facing")
	return stateInTag(blockAt(rg, relDir(p, dirNames[f], 1)), "supports_cocoa")
}

// canAttachTo is MultifaceBlock.canAttachTo: the neighbour's support
// or collision shape has a full face towards us. The collision half is
// derived from the full-cube collision fact (leaves have an empty
// support shape but a full collision cube).
func canAttachTo(rg *Region, dir int, q BlockPos) bool {
	b := blockAt(rg, q)
	return sturdyFull(b, oppositeDir(dir)) || fullCollision(b)
}

var vineFaces = [6]string{"", "up", "north", "south", "west", "east"}

// vineCanSupportAtFace is VineBlock.canSupportAtFace.
func vineCanSupportAtFace(rg *Region, p BlockPos, dir int) bool {
	if dir == dirDown {
		return false
	}
	if canAttachTo(rg, dir, relDir(p, dir, 1)) {
		return true
	}
	if dir == dirUp {
		return false
	}
	above := blockAt(rg, offset(p, 0, 1, 0))
	return isBlock(above, "vine") && propIs(above, vineFaces[dir], "true")
}

// vineUpdatedState is VineBlock.getUpdatedState.
func vineUpdatedState(rg *Region, st world.Block, p BlockPos) world.Block {
	up := offset(p, 0, 1, 0)
	if propIs(st, "up", "true") {
		v := "false"
		if canAttachTo(rg, dirDown, up) {
			v = "true"
		}
		st = withProp(st, "up", v)
	}
	for _, d := range horizontalDirs {
		f := vineFaces[d]
		if !propIs(st, f, "true") {
			continue
		}
		ok := vineCanSupportAtFace(rg, p, d)
		if !ok {
			above := blockAt(rg, up)
			ok = isBlock(above, "vine") && propIs(above, f, "true")
		}
		v := "false"
		if ok {
			v = "true"
		}
		st = withProp(st, f, v)
	}
	return st
}

func vineHasFaces(st world.Block) bool {
	for _, f := range vineFaces[1:] {
		if propIs(st, f, "true") {
			return true
		}
	}
	return false
}

// mossyCarpetUpdated is MossyCarpetBlock.getUpdatedState.
func mossyCarpetUpdated(rg *Region, st world.Block, p BlockPos, tall bool) world.Block {
	tall = tall || propIs(st, "bottom", "true")
	var above, below world.Block
	haveAbove, haveBelow := false, false
	for _, d := range horizontalDirs {
		f := vineFaces[d]
		var side string
		if d != dirUp && canAttachTo(rg, d, relDir(p, d, 1)) {
			if tall {
				side = "low"
			} else {
				side, _ = world.StateProperty(st, f)
			}
		} else {
			side = "none"
		}
		if side == "low" {
			if !haveAbove {
				above, haveAbove = blockAt(rg, offset(p, 0, 1, 0)), true
			}
			if isBlock(above, "pale_moss_carpet") && !propIs(above, f, "none") && !propIs(above, "bottom", "true") {
				side = "tall"
			}
			if !propIs(st, "bottom", "true") {
				if !haveBelow {
					below, haveBelow = blockAt(rg, offset(p, 0, -1, 0)), true
				}
				if isBlock(below, "pale_moss_carpet") && propIs(below, f, "none") {
					side = "none"
				}
			}
		}
		st = withProp(st, f, side)
	}
	return st
}

func mossyCarpetHasFaces(st world.Block) bool {
	if propIs(st, "bottom", "true") {
		return true
	}
	for _, d := range horizontalDirs {
		if !propIs(st, vineFaces[d], "none") {
			return true
		}
	}
	return false
}

func init() {
	surviveByClass["CocoaBlock"] = cocoaSurvives
	surviveByClass["VineBlock"] = func(rg *Region, st world.Block, p BlockPos) bool {
		return vineHasFaces(vineUpdatedState(rg, st, p))
	}
}
