package worldgen

import (
	"runtime"
	"sync"
	"weak"

	"mc-go/internal/world"
)

// canSurvive families for the blocks the vegetation features place.
// Each cites the measured 26.2 override (class.method); a family is
// keyed by the most specific class that overrides canSurvive (or the
// mayPlaceOn it delegates to).

func init() {
	// GrowingPlantBlock.canSurvive: the block on the side opposite the
	// growth direction must pass canAttachTo, then be this plant's head
	// or body or have a sturdy face toward the growth direction.
	surviveByClass["GrowingPlantBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		name, _ := world.StateOwner(st)
		fam, ok := growingPlants[name]
		if !ok {
			return true
		}
		d := dirOffsets[oppositeDir(fam.growth)]
		q := offset(p, d[0], d[1], d[2])
		b := rg.Block(q[0], q[1], q[2])
		if fam.kelp && blockInTag(b, "cannot_support_kelp") {
			return false
		}
		return isBlock(b, fam.head) || isBlock(b, fam.body) || sturdyFull(b, fam.growth)
	}
	// BigDripleafBlock.canSurvive: below is a big dripleaf, its stem or
	// #supports_big_dripleaf.
	surviveByClass["BigDripleafBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		b := rg.Block(p[0], p[1]-1, p[2])
		return isBlock(b, "big_dripleaf") || isBlock(b, "big_dripleaf_stem") || blockInTag(b, "supports_big_dripleaf")
	}
	// BigDripleafStemBlock.canSurvive: stem or support below, stem or
	// leaf above.
	surviveByClass["BigDripleafStemBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		b := rg.Block(p[0], p[1]-1, p[2])
		a := rg.Block(p[0], p[1]+1, p[2])
		return (isBlock(b, "big_dripleaf_stem") || blockInTag(b, "supports_big_dripleaf")) &&
			(isBlock(a, "big_dripleaf_stem") || isBlock(a, "big_dripleaf"))
	}
	// SmallDripleafBlock: the upper half needs its lower half below; the
	// lower half's mayPlaceOn is #supports_small_dripleaf, or a water
	// source in the dripleaf's own cell over #supports_vegetation.
	surviveByClass["SmallDripleafBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		b := rg.Block(p[0], p[1]-1, p[2])
		if h, _ := world.StateProperty(st, "half"); h == "upper" {
			bh, _ := world.StateProperty(b, "half")
			return isBlock(b, "small_dripleaf") && bh == "lower"
		}
		if blockInTag(b, "supports_small_dripleaf") {
			return true
		}
		here := rg.Block(p[0], p[1], p[2])
		return fluidID(here) == "water" && blockInTag(b, "supports_vegetation")
	}
	// HangingRootsBlock.canSurvive: the block above is sturdy downward.
	surviveByClass["HangingRootsBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		return sturdyFull(rg.Block(p[0], p[1]+1, p[2]), dirDown)
	}
	// BambooStalkBlock / BambooSaplingBlock.canSurvive: #supports_bamboo
	// below.
	bamboo := func(rg blockGetter, st world.Block, p BlockPos) bool {
		return blockInTag(rg.Block(p[0], p[1]-1, p[2]), "supports_bamboo")
	}
	surviveByClass["BambooStalkBlock"] = bamboo
	surviveByClass["BambooSaplingBlock"] = bamboo
	// MultifaceBlock.canSurvive: at least one face, and every face set
	// attaches (MultifaceBlock.canAttachTo).
	surviveByClass["MultifaceBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		any := false
		for d := 0; d < 6; d++ {
			if !multifaceHas(st, d) {
				continue
			}
			if !multifaceCanAttach(rg, p, d) {
				return false
			}
			any = true
		}
		return any
	}
	// SeaPickleBlock.mayPlaceOn: the block below has a non-empty top
	// collision face or a sturdy top.
	surviveByClass["SeaPickleBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		b := rg.Block(p[0], p[1]-1, p[2])
		return topFaceNonEmpty(b) || sturdyFull(b, dirUp)
	}
	// SeagrassBlock.mayPlaceOn: sturdy top, not #cannot_support_seagrass.
	surviveByClass["SeagrassBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		return seagrassBase(rg.Block(p[0], p[1]-1, p[2]))
	}
	// TallSeagrassBlock.canSurvive: the upper half over its lower half;
	// the lower half on a seagrass base inside a full water cell.
	surviveByClass["TallSeagrassBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		b := rg.Block(p[0], p[1]-1, p[2])
		if h, _ := world.StateProperty(st, "half"); h == "upper" {
			bh, _ := world.StateProperty(b, "half")
			return isBlock(b, "tall_seagrass") && bh == "lower"
		}
		here := rg.Block(p[0], p[1], p[2])
		return seagrassBase(b) && isWaterAt(here) && fluidAmount(here) == 8
	}
	// CactusFlowerBlock.mayPlaceOn: #support_override_cactus_flower or a
	// center-sturdy top.
	surviveByClass["CactusFlowerBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		b := rg.Block(p[0], p[1]-1, p[2])
		return blockInTag(b, "support_override_cactus_flower") || sturdyCenter(b, dirUp)
	}
	// BaseCoralPlantTypeBlock.canSurvive: sturdy top below.
	surviveByClass["BaseCoralPlantTypeBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		return sturdyFull(rg.Block(p[0], p[1]-1, p[2]), dirUp)
	}
	// BaseCoralWallFanBlock.canSurvive: the block behind is sturdy on
	// the facing side.
	surviveByClass["BaseCoralWallFanBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		f, _ := world.StateProperty(st, "facing")
		d := dirNames[f]
		o := dirOffsets[oppositeDir(d)]
		return sturdyFull(rg.Block(p[0]+o[0], p[1]+o[1], p[2]+o[2]), d)
	}
	// SpeleothemBlock.isValidSpeleothemPlacement: the block behind the
	// tip is sturdy toward it, or the same speleothem pointing the same
	// way.
	surviveByClass["SpeleothemBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		v, _ := world.StateProperty(st, "vertical_direction")
		d := dirNames[v]
		o := dirOffsets[oppositeDir(d)]
		b := rg.Block(p[0]+o[0], p[1]+o[1], p[2]+o[2])
		if sturdyFull(b, d) {
			return true
		}
		name, _ := world.StateOwner(st)
		bv, _ := world.StateProperty(b, "vertical_direction")
		return blockInTag(b, "speleothems") && bv == v && isBlock(b, name)
	}
	// GlazedTerracottaBlock has no canSurvive override (the parity
	// tests' order probe places it).
	surviveByClass["GlazedTerracottaBlock"] = func(blockGetter, world.Block, BlockPos) bool { return true }
	// VineBlock.canSurvive: the shape update leaves at least one face.
	surviveByClass["VineBlock"] = func(rg blockGetter, st world.Block, p BlockPos) bool {
		return vineHasFaces(vineUpdatedState(rg, st, p))
	}
}

