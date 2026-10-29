package worldgen

import (
	"strings"

	"mc-go/internal/world"
)

// canSurvive families for player-placed blocks (BlockItem.place checks
// BlockState.canSurvive before writing), measured from the 26.2 jar.
// Each cites the overriding class.method.

// canSupportCenter is Block.canSupportCenter: a center-sturdy face
// toward dir, except an #unstable_bottom_center block's bottom.
func canSupportCenter(rg blockGetter, p BlockPos, dir int) bool {
	b := rg.at(p)
	if dir == dirDown && blockInTag(b, "unstable_bottom_center") {
		return false
	}
	return sturdyCenter(b, dir)
}

// canSupportRigidBlock is Block.canSupportRigidBlock: a rigid-sturdy top.
func canSupportRigidBlock(rg blockGetter, p BlockPos) bool { return sturdyRigid(rg.at(p), dirUp) }

func propDir(st world.Block, prop string) int {
	v, _ := world.StateProperty(st, prop)
	if d, ok := dirNames[v]; ok {
		return d
	}
	return dirNorth
}

// relative is BlockPos.relStep(dir).
func relStep(p BlockPos, dir int) BlockPos {
	o := dirOffsets[dir]
	return offset(p, o[0], o[1], o[2])
}

// faceAttachedDir is FaceAttachedHorizontalDirectionalBlock
// .getConnectedDirection: floor DOWN, ceiling UP, wall FACING.
func faceAttachedDir(st world.Block) int {
	switch v, _ := world.StateProperty(st, "face"); v {
	case "floor":
		return dirUp
	case "ceiling":
		return dirDown
	}
	return propDir(st, "facing")
}

