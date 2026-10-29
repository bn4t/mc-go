package worldgen

import (
	"sync"

	"mc-go/internal/world"
)

// Bit layout of vanillaStateProps (tools/genblockprops).
const (
	bpAir uint64 = 1 << iota
	bpBlocksMotion
	bpSolid
	bpReplaceable
	bpLeaves
	bpBlockEntity
	bpFluid
	bpWater
	bpLava
	bpSource
)

const (
	bpAmountShift    = 10
	bpSturdyFull     = 14 // + direction index (down, up, north, south, west, east)
	bpSturdyCenter   = 20
	bpSturdyRigid    = 26
	bpFullCollision  = 32
	bpRandomTicks    = 33
	bpSkyDown        = 34
	bpConductor      = 35
	bpPostProcess    = 36
	bpSolidRender    = 37
	bpCanOcclude     = 38
	bpLiquidBlock    = 39
	bpEmissionShift  = 40
	bpDampeningShift = 44
	bpCollisionFace  = 48 // + direction index: Block.isFaceFull(collision shape, dir)
	bpNoCollision    = 54 // the collision shape is empty
)

// Directions in the sturdy-face bit order.
const (
	dirDown = iota
	dirUp
	dirNorth
	dirSouth
	dirWest
	dirEast
)

var propsByBlock = sync.OnceValue(func() []uint64 {
	m, ok := world.BlockMapping(776)
	if !ok {
		panic("worldgen: no protocol 776 block mapping")
	}
	n := world.StateCount()
	out := make([]uint64, n)
	for i := 0; i < n; i++ {
		id, ok := m.Lookup(world.Block(i))
		if ok && int(id) < len(vanillaStateProps) {
			out[i] = vanillaStateProps[id]
		}
	}
	return out
})

func props(b world.Block) uint64 {
	t := propsByBlock()
	if int(b) < len(t) {
		return t[b]
	}
	return 0
}

func isAir(b world.Block) bool         { return props(b)&bpAir != 0 }
func isSolidState(b world.Block) bool  { return props(b)&bpSolid != 0 }
func isReplaceable(b world.Block) bool { return props(b)&bpReplaceable != 0 }
func isLeaves(b world.Block) bool      { return props(b)&bpLeaves != 0 }
func hasFluid(b world.Block) bool      { return props(b)&bpFluid != 0 }
func isWaterFluid(b world.Block) bool  { return props(b)&bpWater != 0 }
func isLavaFluid(b world.Block) bool   { return props(b)&bpLava != 0 }
func isFluidSource(b world.Block) bool { return props(b)&bpSource != 0 }
func hasBlockEntity(b world.Block) bool {
	return props(b)&bpBlockEntity != 0
}
func fluidAmount(b world.Block) int32 { return int32(props(b) >> bpAmountShift & 15) }
func sturdyFull(b world.Block, dir int) bool {
	return props(b)&(1<<(bpSturdyFull+dir)) != 0
}
func sturdyCenter(b world.Block, dir int) bool {
	return props(b)&(1<<(bpSturdyCenter+dir)) != 0
}
func sturdyRigid(b world.Block, dir int) bool {
	return props(b)&(1<<(bpSturdyRigid+dir)) != 0
}
func collisionFaceFull(b world.Block, dir int) bool {
	return props(b)&(1<<(bpCollisionFace+dir)) != 0
}
func fullCollision(b world.Block) bool  { return props(b)&(1<<bpFullCollision) != 0 }
func hasPostProcess(b world.Block) bool { return props(b)&(1<<bpPostProcess) != 0 }
func lightEmission(b world.Block) int32 { return int32(props(b) >> bpEmissionShift & 15) }

// blocksMotionState is BlockState.blocksMotion.
func blocksMotionState(b world.Block) bool { return props(b)&bpBlocksMotion != 0 }
