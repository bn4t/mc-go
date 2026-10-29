package worldgen

import (
	"encoding/json"

	"mc-go/internal/world"
)

// multiface_growth (MultifaceGrowthFeature): glow lichen / sculk vein
// on a supporting face of a listed block, then a chance to spread one
// step (MultifaceSpreader.spreadFromFaceTowardRandomDirection).

func init() {
	featureBuilders["multiface_growth"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		name := trimNS(cfg.str("block"))
		mf := multifaceKind{block: name, sculk: name == "sculk_vein"}
		mf.base = blockNamed(name)
		search := int32(10)
		if _, ok := cfg["search_range"]; ok {
			search = cfg.i32("search_range")
		}
		flag := func(k string) bool {
			var v bool
			if raw, ok := cfg[k]; ok {
				json.Unmarshal(raw, &v)
			}
			return v
		}
		// MultifaceGrowthConfiguration.validDirections: ceiling (UP),
		// floor (DOWN), then the horizontal plane.
		var valid []int
		if flag("can_place_on_ceiling") {
			valid = append(valid, dirUp)
		}
		if flag("can_place_on_floor") {
			valid = append(valid, dirDown)
		}
		if flag("can_place_on_wall") {
			valid = append(valid, horizontalDirs[:]...)
		}
		chance := float32(0.5)
		if raw, ok := cfg["chance_of_spreading"]; ok {
			chance = parseF32(raw)
		}
		placeOn := blockNameSet(cfg["can_be_placed_on"])
		airOrWater := func(b world.Block) bool { return isAir(b) || isBlock(b, "water") }

		grow := func(rg *Region, r Random, p BlockPos, cur world.Block, dirs []int) bool {
			for _, d := range dirs {
				o := dirOffsets[d]
				if !inTag(rg.Block(p[0]+o[0], p[1]+o[1], p[2]+o[2]), placeOn) {
					continue
				}
				st, ok := mf.stateForPlacement(rg, cur, p, d)
				if !ok {
					return false
				}
				rg.SetBlock(p[0], p[1], p[2], st)
				if r.NextFloat() < chance {
					mf.spreadTowardRandomDirection(rg, r, st, p, d)
				}
				return true
			}
			return false
		}
		return func(c *featCtx, r Random, p BlockPos) bool {
			rg := c.rg
			cur := rg.Block(p[0], p[1], p[2])
			if !airOrWater(cur) {
				return false
			}
			dirs := shuffledDirs(valid, r)
			if grow(rg, r, p, cur, dirs) {
				return true
			}
			for _, d := range dirs {
				var rest []int
				for _, v := range valid {
					if v != oppositeDir(d) {
						rest = append(rest, v)
					}
				}
				rest = shuffledDirs(rest, r)
				// The search re-tests origin+d each step (setWithOffset
				// from the origin), so only the first step can differ.
				if search <= 0 {
					continue
				}
				o := dirOffsets[d]
				q := offset(p, o[0], o[1], o[2])
				st := rg.Block(q[0], q[1], q[2])
				if !airOrWater(st) && !isBlock(st, name) {
					continue
				}
				if grow(rg, r, q, st, rest) {
					return true
				}
			}
			return false
		}
	}
}

// multifaceKind is a MultifaceSpreadeableBlock with its spreader config
// (DefaultSpreaderConfig, or SculkVeinSpreaderConfig for sculk vein).
type multifaceKind struct {
	block string
	base  world.Block
	sculk bool
}

// validForPlacement is MultifaceBlock.isValidStateForPlacement.
func (m multifaceKind) validForPlacement(rg *Region, cur world.Block, p BlockPos, d int) bool {
	if isBlock(cur, m.block) && multifaceHas(cur, d) {
		return false
	}
	return multifaceCanAttach(rg, p, d)
}

// stateForPlacement is MultifaceBlock.getStateForPlacement(state,
// level, pos, dir).
func (m multifaceKind) stateForPlacement(rg *Region, cur world.Block, p BlockPos, d int) (world.Block, bool) {
	if !m.validForPlacement(rg, cur, p, d) {
		return 0, false
	}
	st := m.base
	switch {
	case isBlock(cur, m.block):
		st = cur
	case isWaterAt(cur) && isFluidSource(cur):
		st = carpetWith(st, "waterlogged", "true")
	}
	return carpetWith(st, multifaceFaceProps[d], "true"), true
}

// replaceable is the spreader config's stateCanBeReplaced.
func (m multifaceKind) replaceable(rg *Region, src, q BlockPos, face int, st world.Block) bool {
	base := isAir(st) || isBlock(st, m.block) || (isBlock(st, "water") && isFluidSource(st))
	if !m.sculk {
		return base
	}
	o := dirOffsets[face]
	behind := rg.Block(q[0]+o[0], q[1]+o[1], q[2]+o[2])
	if isBlock(behind, "sculk") || isBlock(behind, "sculk_catalyst") || isBlock(behind, "moving_piston") {
		return false
	}
	if absI(src[0]-q[0])+absI(src[1]-q[1])+absI(src[2]-q[2]) == 2 {
		b := offset(src, -o[0], -o[1], -o[2])
		if sturdyFull(rg.Block(b[0], b[1], b[2]), face) {
			return false
		}
	}
	// FluidState.is(Fluids.WATER): only a water source passes.
	if hasFluid(st) && !(isWaterFluid(st) && isFluidSource(st)) {
		return false
	}
	if blockInTag(st, "fire") {
		return false
	}
	return isReplaceable(st) || base
}

// spreadTowardRandomDirection is
// MultifaceSpreader.spreadFromFaceTowardRandomDirection: the first of
// the six shuffled directions with a valid spread target places there.
func (m multifaceKind) spreadTowardRandomDirection(rg *Region, r Random, st world.Block, p BlockPos, face int) {
	all := []int{dirDown, dirUp, dirNorth, dirSouth, dirWest, dirEast}
	for _, sd := range shuffledDirs(all, r) {
		if sd>>1 == face>>1 { // same axis
			continue
		}
		// canSpreadFrom: sculk vein accepts any other block as source.
		if !(m.sculk && !isBlock(st, "sculk_vein")) && !(multifaceHas(st, face) && !multifaceHas(st, sd)) {
			continue
		}
		so, fo := dirOffsets[sd], dirOffsets[face]
		cands := [3]struct {
			q    BlockPos
			face int
		}{
			{p, sd},                                // SAME_POSITION
			{offset(p, so[0], so[1], so[2]), face}, // SAME_PLANE
			{offset(p, so[0]+fo[0], so[1]+fo[1], so[2]+fo[2]), oppositeDir(sd)}, // WRAP_AROUND
		}
		for _, cd := range cands {
			cur := rg.Block(cd.q[0], cd.q[1], cd.q[2])
			if !m.replaceable(rg, p, cd.q, cd.face, cur) || !m.validForPlacement(rg, cur, cd.q, cd.face) {
				continue
			}
			// spreadToFace: placement re-checked on the current state.
			ns, ok := m.stateForPlacement(rg, cur, cd.q, cd.face)
			if ok {
				rg.SetBlock(cd.q[0], cd.q[1], cd.q[2], ns)
				return
			}
			break
		}
	}
}

func absI(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
