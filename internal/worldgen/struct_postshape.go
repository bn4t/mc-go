package worldgen

import (
	"sort"

	"mc-go/internal/world"
)

// Post-processing of the shape-checked states structure pieces place
// (StructurePiece.SHAPE_CHECK_BLOCKS, marked by placeBlock):
// LevelChunk.postProcessGeneration runs Block.updateFromNeighbourShapes
// on every marked non-liquid cell when the chunk turns full and writes
// the result when it changed. Modeled here for the families those
// blocks belong to, measured from the bytecode:
//
//   - FenceBlock.updateShape: a horizontal side connects to a same-kind
//     fence (fences tag, matching wooden_fences membership), a fence
//     gate whose facing axis is perpendicular to the side
//     (FenceGateBlock.connectsToDirection), or a face sturdy toward it
//     on a block that is not an exception (Block.isExceptionForConnection:
//     leaves, barrier, pumpkins, melon, shulker boxes).
//   - IronBarsBlock.updateShape: attachsTo — sturdy face (non
//     exception), any IronBarsBlock or a wall.
//   - WallTorchBlock / LadderBlock.updateShape: the neighbour behind the
//     facing must keep a sturdy face toward it, else air;
//     BaseTorchBlock.updateShape: the block below must support its
//     center.
//
// Other marks keep their state (as before: neighbour shape updates of
// feature and carver marks are not modeled).

func connectionException(b world.Block) bool {
	n := blockName(b)
	return isLeaves(b) || n == "barrier" || n == "carved_pumpkin" || n == "jack_o_lantern" || n == "melon" || n == "pumpkin" || blockInTag(b, "shulker_boxes")
}

func horizontalSideName(d int) string { return dirNameOf[d] }

func dirAxisZ(d int) bool { return d == dirNorth || d == dirSouth }

// postShape is the updateShape of the post-processed families; ok is
// false for a state outside them.
func postShape(read func(BlockPos) world.Block, st world.Block, p BlockPos, d int) (world.Block, bool) {
	n := blockName(st)
	o := dirOffsets[d]
	nb := read(offset(p, o[0], o[1], o[2]))
	switch {
	case hasClass(n, "FenceBlock"):
		if d == dirUp || d == dirDown {
			return st, true
		}
		opp := oppositeDir(d)
		same := blockInTag(nb, "fences") && blockInTag(nb, "wooden_fences") == blockInTag(st, "wooden_fences")
		gate := false
		if hasClass(blockName(nb), "FenceGateBlock") {
			f, _ := world.StateProperty(nb, "facing")
			gate = dirAxisZ(dirNames[f]) == dirAxisZ(clockwise(opp))
		}
		c := !connectionException(nb) && sturdyFull(nb, opp) || same || gate
		return withProp(st, horizontalSideName(d), boolName(c)), true
	case hasClass(n, "IronBarsBlock"):
		if d == dirUp || d == dirDown {
			return st, true
		}
		opp := oppositeDir(d)
		c := !connectionException(nb) && sturdyFull(nb, opp) || hasClass(blockName(nb), "IronBarsBlock") || blockInTag(nb, "walls")
		return withProp(st, horizontalSideName(d), boolName(c)), true
	case hasClass(n, "WallTorchBlock"), hasClass(n, "LadderBlock"):
		f, _ := world.StateProperty(st, "facing")
		fd := dirNames[f]
		if oppositeDir(d) == fd {
			bo := dirOffsets[oppositeDir(fd)]
			if !sturdyFull(read(offset(p, bo[0], bo[1], bo[2])), fd) {
				return world.Air, true
			}
		}
		return st, true
	case hasClass(n, "BaseTorchBlock"):
		if d == dirDown && !sturdyCenter(read(offset(p, 0, -1, 0)), dirUp) {
			return world.Air, true
		}
		return st, true
	}
	return st, false
}

func boolName(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// PostProcessShapes applies the neighbour shape pass to chunk c's
// marked cells (per section, in marking order), reading neighbours
// from chunks.
func PostProcessShapes(c *ProtoChunk, chunks map[[2]int32]*ProtoChunk) {
	read := func(p BlockPos) world.Block {
		ch := chunks[[2]int32{p[0] >> 4, p[2] >> 4}]
		if ch == nil {
			return world.Air
		}
		return ch.Get(p[0]&15, p[1], p[2]&15)
	}
	marks := append([][3]int32(nil), c.PostProcess...)
	sort.SliceStable(marks, func(i, j int) bool { return floorDiv(marks[i][1], 16) < floorDiv(marks[j][1], 16) })
	for _, m := range marks {
		st := c.Get(m[0]&15, m[1], m[2]&15)
		if props(st)&(1<<bpLiquidBlock) != 0 {
			continue
		}
		cur := st
		handled := false
		for _, d := range updateShapeOrder {
			ns, ok := postShape(read, cur, m, d)
			if !ok {
				break
			}
			handled = true
			cur = ns
			if cur == world.Air {
				break
			}
		}
		if handled && cur != st {
			c.Set(m[0]&15, m[1], m[2]&15, cur)
		}
	}
}
