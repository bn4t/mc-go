package server

import (
	"strconv"
	"strings"

	"mc-go/internal/world"
)

// cellBox is one collision box inside a block cell, coordinates in
// [0,1] measured from the cell's min corner — a row of a VoxelShape's
// AABB list.
type cellBox [6]float64

// collisionBoxesOf decomposes a block state into its collision boxes
// — the blocks whose getCollisionShape isn't the unit cube, measured
// from the 26.2 decompiled constants (SlabBlock.column(16,0,8),
// CrossCollisionBlock.makeShapes(4,16,4,16,24) for fences, and so
// on). The second return is false when the block isn't in the table
// (the caller then treats it as the full unit cube); a true result
// with an empty box list means vanilla's collision shape is empty —
// powder_snow and the open fence gate are the live cases.
func collisionBoxesOf(blk world.Block) ([]cellBox, bool) {
	name, ok := world.StateOwner(blk)
	if !ok {
		return nil, false
	}
	short := strings.TrimPrefix(name, "minecraft:")
	prop := func(k string) string {
		v, _ := world.StateProperty(blk, k)
		return v
	}

	topPlate := func(top float64) []cellBox {
		return []cellBox{{0, 0, 0, 1, top, 1}}
	}

	switch {
	case strings.HasSuffix(short, "_slab"):
		switch prop("type") {
		case "bottom":
			return topPlate(0.5), true
		case "top":
			return []cellBox{{0, 0.5, 0, 1, 1, 1}}, true
		}
		return nil, false // double

	case strings.HasSuffix(short, "_stairs"):
		// StairBlock: the stair's HALF is a full-width plate and the
		// other half carries quarter boxes on the shape's arms —
		// SHAPE_BOTTOM_* is (column(16,0,8) | quarter rows) rotated
		// to the lookup direction; SHAPE_TOP_* is the same inverted.
		return stairBoxes(prop("facing"), prop("half"), prop("shape")), true

	case short == "snow":
		n, err := strconv.Atoi(prop("layers"))
		if err != nil || n <= 0 {
			return nil, false
		}
		if n >= 8 {
			return nil, false // snow_block is a separate block
		}
		return topPlate(float64(n) * 0.125), true

	case strings.HasSuffix(short, "_trapdoor"):
		if prop("open") == "true" {
			// boxZ(16,13,16) rotated to FACING: a 3/16 plate on the
			// facing edge, only 13/16 tall.
			return []cellBox{edgePlate(prop("facing"), 0.1875, 0.8125)}, true
		}
		if prop("half") == "top" {
			return []cellBox{{0, 0.8125, 0, 1, 1, 1}}, true
		}
		return topPlate(0.1875), true

	case strings.HasSuffix(short, "_door"):
		// DoorBlock.boxZ(16,13,16): the closed plate sits on the
		// facing edge; opening swings it onto the hinge edge —
		// hinge=right turns counterclockwise, left clockwise.
		facing := prop("facing")
		if prop("open") == "true" {
			if prop("hinge") == "right" {
				facing = dirCCW(facing)
			} else {
				facing = dirCW(facing)
			}
		}
		return []cellBox{edgePlate(facing, 0.1875, 0.8125)}, true

	case strings.HasSuffix(short, "_fence_gate"):
		if prop("open") == "true" {
			return nil, true // getCollisionShape → Shapes.empty()
		}
		// column(16,4,0,24) on the facing axis: a 1.5-high plate
		// 4/16 thick across the gate span — in_wall drops the top
		// to 21/16 (Z_COLLISION_SHAPE_SHORT).
		top := 1.5
		if prop("in_wall") == "true" {
			top = 1.3125
		}
		if prop("facing") == "north" || prop("facing") == "south" {
			return []cellBox{{0, 0, 0.375, 1, top, 0.625}}, true
		}
		return []cellBox{{0.375, 0, 0, 0.625, top, 1}}, true

	case strings.HasSuffix(short, "_fence"):
		// FenceBlock collision makeShapes(4,24,4,0,24): center post
		// column(4,0,24) plus a 4/16-wide 1.5-high arm per connected
		// side.
		boxes := []cellBox{{0.375, 0, 0.375, 0.625, 1.5, 0.625}}
		for _, d := range [4]string{"north", "south", "east", "west"} {
			if prop(d) == "true" {
				boxes = append(boxes, crossArm(d, 0.375, 1.5, 0.5))
			}
		}
		return boxes, true

	case strings.HasSuffix(short, "_wall"):
		// WallBlock.collisionShapes = makeShapes(24,24): up post
		// column(8,0,24) and 6/16-wide 1.5-high arms reaching
		// z∈[0,0.6875] per non-none side.
		var boxes []cellBox
		if prop("up") == "true" {
			boxes = append(boxes, cellBox{0.25, 0, 0.25, 0.75, 1.5, 0.75})
		}
		for _, d := range [4]string{"north", "south", "east", "west"} {
			if prop(d) != "" && prop(d) != "none" {
				boxes = append(boxes, crossArm(d, 0.3125, 1.5, 0.6875))
			}
		}
		return boxes, true

	case short == "iron_bars" || strings.HasSuffix(short, "_pane"):
		// IronBarsBlock collision makeShapes(2,16,2,0,16): the center
		// node column(2,0,16) and 2/16 plates to each connected edge.
		boxes := []cellBox{{0.4375, 0, 0.4375, 0.5625, 1, 0.5625}}
		for _, d := range [4]string{"north", "south", "east", "west"} {
			if prop(d) == "true" {
				boxes = append(boxes, crossArm(d, 0.4375, 1, 0.5))
			}
		}
		return boxes, true

	case short == "hopper":
		// HopperBlock: floor plate + four rim walls.
		return []cellBox{
			{0, 0, 0, 1, 0.125, 1},
			{0, 0.125, 0, 1, 1, 0.125},
			{0, 0.125, 0.875, 1, 1, 1},
			{0, 0.125, 0, 0.125, 1, 1},
			{0.875, 0.125, 0, 1, 1, 1},
		}, true

	case strings.HasSuffix(short, "cauldron"):
		// CauldronBlock: a 5/16 floor and 2/16 walls above it.
		return []cellBox{
			{0, 0, 0, 1, 0.3125, 1},
			{0, 0.3125, 0, 1, 1, 0.125},
			{0, 0.3125, 0.875, 1, 1, 1},
			{0, 0.3125, 0, 0.125, 1, 1},
			{0.875, 0.3125, 0, 1, 1, 1},
		}, true

	case short == "composter":
		return []cellBox{
			{0, 0, 0, 1, 0.125, 1},
			{0, 0.125, 0, 1, 1, 0.125},
			{0, 0.125, 0.875, 1, 1, 1},
			{0, 0.125, 0, 0.125, 1, 1},
			{0.875, 0.125, 0, 1, 1, 1},
		}, true

	case short == "cake":
		// CakeBlock: box(1,0,1, 15-2·bites, 8, 15).
		bites, _ := strconv.Atoi(prop("bites"))
		if bites < 0 || bites > 6 {
			bites = 0
		}
		return []cellBox{{0.0625, 0, 0.0625, (15 - 2*float64(bites)) / 16, 0.5, 0.9375}}, true

	case short == "anvil" || short == "chipped_anvil" || short == "damaged_anvil":
		// AnvilBlock: base column(12,0,4) + step column(8,10,4,5) +
		// neck column(4,8,5,10) + top column(10,16,10,16) on the
		// facing axis.
		boxes := []cellBox{
			{0.125, 0, 0.125, 0.875, 0.25, 0.875},
			{0.25, 0.25, 0.1875, 0.75, 0.3125, 0.8125},
			{0.375, 0.3125, 0.25, 0.625, 0.625, 0.75},
			{0.1875, 0.625, 0.1875, 0.8125, 1, 0.8125},
		}
		if prop("facing") == "east" || prop("facing") == "west" {
			for i, b := range boxes {
				boxes[i] = cellBox{b[2], b[1], b[0], b[5], b[4], b[3]}
			}
		}
		return boxes, true

	case short == "lantern" || short == "soul_lantern":
		// LanternBlock: column(6,0,7) base + column(4,7,9) top;
		// hanging shifts both up 1/16.
		dy := 0.0
		if prop("hanging") == "true" {
			dy = 0.0625
		}
		return []cellBox{
			{0.3125, dy, 0.3125, 0.6875, 0.4375 + dy, 0.6875},
			{0.375, 0.4375 + dy, 0.375, 0.625, 0.5625 + dy, 0.625},
		}, true

	case short == "chain" || short == "end_rod" || short == "lightning_rod":
		// Vertical axis rods: a centered 0.375..0.625 (chain 0.40625..
		// 0.59375) column; sideways rods lie along their axis.
		half := 0.1875
		axis := prop("facing")
		if short == "chain" {
			half = 0.09375
			axis = prop("axis") // ChainBlock orients by axis, not facing
		}
		switch axis {
		case "north", "south", "z":
			return []cellBox{{0.5 - half, 0.5 - half, 0, 0.5 + half, 0.5 + half, 1}}, true
		case "east", "west", "x":
			return []cellBox{{0, 0.5 - half, 0.5 - half, 1, 0.5 + half, 0.5 + half}}, true
		case "down":
			return []cellBox{{0.5 - half, 0, 0.5 - half, 0.5 + half, 0.5, 0.5 + half}}, true
		}
		return []cellBox{{0.5 - half, 0, 0.5 - half, 0.5 + half, 1, 0.5 + half}}, true

	case short == "scaffolding":
		// ScaffoldingBlock: top plate + 4 corner posts; bottom=
		// unstable adds a floor plate and edge skirts.
		boxes := []cellBox{
			{0, 0.875, 0, 1, 1, 1},
			{0, 0, 0, 0.125, 1, 0.125}, {0.875, 0, 0, 1, 1, 0.125},
			{0, 0, 0.875, 0.125, 1, 1}, {0.875, 0, 0.875, 1, 1, 1},
		}
		if prop("bottom") == "true" {
			boxes = append(boxes,
				cellBox{0, 0, 0, 1, 0.125, 1},
				cellBox{0, 0, 0, 1, 0.125, 0.125},
				cellBox{0, 0, 0.875, 1, 0.125, 1},
				cellBox{0, 0, 0, 0.125, 0.125, 1},
				cellBox{0.875, 0, 0, 1, 0.125, 1})
		}
		return boxes, true

	case short == "powder_snow":
		// PowderSnowBlock's collision is empty — the ray passes
		// through like a fluid.
		return nil, true

	case short == "chest" || short == "trapped_chest" || short == "ender_chest":
		return []cellBox{{0.0625, 0, 0.0625, 0.9375, 0.875, 0.9375}}, true

	case short == "turtle_egg":
		// TurtleEggBlock: SHAPE_SINGLE box(3,0,7,12,12,14),
		// SHAPE_MULTIPLE column(14,7,12) → box(1,0,2,15,7,14).
		if prop("eggs") == "1" {
			return []cellBox{{0.1875, 0, 0.4375, 0.75, 0.75, 0.875}}, true
		}
		return []cellBox{{0.0625, 0, 0.125, 0.9375, 0.4375, 0.875}}, true

	case short == "bell":
		// BellBlock: the bell body box(4,4,4,12,12,12)/16 plus a
		// 1/16 rim plate at its base.
		return []cellBox{
			{0.25, 0.25, 0.25, 0.75, 0.75, 0.75},
			{0.1875, 0.1875, 0.1875, 0.8125, 0.25, 0.8125},
		}, true
	}

	// Full-width partial-height blocks — the low-collision floors.
	switch short {
	case "honey_block", "farmland", "dirt_path":
		return topPlate(0.9375), true
	case "soul_sand", "mud":
		return topPlate(0.875), true
	case "enchanting_table":
		return topPlate(0.75), true
	case "end_portal_frame":
		return topPlate(0.8125), true
	case "daylight_detector":
		return topPlate(0.375), true
	case "campfire", "soul_campfire":
		return topPlate(0.4375), true
	case "stonecutter":
		return topPlate(0.5625), true
	case "lily_pad":
		return []cellBox{{0.0625, 0, 0.0625, 0.9375, 0.09375, 0.9375}}, true
	}
	if strings.HasSuffix(short, "_bed") {
		return topPlate(0.5625), true
	}
	if strings.HasSuffix(short, "_carpet") {
		return topPlate(0.0625), true
	}
	return nil, false
}

