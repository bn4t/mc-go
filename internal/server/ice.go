package server

import (
	"strconv"

	"mc-go/internal/world"
)

// Ice melt, measured off the 26.2 jar (IceBlock):
//
//   - randomTick: getBrightness(LightLayer.BLOCK, pos) > 11 -
//     getLightDampening — ice dampens 1 (noOcclusion without
//     propagatesSkylightDown), so block light 11+ thaws it; sky
//     light plays no part — sunlit ice holds.
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
// can contribute more than level. No occlusion is modelled — a torch
// behind a wall still reads — so it is an upper bound on the real
// value.
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

// iceMeltRule is IceBlock.randomTick: block light over 10 (the
// 11 - dampening 1 gate) melts to liquid.
func iceMeltRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	if !s.blockLightAbove(d, pos, 10) {
		return
	}
	s.meltToLiquid(d, pos)
}

// meltToLiquid is IceBlock.melt: WATER_EVAPORATES (ultraWarm) leaves
// air; otherwise the cell becomes a water source.
func (s *Server) meltToLiquid(d *dimension, pos world.Pos) {
	if d.ultraWarm {
		s.setNaturalBlock(d, pos, world.Air)
		return
	}
	if w, ok := world.BlockStateOf("water", nil); ok {
		s.setNaturalBlock(d, pos, w)
	}
}

// FrostedIceBlock, measured off the 26.2 jar. The block is NOT
// random-ticking (its registration skips .randomTicks()); decay is a
// scheduled-tick chain plus a neighbour-collapse rule:
//
//   - onPlace: scheduleTick(pos, this, 60-120) — fires on every write
//     landing a frosted state, age bumps included.
//   - tick: when nextInt(3)==0 OR the cell has <4 frosted neighbours,
//     gate on brightness — the End reads block light only, elsewhere
//     maxLocalRawBrightness (max raw sky minus skyDarken, and block
//     light). i > 10 - age (dampening 1) slightly-melts: age<3 bumps
//     age by one (flag-2 write), age 3 melts to liquid. A full melt
//     cascades: each frosted neighbour is slightly-melted, and ones
//     that only aged get their own tick in 20-40. Every other exit
//     reschedules pos in 20-40.
//   - neighborChanged: when the notified block is frosted ice and the
//     cell keeps <2 frosted neighbours, it melts outright. Vanilla
//     notifies with the OLD block on gameplay writes (so sparse
//     frosted collapses as its neighbours depart) and with the NEW
//     block on command writes (updateNeighboursOnBlockSet — so a
//     setblock'd frosted collapses its sparse neighbours).
//
// The brightness half of getMaxLocalRawBrightness is approximated:
// sky contributes 15 under an all-light-passable column in a skylight
// dimension while the sun is up (skyDarken 0); dusk and night drop it
// below the 12 the gate needs.

// frostedIceOnPlace is the onPlace half — scheduled from the write
// paths so command placement ages too.
func (s *Server) frostedIceOnPlace(d *dimension, pos world.Pos, blk world.Block) {
	if name, _ := world.StateOwner(blk); name == "frosted_ice" {
		// Mth.nextInt(random, 60, 120) — inclusive.
		s.scheduleBlockTick(d, pos, int64(60+s.rng.intn(61)))
	}
}

// frostedCount is fewerNeigboursThan's count: frosted_ice on the six
// face neighbours of pos.
func frostedCount(d *dimension, pos world.Pos) int {
	n := 0
	for _, np := range neighbourPositions(pos) {
		if name, _ := world.StateOwner(d.w.GetBlock(np)); name == "frosted_ice" {
			n++
		}
	}
	return n
}

// frostedSkyLit returns 15 when raw sky light reaches pos in daylight,
// else 0 — the sky half of getMaxLocalRawBrightness.
func (s *Server) frostedSkyLit(d *dimension, pos world.Pos) int {
	if !d.skylight {
		return 0
	}
	dt := s.dayTime() % 24000
	if dt < 0 {
		dt += 24000
	}
	if dt >= 13000 && dt < 23000 {
		return 0
	}
	if !s.cropLit(d, pos) {
		return 0
	}
	return 15
}

// frostedTick is FrostedIceBlock.tick — the scheduled decay tick.
func (s *Server) frostedTick(d *dimension, pos world.Pos, blk world.Block) {
	age, _ := intProp(blk, "age")
	if s.rng.intn(3) == 0 || frostedCount(d, pos) < 4 {
		// End reads block light only; elsewhere maxLocalRawBrightness
		// is max(sky - skyDarken, block). sky is 15 under open sky in
		// daylight — always over the 11-age gate — so it collapses to
		// "exposed" vs the emitter scan. End has no skylight, so the
		// same check serves both branches.
		if s.frostedSkyLit(d, pos) == 15 || s.blockLightAbove(d, pos, 10-age) {
			if s.frostedSlightlyMelt(d, pos, blk) {
				for _, np := range neighbourPositions(pos) {
					nb := d.w.GetBlock(np)
					if name, _ := world.StateOwner(nb); name != "frosted_ice" {
						continue
					}
					if !s.frostedSlightlyMelt(d, np, nb) {
						s.scheduleBlockTick(d, np, int64(20+s.rng.intn(21)))
					}
				}
				return
			}
		}
	}
	s.scheduleBlockTick(d, pos, int64(20+s.rng.intn(21)))
}

// frostedSlightlyMelt is slightlyMelt: under age 3 the write bumps age
// (flag-2 — no neighbour pass) and reports false; at 3 the cell melts
// to liquid and reports true.
func (s *Server) frostedSlightlyMelt(d *dimension, pos world.Pos, blk world.Block) bool {
	age, _ := intProp(blk, "age")
	if age < 3 {
		if nb, ok := world.WithProperty(blk, "age", strconv.Itoa(age+1)); ok {
			s.setSilentBlock(d, pos, nb)
		}
		return false
	}
	s.meltToLiquid(d, pos)
	return true
}

// frostedNeighborChange is FrostedIceBlock.neighborChanged: when the
// block the notify passed is frosted ice and pos keeps fewer than 2
// frosted neighbours, pos melts outright (NEIGHBORS_TO_MELT). The
// notify argument is what vanilla hands the callback — the old state
// for gameplay writes, the new one for command writes.
func (s *Server) frostedNeighborChange(d *dimension, pos world.Pos, notify world.Block) {
	if name, _ := world.StateOwner(notify); name != "frosted_ice" {
		return
	}
	if frostedCount(d, pos) < 2 {
		s.meltToLiquid(d, pos)
	}
}