// growingPlant names a GrowingPlantBlock family: head/body blocks and
// growth direction (CaveVines DOWN, Kelp UP, WeepingVines DOWN,
// TwistingVines UP).
type growingPlant struct {
	head, body string
	growth     int
	kelp       bool // KelpBlock.canAttachTo: not #cannot_support_kelp
}

var growingPlants = func() map[string]growingPlant {
	m := map[string]growingPlant{}
	for _, f := range []growingPlant{
		{"cave_vines", "cave_vines_plant", dirDown, false},
		{"kelp", "kelp_plant", dirUp, true},
		{"weeping_vines", "weeping_vines_plant", dirDown, false},
		{"twisting_vines", "twisting_vines_plant", dirUp, false},
	} {
		m[f.head], m[f.body] = f, f
	}
	return m
}()

func seagrassBase(b world.Block) bool {
	return sturdyFull(b, dirUp) && !blockInTag(b, "cannot_support_seagrass")
}

// topFaceNonEmpty approximates "collision shape's UP face is not
// empty": the probe has no face-shape fact, so a sturdy (full, center
// or rigid) top or a full collision box stands in for it.
func topFaceNonEmpty(b world.Block) bool {
	return fullCollision(b) || sturdyCenter(b, dirUp) || sturdyRigid(b, dirUp)
}

var multifaceFaceProps = [6]string{"down", "up", "north", "south", "west", "east"}

func multifaceHas(st world.Block, d int) bool {
	v, _ := world.StateProperty(st, multifaceFaceProps[d])
	return v == "true"
}

// faceFullToward approximates MultifaceBlock.canAttachTo's
// "support shape or collision shape is full on the face": sturdy FULL
// covers the support shape, a full-cube collision box the other.
func faceFullToward(b world.Block, d int) bool {
	return sturdyFull(b, d) || fullCollision(b)
}

// multifaceCanAttach is MultifaceBlock.canAttachTo(level, pos, dir):
// the neighbour in dir has a full face toward pos.
func multifaceCanAttach(rg blockGetter, p BlockPos, d int) bool {
	o := dirOffsets[d]
	return faceFullToward(rg.Block(p[0]+o[0], p[1]+o[1], p[2]+o[2]), oppositeDir(d))
}

// regionRandom is WorldGenRegion.getRandom: one stream per region,
// from RandomState's "minecraft:worldgen_region_random" factory at the
// center chunk's world origin, shared by every feature of the
// decoration pass.
func regionRandom(rg *Region) Random {
	key := weak.Make(rg)
	regionRandMu.Lock()
	defer regionRandMu.Unlock()
	if r, ok := regionRands[key]; ok {
		return r
	}
	f := rg.g.RS.random.FromHashOf("minecraft:worldgen_region_random").ForkPositional()
	r := f.At(rg.center.CX*16, 0, rg.center.CZ*16)
	regionRands[key] = r
	runtime.AddCleanup(rg, func(k weak.Pointer[Region]) {
		regionRandMu.Lock()
		delete(regionRands, k)
		regionRandMu.Unlock()
	}, key)
	return r
}

var (
	regionRandMu sync.Mutex
	regionRands  = map[weak.Pointer[Region]]Random{}
)
