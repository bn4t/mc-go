package world

import "strings"

// HeightmapPredicates are the per-state tests behind the four
// heightmap types. The defaults are name-based approximations; the
// worldgen package installs the exact per-state facts measured from
// the vanilla jar (it imports world, so it cannot be called from here).
var HeightmapPredicates = struct {
	// WorldSurface is !isAir (WORLD_SURFACE).
	WorldSurface func(Block) bool
	// OceanFloor is blocksMotion (OCEAN_FLOOR).
	OceanFloor func(Block) bool
	// MotionBlocking is blocksMotion or a fluid (MOTION_BLOCKING).
	MotionBlocking func(Block) bool
	// MotionBlockingNoLeaves is MOTION_BLOCKING minus leaves.
	MotionBlockingNoLeaves func(Block) bool
}{
	WorldSurface: func(b Block) bool { return b != Air },
	OceanFloor:   func(b Block) bool { return !NonSolid(b) },
	MotionBlocking: func(b Block) bool {
		name, _ := StateOwner(b)
		return !NonSolid(b) || isFluid(name)
	},
	MotionBlockingNoLeaves: func(b Block) bool {
		name, _ := StateOwner(b)
		return !NonSolid(b) && !strings.Contains(name, "leaves")
	},
}

// ColumnHeights computes the four heightmaps of a column of sections
// (YZX order, height blocks tall): for each X-fastest column, the
// highest matching block's offset from the floor plus one, zero for
// none.
func ColumnHeights(blocks []Block, height int) (surface, ocean, motion, noLeaves [256]int32) {
	hp := HeightmapPredicates
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			col := z*16 + x
			for y := height - 1; y >= 0; y-- {
				b := blocks[(y*16+z)*16+x]
				if b == Air {
					continue
				}
				v := int32(y + 1)
				if surface[col] == 0 && hp.WorldSurface(b) {
					surface[col] = v
				}
				if ocean[col] == 0 && hp.OceanFloor(b) {
					ocean[col] = v
				}
				if motion[col] == 0 && hp.MotionBlocking(b) {
					motion[col] = v
				}
				if noLeaves[col] == 0 && hp.MotionBlockingNoLeaves(b) {
					noLeaves[col] = v
				}
				if motion[col] != 0 && surface[col] != 0 && noLeaves[col] != 0 && ocean[col] != 0 {
					break
				}
			}
		}
	}
	return
}
