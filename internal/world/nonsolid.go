package world

// Which blocks a fluid flows into, and which a heightmap looks through.
//
// One set serves both questions, because vanilla's two rules agree on
// everything that matters here: a fluid washes away what has no
// collision, and the heightmaps that are supposed to find the ground
// stop at what has collision. The set below is the "washed away" half
// - the plants, torches, rails and thin growth a fluid replaces - and
// NonSolid adds air, which has no state of its own to look up.

// NonSolid reports whether a block is transparent to a fluid and to a
// motion-blocking heightmap: air, and the decoration blocks with no
// collision box that a fluid destroys.
func NonSolid(b Block) bool {
	if b == Air {
		return true
	}
	name, ok := StateOwner(b)
	if !ok {
		return false
	}
	return nonSolidBlocks[name]
}

// nonSolidBlocks are the blocks a fluid flows into and destroys.
// Waterlogged blocks are handled separately (they keep their block and
// just take the fluid); this is the "washed away" set.
var nonSolidBlocks = map[string]bool{
	"air": true, "cave_air": true, "void_air": true,
	"short_grass": true, "tall_grass": true, "fern": true, "large_fern": true,
	"dead_bush": true, "dandelion": true, "poppy": true, "cornflower": true,
	"azure_bluet": true, "oxeye_daisy": true, "allium": true, "blue_orchid": true,
	"lily_of_the_valley": true, "sunflower": true, "lilac": true, "rose_bush": true,
	"peony": true, "red_tulip": true, "orange_tulip": true, "white_tulip": true,
	"pink_tulip": true, "torchflower": true, "pitcher_plant": true,
	"wheat": true, "carrots": true, "potatoes": true, "beetroots": true,
	"oak_sapling": true, "birch_sapling": true, "spruce_sapling": true,
	"jungle_sapling": true, "acacia_sapling": true, "dark_oak_sapling": true,
	"cherry_sapling": true, "pale_oak_sapling": true, "mangrove_propagule": true,
	"sugar_cane": true, "kelp": true, "kelp_plant": true, "seagrass": true,
	"tall_seagrass": true, "snow": true, "fire": true, "soul_fire": true,
	"nether_sprouts": true, "crimson_roots": true, "warped_roots": true,
	"crimson_fungus": true, "warped_fungus": true, "brown_mushroom": true,
	"red_mushroom": true, "sweet_berry_bush": true, "glow_lichen": true,
	"sculk_vein": true, "vine": true, "hanging_roots": true, "big_dripleaf": true,
	"small_dripleaf": true, "azalea": true, "flowering_azalea": true,
	"moss_carpet": true, "pink_petals": true, "spore_blossom": true,
	"cave_vines": true, "cave_vines_plant": true, "weeping_vines": true,
	"weeping_vines_plant": true, "twisting_vines": true, "twisting_vines_plant": true,
	"nether_wart": true, "torch": true, "soul_torch": true, "redstone_torch": true,
	"redstone_wire": true, "lever": true, "tripwire": true, "tripwire_hook": true,
	"string": true, "rail": true, "powered_rail": true, "detector_rail": true,
	"activator_rail": true, "red_mushroom_block": false,
}
