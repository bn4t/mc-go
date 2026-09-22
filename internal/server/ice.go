package server

import (
	"mc-go/internal/world"
)

// Ice melt, measured off the 26.2 jar (IceBlock):
//
//   - randomTick: getBrightness(LightLayer.BLOCK, pos) > 11 -
//     getLightDampening (ice dampens 0) → melt(). So block light 12+
//     thaws ice; sky light plays no part — sunlit ice holds.
//   - melt(): WATER_EVAPORATES (ultraWarm) → removeBlock = air;
//     otherwise setBlockAndUpdate to a water source.
//   - playerDestroy also melts — covered by the dig path elsewhere.
//
// mc-go has no light engine, so blockLightAbove scans the emitters
// that can matter: a light value above `level` needs an emitter of
// emission E at taxicab distance < E-level — the table only lists
// the usual sources; the scan bound keeps it cheap.

// blockLightEmission is a curated luminance table (vanilla
// getLightEmission). States matter: furnaces, campfires and candles
// only emit while lit.
var blockLightEmission = map[string]int{
	"beacon": 15, "conduit": 15, "end_gateway": 15,
	"fire":            15,
	"glowstone":       15,
	"jack_o_lantern":  15,
	"lantern":         15,
	"lava":            15,
	"ochre_froglight": 15, "pearlescent_froglight": 15, "verdant_froglight": 15,
	"sea_lantern": 15, "shroomlight": 15,
	"end_rod": 14, "torch": 14, "wall_torch": 14,
	"crying_obsidian": 10, "soul_campfire": 10, "soul_fire": 10,
	"soul_lantern": 10, "soul_torch": 10, "soul_wall_torch": 10,
	"glow_lichen": 7, "redstone_torch": 7, "redstone_wall_torch": 7,
	"ender_chest": 7, "large_amethyst_bud": 4, "amethyst_cluster": 5,
	"medium_amethyst_bud": 3, "small_amethyst_bud": 1, "magma_block": 3,
	"dragon_egg": 1, "brewing_stand": 1, "brown_mushroom": 1,
}

// litEmission handles light gated on a `lit` state prop.
var litBlockLightEmission = map[string]int{
	"campfire": 15, "furnace": 13, "blast_furnace": 13, "smoker": 13,
	"redstone_lamp": 15, "copper_bulb": 15, "exposed_copper_bulb": 15,
	"oxidized_copper_bulb": 15, "weathered_copper_bulb": 15,
	"candle": 12, "white_candle": 12,
}

// lightEmit returns a block state's block-light emission.
func lightEmit(b world.Block) int {
	name, ok := world.StateOwner(b)
	if !ok {
		return 0
	}
	if lit, hasLit := world.StateProperty(b, "lit"); hasLit && lit == "false" {
		return 0
	}
	if e, ok := litBlockLightEmission[name]; ok {
		return e
	}
	if e, ok := blockLightEmission[name]; ok {
		return e
	}
	return 0
}

// blockLightAbove reports whether the block light at pos tops level —
// scanning emitters within E-level cells of pos, the only ones that
// can contribute more than level.
func (s *Server) blockLightAbove(d *dimension, pos world.Pos, level int) bool {
	r := 15 - level
	for dx := -r; dx <= r; dx++ {
		for dy := -r; dy <= r; dy++ {
			for dz := -r; dz <= r; dz++ {
				m := absi(dx) + absi(dy) + absi(dz)
				if m > r {
					continue
				}
				e := lightEmit(s.rtBlock(d, world.Pos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}))
				if e-m > level {
					return true
				}
			}
		}
	}
	return false
}

func absi(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// iceMeltRule is IceBlock.randomTick.
func iceMeltRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	if !s.blockLightAbove(d, pos, 11) {
		return
	}
	if d.ultraWarm {
		// WATER_EVAPORATES: melt removes the block outright.
		s.setNaturalBlock(d, pos, world.Air)
		return
	}
	if w, ok := world.BlockStateOf("water", nil); ok {
		s.setNaturalBlock(d, pos, w)
	}
}
