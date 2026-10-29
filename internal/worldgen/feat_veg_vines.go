package worldgen

import "mc-go/internal/world"

// vines (VinesFeature): an air origin takes one vine face, the first
// direction in Direction order (DOWN skipped) whose neighbour offers a
// full face (VineBlock.isAcceptableNeighbour).

func init() {
	featureBuilders["vines"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		vine := blockNamed("vine")
		return func(c *featCtx, r Random, p BlockPos) bool {
			if !isAir(c.rg.Block(p[0], p[1], p[2])) {
				return false
			}
			for d := dirUp; d <= dirEast; d++ {
				if vineAcceptable(c.rg, p, d) {
					c.rg.SetBlock(p[0], p[1], p[2], carpetWith(vine, multifaceFaceProps[d], "true"))
					return true
				}
			}
			return false
		}
	}
}

// vineAcceptable is VineBlock.isAcceptableNeighbour for the neighbour
// of p in direction d.
func vineAcceptable(rg blockGetter, p BlockPos, d int) bool { return multifaceCanAttach(rg, p, d) }

func vineFace(st world.Block, d int) bool {
	v, _ := world.StateProperty(st, multifaceFaceProps[d])
	return v == "true"
}

// vineSupportAt is VineBlock.canSupportAtFace.
func vineSupportAt(rg blockGetter, p BlockPos, d int) bool {
	if d == dirDown {
		return false
	}
	if vineAcceptable(rg, p, d) {
		return true
	}
	if d == dirUp {
		return false
	}
	a := rg.Block(p[0], p[1]+1, p[2])
	return isBlock(a, "vine") && vineFace(a, d)
}

// vineUpdatedState is VineBlock.getUpdatedState.
func vineUpdatedState(rg blockGetter, st world.Block, p BlockPos) world.Block {
	if vineFace(st, dirUp) {
		st = carpetWith(st, "up", boolStr(vineAcceptable(rg, p, dirUp)))
	}
	for _, d := range horizontalDirs {
		if !vineFace(st, d) {
			continue
		}
		ok := vineSupportAt(rg, p, d)
		if !ok {
			a := rg.Block(p[0], p[1]+1, p[2])
			ok = isBlock(a, "vine") && vineFace(a, d)
		}
		st = carpetWith(st, multifaceFaceProps[d], boolStr(ok))
	}
	return st
}

func vineHasFaces(st world.Block) bool {
	for d := dirUp; d <= dirEast; d++ {
		if vineFace(st, d) {
			return true
		}
	}
	return false
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