// edgePlate is a vertical plate `thick` deep on the named cell edge,
// spanning y∈[y0,y1] — the trapdoor's open panel and the door's
// closed plate are both this shape at 3/16.
func edgePlate(dir string, thick, y1 float64) cellBox {
	switch dir {
	case "north":
		return cellBox{0, 0, 0, 1, y1, thick}
	case "south":
		return cellBox{0, 0, 1 - thick, 1, y1, 1}
	case "west":
		return cellBox{0, 0, 0, thick, y1, 1}
	default: // east
		return cellBox{1 - thick, 0, 0, 1, y1, 1}
	}
}

// crossArm is a CrossCollisionBlock/WallBlock arm — a `half`-wide
// vertical strip from the cell edge inward to `reach`, `top` high.
func crossArm(dir string, half, top, reach float64) cellBox {
	switch dir {
	case "north":
		return cellBox{0.5 - half, 0, 0, 0.5 + half, top, reach}
	case "south":
		return cellBox{0.5 - half, 0, 1 - reach, 0.5 + half, top, 1}
	case "east":
		return cellBox{1 - reach, 0, 0.5 - half, 1, top, 0.5 + half}
	default: // west
		return cellBox{0, 0, 0.5 - half, reach, top, 0.5 + half}
	}
}

// stairBoxes decomposes *_stairs: the stair's named half is a full
// plate and the other half carries the quarter boxes of the SHAPE
// (straight / inner_left / inner_right / outer_left / outer_right)
// relative to FACING.
func stairBoxes(facing, half, shape string) []cellBox {
	// lo spans the named direction's horizontal half of a box.
	lo := func(dir string, b cellBox) cellBox {
		switch dir {
		case "north":
			b[5] = 0.5
		case "south":
			b[2] = 0.5
		case "west":
			b[3] = 0.5
		case "east":
			b[0] = 0.5
		}
		return b
	}
	var plate cellBox
	var quarters []string
	if half == "bottom" {
		plate = cellBox{0, 0, 0, 1, 0.5, 1}
		switch shape {
		case "straight":
			quarters = []string{facing}
		case "inner_left":
			quarters = []string{facing, dirCCW(facing)}
		case "inner_right":
			quarters = []string{facing, dirCW(facing)}
		case "outer_left":
			// corner at facing ∩ CCW(facing)
			return cornerStair(plate, facing, dirCCW(facing), 0.5)
		case "outer_right":
			return cornerStair(plate, facing, dirCW(facing), 0.5)
		}
		boxes := []cellBox{plate}
		for _, d := range quarters {
			boxes = append(boxes, lo(d, cellBox{0, 0.5, 0, 1, 1, 1}))
		}
		return boxes
	}
	plate = cellBox{0, 0.5, 0, 1, 1, 1}
	switch shape {
	case "straight":
		quarters = []string{facing}
	case "inner_left":
		quarters = []string{facing, dirCCW(facing)}
	case "inner_right":
		quarters = []string{facing, dirCW(facing)}
	case "outer_left":
		return cornerStair(plate, facing, dirCCW(facing), 0)
	case "outer_right":
		return cornerStair(plate, facing, dirCW(facing), 0)
	}
	boxes := []cellBox{plate}
	for _, d := range quarters {
		boxes = append(boxes, lo(d, cellBox{0, 0, 0, 1, 0.5, 1}))
	}
	return boxes
}

// cornerStair is the outer-shape stair: the full half plate plus a
// single quarter-quarter at the (facing, side) corner of the other
// half — y0 is that half's floor.
func cornerStair(plate cellBox, facing, side string, y0 float64) []cellBox {
	q := cellBox{0, y0, 0, 1, y0 + 0.5, 1}
	switch facing {
	case "north":
		q[5] = 0.5
	case "south":
		q[2] = 0.5
	case "west":
		q[3] = 0.5
	case "east":
		q[0] = 0.5
	}
	switch side {
	case "north":
		q[5] = 0.5
	case "south":
		q[2] = 0.5
	case "west":
		q[3] = 0.5
	case "east":
		q[0] = 0.5
	}
	return []cellBox{plate, q}
}