func init() {
	// BaseTorchBlock.canSurvive: canSupportCenter(below, UP).
	surviveByClass["BaseTorchBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		return canSupportCenter(rg, offset(p, 0, -1, 0), dirUp)
	}
	// WallTorchBlock.canSurvive (and RedstoneWallTorchBlock): the block
	// behind is sturdy on the facing side.
	wallTorch := func(rg blockGetter, st world.Block, p BlockPos) bool {
		f := propDir(st, "facing")
		return sturdyFull(rg.at(relStep(p, oppositeDir(f))), f)
	}
	surviveByClass["WallTorchBlock"] = wallTorch
	surviveByClass["RedstoneWallTorchBlock"] = wallTorch
	// LadderBlock.canSurvive: canAttachTo(behind, facing) - full sturdy.
	surviveByClass["LadderBlock"] = wallTorch
	// StandingSignBlock / BannerBlock / CakeBlock / CandleCakeBlock: a
	// solid block below.
	solidBelow := func(rg blockGetter, st world.Block, p BlockPos) bool {
		return isSolidState(rg.at(offset(p, 0, -1, 0)))
	}
	surviveByClass["StandingSignBlock"] = solidBelow
	surviveByClass["BannerBlock"] = solidBelow
	surviveByClass["CakeBlock"] = solidBelow
	surviveByClass["CandleCakeBlock"] = solidBelow
	// WallSignBlock / WallBannerBlock: a solid block behind.
	solidBehind := func(rg blockGetter, st world.Block, p BlockPos) bool {
		return isSolidState(rg.at(relStep(p, oppositeDir(propDir(st, "facing")))))
	}
	surviveByClass["WallSignBlock"] = solidBehind
	surviveByClass["WallBannerBlock"] = solidBehind
	// CeilingHangingSignBlock.canSurvive: center-sturdy bottom above.
	surviveByClass["CeilingHangingSignBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		return sturdyCenter(rg.at(offset(p, 0, 1, 0)), dirDown)
	}
	// WallHangingSignBlock has no canSurvive override.
	surviveByClass["WallHangingSignBlock"] = func(blockGetter, world.Block, BlockPos) bool { return true }
	// FaceAttachedHorizontalDirectionalBlock.canSurvive: canAttach on
	// the connected direction's opposite (full sturdy face toward it).
	// GrindstoneBlock overrides it to true.
	surviveByClass["FaceAttachedHorizontalDirectionalBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		d := oppositeDir(faceAttachedDir(st))
		return sturdyFull(rg.at(relStep(p, d)), oppositeDir(d))
	}
	surviveByClass["GrindstoneBlock"] = func(blockGetter, world.Block, BlockPos) bool { return true }
	// BellBlock.canSurvive: a ceiling bell needs canSupportCenter above
	// (DOWN); others canAttach toward the connected direction's opposite.
	surviveByClass["BellBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		var conn int
		switch a, _ := world.StateProperty(st, "attachment"); a {
		case "floor":
			conn = dirUp
		case "ceiling":
			conn = dirDown
		default:
			// A wall bell's FACING points into its wall.
			conn = oppositeDir(propDir(st, "facing"))
		}
		d := oppositeDir(conn)
		if d == dirUp {
			return canSupportCenter(rg, offset(p, 0, 1, 0), dirDown)
		}
		return sturdyFull(rg.at(relStep(p, d)), oppositeDir(d))
	}
	// LanternBlock.canSurvive: canSupportCenter toward the attachment
	// (above when hanging, below otherwise).
	surviveByClass["LanternBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		d := dirDown
		if propIs(st, "hanging", "true") {
			d = dirUp
		}
		return canSupportCenter(rg, relStep(p, d), oppositeDir(d))
	}
	// DoorBlock.canSurvive: the lower half on a full sturdy top, the
	// upper half over this door.
	surviveByClass["DoorBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		b := rg.at(offset(p, 0, -1, 0))
		if propIs(st, "half", "lower") {
			return sturdyFull(b, dirUp)
		}
		n, _ := world.StateOwner(st)
		return isBlock(b, n)
	}
	// BaseRailBlock.canSurvive: canSupportRigidBlock below.
	surviveByClass["BaseRailBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		return canSupportRigidBlock(rg, offset(p, 0, -1, 0))
	}
	// DiodeBlock.canSurvive: a rigid-sturdy top below.
	surviveByClass["DiodeBlock"] = surviveByClass["BaseRailBlock"]
	// RedStoneWireBlock.canSurvive: a full sturdy top below, or a hopper.
	surviveByClass["RedStoneWireBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		b := rg.at(offset(p, 0, -1, 0))
		return sturdyFull(b, dirUp) || isBlock(b, "hopper")
	}
	// BasePressurePlateBlock.canSurvive: rigid support or a center-sturdy
	// top below.
	surviveByClass["BasePressurePlateBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		q := offset(p, 0, -1, 0)
		return canSupportRigidBlock(rg, q) || canSupportCenter(rg, q, dirUp)
	}
	// CandleBlock.canSurvive: canSupportCenter(below, UP).
	surviveByClass["CandleBlock"] = surviveByClass["BaseTorchBlock"]
	// AmethystClusterBlock.canSurvive: the block behind sturdy toward it.
	surviveByClass["AmethystClusterBlock"] = wallTorch
	// TripWireHookBlock.canSurvive: horizontal facing with a full sturdy
	// face behind.
	surviveByClass["TripWireHookBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		f := propDir(st, "facing")
		if f == dirUp || f == dirDown {
			return false
		}
		return sturdyFull(rg.at(relStep(p, oppositeDir(f))), f)
	}
	// ChorusPlantBlock.canSurvive: with air neither above nor below, no
	// horizontal plant; a horizontal plant counts when the block under
	// it is a plant or #supports_chorus_plant; else the block below is.
	surviveByClass["ChorusPlantBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		name, _ := world.StateOwner(st)
		below := rg.at(offset(p, 0, -1, 0))
		vertical := !isAir(rg.at(offset(p, 0, 1, 0))) && !isAir(below)
		for _, d := range [4]int{dirNorth, dirSouth, dirWest, dirEast} {
			q := relStep(p, d)
			if !isBlock(rg.at(q), name) {
				continue
			}
			if vertical {
				return false
			}
			u := rg.at(offset(q, 0, -1, 0))
			if isBlock(u, name) || blockInTag(u, "supports_chorus_plant") {
				return true
			}
		}
		return isBlock(below, name) || blockInTag(below, "supports_chorus_plant")
	}
	// ChorusFlowerBlock.canSurvive: the plant or #supports_chorus_flower
	// below; else over air exactly one horizontal plant and only air
	// otherwise.
	surviveByClass["ChorusFlowerBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		below := rg.at(offset(p, 0, -1, 0))
		if isBlock(below, "chorus_plant") || blockInTag(below, "supports_chorus_flower") {
			return true
		}
		if !isAir(below) {
			return false
		}
		found := false
		for _, d := range [4]int{dirNorth, dirSouth, dirWest, dirEast} {
			n := rg.at(relStep(p, d))
			if isBlock(n, "chorus_plant") {
				if found {
					return false
				}
				found = true
			} else if !isAir(n) {
				return false
			}
		}
		return found
	}
	// CropBlock.canSurvive: raw brightness at least 8 and #supports_crops
	// below (CropBlock.mayPlaceOn).
	surviveByClass["CropBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		return rg.rawBrightness(p) >= 8 && blockInTag(rg.at(offset(p, 0, -1, 0)), "supports_crops")
	}
	// StemBlock / AttachedStemBlock.mayPlaceOn: the stem's support tag
	// (supports_pumpkin_stem / supports_melon_stem).
	stem := func(rg blockGetter, st world.Block, p BlockPos) bool {
		name, _ := world.StateOwner(st)
		name = strings.TrimPrefix(name, "attached_")
		return blockInTag(rg.at(offset(p, 0, -1, 0)), "supports_"+name)
	}
	surviveByClass["StemBlock"] = stem
	surviveByClass["AttachedStemBlock"] = stem
	// NetherWartBlock / WitherRoseBlock.mayPlaceOn: their support tags.
	surviveByClass["NetherWartBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		return blockInTag(rg.at(offset(p, 0, -1, 0)), "supports_nether_wart")
	}
	surviveByClass["WitherRoseBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		return blockInTag(rg.at(offset(p, 0, -1, 0)), "supports_wither_rose")
	}
	// FrogspawnBlock.canSurvive: below's fluid in #supports_frogspawn or
	// its block in the block tag, with no fluid in the frogspawn's cell.
	surviveByClass["FrogspawnBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		b := rg.at(offset(p, 0, -1, 0))
		return (inFluidTag(b, "supports_frogspawn") || blockInTag(b, "supports_frogspawn")) && fluidID(rg.at(p)) == "empty"
	}
	// DirtPathBlock.canSurvive: nothing solid above but a fence gate.
	surviveByClass["DirtPathBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		a := rg.at(offset(p, 0, 1, 0))
		return !isSolidState(a) || hasClassState(a, "FenceGateBlock")
	}
	// FarmlandBlock.canSurvive: nothing solid above, or a block in
	// #maintains_farmland.
	surviveByClass["FarmlandBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		a := rg.at(offset(p, 0, 1, 0))
		return !isSolidState(a) || blockInTag(a, "maintains_farmland")
	}
	// HangingMossBlock.canStayAtPosition: the block above attaches
	// (MultifaceBlock.canAttachTo, UP) or is hanging moss.
	surviveByClass["HangingMossBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		name, _ := world.StateOwner(st)
		return multifaceCanAttach(rg, p, dirUp) || isBlock(rg.at(offset(p, 0, 1, 0)), name)
	}
	// PitcherCropBlock.canSurvive: the lower half needs raw brightness
	// 8 (sufficientLight), then the double plant rules.
	surviveByClass["PitcherCropBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		if propIs(st, "half", "lower") {
			if rg.rawBrightness(p) < 8 {
				return false
			}
			return blockInTag(rg.at(offset(p, 0, -1, 0)), "supports_crops")
		}
		b := rg.at(offset(p, 0, -1, 0))
		return isBlock(b, "pitcher_crop") && propIs(b, "half", "lower")
	}
}
