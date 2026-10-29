package worldgen

import (
	"encoding/json"

	"mc-go/internal/world"
)

// mangroveRootPlacer is MangroveRootPlacer (the only RootPlacer the
// 26.2 data uses) with its RootPlacer base: trunk offset, root provider
// and the optional above-root placement.
type mangroveRootPlacer struct {
	trunkOffsetY  intProvider
	rootProvider  stateProvider
	hasAbove      bool
	aboveProvider stateProvider
	aboveChance   float32
	growThrough   func(world.Block) bool
	muddyIn       func(world.Block) bool
	muddyProvider stateProvider
	maxWidth      int32
	maxLength     int32
	skew          float32
}

func parseRootPlacer(g *Generator, raw json.RawMessage) *mangroveRootPlacer {
	var o obj
	jsonUnmarshal(raw, &o)
	if t := trimNS(o.str("type")); t != "mangrove_root_placer" {
		panic("worldgen: unknown root placer " + t)
	}
	rp := &mangroveRootPlacer{
		trunkOffsetY: parseIntProvider(o["trunk_offset_y"]),
		rootProvider: g.compileStateProvider(o["root_provider"]),
	}
	if a, ok := o["above_root_placement"]; ok {
		var ao obj
		jsonUnmarshal(a, &ao)
		rp.hasAbove = true
		rp.aboveProvider = g.compileStateProvider(ao["above_root_provider"])
		rp.aboveChance = parseF32(ao["above_root_placement_chance"])
	}
	var m obj
	jsonUnmarshal(o["mangrove_root_placement"], &m)
	rp.growThrough = parseHolderBlocks(m["can_grow_through"])
	rp.muddyIn = parseHolderBlocks(m["muddy_roots_in"])
	rp.muddyProvider = g.compileStateProvider(m["muddy_roots_provider"])
	rp.maxWidth = m.i32("max_root_width")
	rp.maxLength = m.i32("max_root_length")
	rp.skew = parseF32(m["random_skew_chance"])
	return rp
}

// trunkOrigin is RootPlacer.getTrunkOrigin.
func (rp *mangroveRootPlacer) trunkOrigin(origin BlockPos, r Random) BlockPos {
	return offset(origin, 0, rp.trunkOffsetY(r), 0)
}

// canPlaceRoot is MangroveRootPlacer.canPlaceRoot.
func (rp *mangroveRootPlacer) canPlaceRoot(rg *Region, p BlockPos) bool {
	return validTreePos(rg, p) || rp.growThrough(blockAt(rg, p))
}

// waterlogged is RootPlacer.getPotentiallyWaterloggedState.
func waterloggedFrom(rg *Region, p BlockPos, b world.Block) world.Block {
	if !world.HasProperty(b, "waterlogged") {
		return b
	}
	v := "false"
	if inFluidTag(blockAt(rg, p), "water") {
		v = "true"
	}
	return withProp(b, "waterlogged", v)
}

// placeRoots is MangroveRootPlacer.placeRoots: the column from the
// origin up to the trunk must accept roots, then each horizontal side
// simulates a root run; all positions are placed only once every run
// succeeded.
func (rp *mangroveRootPlacer) placeRoots(t *treeCtx, origin, trunk BlockPos) bool {
	var list []BlockPos
	for m := origin; m[1] < trunk[1]; m = offset(m, 0, 1, 0) {
		if !rp.canPlaceRoot(t.rg, m) {
			return false
		}
	}
	list = append(list, offset(trunk, 0, -1, 0))
	for _, d := range horizontalDirs {
		p := relDir(trunk, d, 1)
		var run []BlockPos
		if !rp.simulate(t, p, d, trunk, &run, 0) {
			return false
		}
		list = append(list, run...)
		list = append(list, relDir(trunk, d, 1))
	}
	for _, p := range list {
		rp.placeRoot(t, p)
	}
	return true
}

func (rp *mangroveRootPlacer) simulate(t *treeCtx, p BlockPos, d int, trunk BlockPos, list *[]BlockPos, depth int32) bool {
	if depth == rp.maxLength || int32(len(*list)) > rp.maxLength {
		return false
	}
	for _, q := range rp.potential(p, d, t.r, trunk) {
		if rp.canPlaceRoot(t.rg, q) {
			*list = append(*list, q)
			if !rp.simulate(t, q, d, trunk, list, depth+1) {
				return false
			}
		}
	}
	return true
}

// potential is MangroveRootPlacer.potentialRootPositions.
func (rp *mangroveRootPlacer) potential(p BlockPos, d int, r Random, trunk BlockPos) []BlockPos {
	below := offset(p, 0, -1, 0)
	side := relDir(p, d, 1)
	dist := distManhattan(p, trunk)
	switch {
	case dist > rp.maxWidth-3 && dist <= rp.maxWidth:
		if r.NextFloat() < rp.skew {
			return []BlockPos{below, offset(side, 0, -1, 0)}
		}
		return []BlockPos{below}
	case dist > rp.maxWidth:
		return []BlockPos{below}
	case r.NextFloat() < rp.skew:
		return []BlockPos{below}
	case r.NextBool():
		return []BlockPos{side}
	}
	return []BlockPos{below}
}

// placeRoot is MangroveRootPlacer.placeRoot over RootPlacer.placeRoot.
func (rp *mangroveRootPlacer) placeRoot(t *treeCtx, p BlockPos) {
	if rp.muddyIn(blockAt(t.rg, p)) {
		b := rp.muddyProvider.getState(t.rg, t.r, p)
		t.setRoot(p, waterloggedFrom(t.rg, p, b))
		return
	}
	if !rp.canPlaceRoot(t.rg, p) {
		return
	}
	t.setRoot(p, waterloggedFrom(t.rg, p, rp.rootProvider.getState(t.rg, t.r, p)))
	if rp.hasAbove {
		up := offset(p, 0, 1, 0)
		if t.r.NextFloat() < rp.aboveChance && isAirAt(t.rg, up) {
			t.setRoot(up, waterloggedFrom(t.rg, up, rp.aboveProvider.getState(t.rg, t.r, up)))
		}
	}
}
