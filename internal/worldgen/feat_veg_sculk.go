package worldgen

import (
	"math"

	"mc-go/internal/world"
)

// sculk_patch (SculkPatchFeature): charge cursors seeded at the origin
// walk the SculkSpreader rules — veins spread over faces, veins turn
// their supporting blocks to sculk, sculk sprouts sensors and shriekers
// — then a catalyst and rare shriekers finish the patch.

// The world-generation spreader (SculkSpreader.createWorldGenSpreader).
const (
	sculkGrowthSpawnCost = 50
	sculkNoGrowthRadius  = 1
	sculkChargeDecayRate = 5
	sculkExtraDecayRate  = 10
	sculkMaxCursors      = 32
)

type sculkCursor struct {
	pos         BlockPos
	charge      int32
	updateDelay int32
	decayDelay  int32
	facings     []int // nil: unknown (fresh cursor); empty: none
}

// sculkNonCornerNeighbours is ChargeCursor.NON_CORNER_NEIGHBOURS:
// BlockPos.betweenClosed(-1..1) order (x fastest, then y, then z)
// keeping offsets with a zero coordinate, origin excluded.
var sculkNonCornerNeighbours = func() []BlockPos {
	var out []BlockPos
	for z := int32(-1); z <= 1; z++ {
		for y := int32(-1); y <= 1; y++ {
			for x := int32(-1); x <= 1; x++ {
				if (x == 0 || y == 0 || z == 0) && !(x == 0 && y == 0 && z == 0) {
					out = append(out, BlockPos{x, y, z})
				}
			}
		}
	}
	return out
}()

var sculkVein = multifaceKind{block: "sculk_vein", sculk: true}

func init() {
	sculkVein.base = blockNamed("sculk_vein")
	featureBuilders["sculk_patch"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		chargeCount := cfg.i32("charge_count")
		perCharge := cfg.i32("amount_per_charge")
		spreadAttempts := cfg.i32("spread_attempts")
		growthRounds := cfg.i32("growth_rounds")
		spreadRounds := cfg.i32("spread_rounds")
		extra := parseIntProvider(cfg["extra_rare_growths"])
		catalyst := parseF32(cfg["catalyst_chance"])
		return func(c *featCtx, r Random, p BlockPos) bool {
			rg := c.rg
			if !sculkCanSpreadFrom(rg, p) {
				return false
			}
			var cursors []*sculkCursor
			for round := int32(0); round < spreadRounds+growthRounds; round++ {
				for i := int32(0); i < chargeCount; i++ {
					for n := perCharge; n > 0; {
						k := min(n, 1000)
						if len(cursors) < sculkMaxCursors {
							cursors = append(cursors, &sculkCursor{pos: p, charge: k, decayDelay: 1})
						}
						n -= k
					}
				}
				veins := round < spreadRounds
				for i := int32(0); i < spreadAttempts; i++ {
					// SculkSpreader.updateCursors: world generation never
					// merges cursors; spent ones drop out.
					var kept []*sculkCursor
					for _, cu := range cursors {
						if max(absI(cu.pos[0]-p[0]), absI(cu.pos[1]-p[1]), absI(cu.pos[2]-p[2])) > 1024 {
							continue
						}
						cu.update(rg, p, r, veins)
						if cu.charge > 0 {
							kept = append(kept, cu)
						}
					}
					cursors = kept
				}
				cursors = nil
			}
			if r.NextFloat() <= catalyst && fullCollision(rg.Block(p[0], p[1]-1, p[2])) {
				rg.SetBlock(p[0], p[1], p[2], blockNamed("sculk_catalyst"))
			}
			n := extra(r)
			shrieker := carpetWith(blockNamed("sculk_shrieker"), "can_summon", "true")
			for i := int32(0); i < n; i++ {
				dx := r.NextIntN(5) - 2
				dz := r.NextIntN(5) - 2
				q := offset(p, dx, 0, dz)
				if isAir(rg.Block(q[0], q[1], q[2])) && sturdyFull(rg.Block(q[0], q[1]-1, q[2]), dirUp) {
					rg.SetBlock(q[0], q[1], q[2], shrieker)
				}
			}
			return true
		}
	}
}

// sculkCanSpreadFrom is SculkPatchFeature.canSpreadFrom.
func sculkCanSpreadFrom(rg *Region, p BlockPos) bool {
	b := rg.Block(p[0], p[1], p[2])
	if isSculkBehaviour(b) {
		return true
	}
	if !isAir(b) && !(isBlock(b, "water") && isFluidSource(b)) {
		return false
	}
	for d := 0; d < 6; d++ {
		o := dirOffsets[d]
		if fullCollision(rg.Block(p[0]+o[0], p[1]+o[1], p[2]+o[2])) {
			return true
		}
	}
	return false
}

func isSculkBehaviour(b world.Block) bool { return isBlock(b, "sculk") || isBlock(b, "sculk_vein") }

// update is ChargeCursor.update.
func (cu *sculkCursor) update(rg *Region, origin BlockPos, r Random, veins bool) {
	if cu.charge <= 0 {
		return
	}
	if cu.updateDelay > 0 {
		cu.updateDelay--
		return
	}
	st := rg.Block(cu.pos[0], cu.pos[1], cu.pos[2])
	isSculk, isVein := isBlock(st, "sculk"), isBlock(st, "sculk_vein")
	if veins && sculkAttemptSpreadVein(rg, cu.pos, st, cu.facings, isSculk, isVein) {
		// canChangeBlockStateOnSpread: false for sculk.
		if !isSculk {
			st = rg.Block(cu.pos[0], cu.pos[1], cu.pos[2])
			isSculk, isVein = isBlock(st, "sculk"), isBlock(st, "sculk_vein")
		}
	}
	switch {
	case isSculk:
		cu.charge = sculkUseCharge(rg, cu, origin, r)
	case isVein:
		cu.charge = veinUseCharge(rg, cu, r, veins)
	default:
		if cu.decayDelay <= 0 {
			cu.charge = 0
		}
	}
	if cu.charge <= 0 {
		if isVein {
			veinDischarged(rg, st, cu.pos)
		}
		return
	}
	if np, ok := sculkMovement(rg, cu.pos, r); ok {
		if isVein {
			veinDischarged(rg, st, cu.pos)
		}
		cu.pos = np
		dx, dz := float64(np[0]-origin[0]), float64(np[2]-origin[2])
		if dx*dx+dz*dz >= 225 {
			cu.charge = 0
			return
		}
		ns := rg.Block(np[0], np[1], np[2])
		if isSculkBehaviour(ns) {
			cu.facings = availableFaces(ns)
		}
	} else if isSculkBehaviour(st) {
		cu.facings = availableFaces(st)
	}
	// updateDecayDelay / getSculkSpreadDelay of the behaviour the cursor
	// acted with (before moving): non-sculk blocks count the delay down.
	if !isSculk && !isVein {
		cu.decayDelay = max(cu.decayDelay-1, 0)
	} else {
		cu.decayDelay = 1
	}
	cu.updateDelay = 1
}

// availableFaces is MultifaceBlock.availableFaces (empty for a block
// that is not multiface).
func availableFaces(b world.Block) []int {
	out := []int{}
	if !isBlock(b, "sculk_vein") {
		return out
	}
	for d := 0; d < 6; d++ {
		if multifaceHas(b, d) {
			out = append(out, d)
		}
	}
	return out
}

// sculkAttemptSpreadVein is SculkBehaviour.attemptSpreadVein (DEFAULT's
// override for non-sculk blocks).
func sculkAttemptSpreadVein(rg *Region, p BlockPos, st world.Block, facings []int, isSculk, isVein bool) bool {
	if isSculk || isVein {
		return sculkVein.spreadAll(rg, st, p, false) > 0
	}
	if facings == nil {
		return sculkVein.spreadAll(rg, rg.Block(p[0], p[1], p[2]), p, true) > 0
	}
	if len(facings) > 0 {
		// FluidState.is(Fluids.WATER): a water source.
		if isAir(st) || (isWaterFluid(st) && isFluidSource(st)) {
			return veinRegrow(rg, p, st, facings)
		}
		return false
	}
	return sculkVein.spreadAll(rg, st, p, false) > 0
}

// veinRegrow is SculkVeinBlock.regrow.
func veinRegrow(rg *Region, p BlockPos, st world.Block, facings []int) bool {
	ns := sculkVein.base
	any := false
	for _, d := range facings {
		if multifaceCanAttach(rg, p, d) {
			ns = carpetWith(ns, multifaceFaceProps[d], "true")
			any = true
		}
	}
	if !any {
		return false
	}
	if hasFluid(st) {
		ns = carpetWith(ns, "waterlogged", "true")
	}
	rg.SetBlock(p[0], p[1], p[2], ns)
	return true
}

// spreadAll is MultifaceSpreader.spreadAll for the sculk vein spreaders
// (sameSpace: SAME_POSITION only).
func (m multifaceKind) spreadAll(rg *Region, st world.Block, p BlockPos, sameSpace bool) int {
	n := 0
	source := m.sculk && !isBlock(st, m.block)
	for face := 0; face < 6; face++ {
		if !source && !multifaceHas(st, face) {
			continue
		}
		for sd := 0; sd < 6; sd++ {
			if sd>>1 == face>>1 {
				continue
			}
			if !source && (!multifaceHas(st, face) || multifaceHas(st, sd)) {
				continue
			}
			so, fo := dirOffsets[sd], dirOffsets[face]
			cands := [3]struct {
				q    BlockPos
				face int
			}{
				{p, sd},
				{offset(p, so[0], so[1], so[2]), face},
				{offset(p, so[0]+fo[0], so[1]+fo[1], so[2]+fo[2]), oppositeDir(sd)},
			}
			limit := 3
			if sameSpace {
				limit = 1
			}
			for _, cd := range cands[:limit] {
				cur := rg.Block(cd.q[0], cd.q[1], cd.q[2])
				if !m.replaceable(rg, p, cd.q, cd.face, cur) || !m.validForPlacement(rg, cur, cd.q, cd.face) {
					continue
				}
				if ns, ok := m.stateForPlacement(rg, cur, cd.q, cd.face); ok {
					rg.SetBlock(cd.q[0], cd.q[1], cd.q[2], ns)
					n++
				}
				break
			}
		}
	}
	return n
}

// sculkUseCharge is SculkBlock.attemptUseCharge.
func sculkUseCharge(rg *Region, cu *sculkCursor, origin BlockPos, r Random) int32 {
	i := cu.charge
	if i == 0 || r.NextIntN(sculkChargeDecayRate) != 0 {
		return i
	}
	p := cu.pos
	d2 := float64(sqI(p[0]-origin[0]) + sqI(p[1]-origin[1]) + sqI(p[2]-origin[2]))
	near := d2 < float64(sculkNoGrowthRadius*sculkNoGrowthRadius)
	if !near && sculkCanPlaceGrowth(rg, p) {
		if r.NextIntN(sculkGrowthSpawnCost) < i {
			up := offset(p, 0, 1, 0)
			var gs world.Block
			if r.NextIntN(11) == 0 {
				gs = carpetWith(blockNamed("sculk_shrieker"), "can_summon", "true")
			} else {
				gs = blockNamed("sculk_sensor")
			}
			if hasFluid(rg.Block(up[0], up[1], up[2])) {
				gs = carpetWith(gs, "waterlogged", "true")
			}
			rg.SetBlock(up[0], up[1], up[2], gs)
		}
		return max(0, i-sculkGrowthSpawnCost)
	}
	if r.NextIntN(sculkExtraDecayRate) != 0 {
		return i
	}
	if near {
		return i - 1
	}
	// getDecayPenalty
	f := float32(math.Sqrt(d2)) - float32(sculkNoGrowthRadius)
	f = float32(f * f)
	k := (24 - sculkNoGrowthRadius) * (24 - sculkNoGrowthRadius)
	g := min(float32(1), f/float32(k))
	return i - max(1, int32(float32(float32(float32(i)*g)*0.5)))
}

func sqI(v int32) int32 { return v * v }

// sculkCanPlaceGrowth is SculkBlock.canPlaceGrowth.
func sculkCanPlaceGrowth(rg *Region, p BlockPos) bool {
	a := rg.Block(p[0], p[1]+1, p[2])
	if !isAir(a) && !(isBlock(a, "water") && isFluidSource(a)) {
		return false
	}
	n := 0
	for z := p[2] - 4; z <= p[2]+4; z++ {
		for y := p[1]; y <= p[1]+2; y++ {
			for x := p[0] - 4; x <= p[0]+4; x++ {
				b := rg.Block(x, y, z)
				if isBlock(b, "sculk_sensor") || isBlock(b, "sculk_shrieker") {
					n++
					if n > 2 {
						return false
					}
				}
			}
		}
	}
	return true
}

// veinUseCharge is SculkVeinBlock.attemptUseCharge.
func veinUseCharge(rg *Region, cu *sculkCursor, r Random, veins bool) int32 {
	if veins && veinPlaceSculk(rg, cu.pos, r) {
		return cu.charge - 1
	}
	if r.NextIntN(sculkChargeDecayRate) == 0 {
		return floorI(float64(float32(cu.charge) * 0.5))
	}
	return cu.charge
}

// veinPlaceSculk is SculkVeinBlock.attemptPlaceSculk.
func veinPlaceSculk(rg *Region, p BlockPos, r Random) bool {
	st := rg.Block(p[0], p[1], p[2])
	all := []int{dirDown, dirUp, dirNorth, dirSouth, dirWest, dirEast}
	for _, d := range shuffledDirs(all, r) {
		if !multifaceHas(st, d) {
			continue
		}
		o := dirOffsets[d]
		n := offset(p, o[0], o[1], o[2])
		if !blockInTag(rg.Block(n[0], n[1], n[2]), "sculk_replaceable_world_gen") {
			continue
		}
		sculk := blockNamed("sculk")
		rg.SetBlock(n[0], n[1], n[2], sculk)
		sculkVein.spreadAll(rg, sculk, n, false)
		opp := oppositeDir(d)
		for d2 := 0; d2 < 6; d2++ {
			if d2 == opp {
				continue
			}
			o2 := dirOffsets[d2]
			q := offset(n, o2[0], o2[1], o2[2])
			if b := rg.Block(q[0], q[1], q[2]); isBlock(b, "sculk_vein") {
				veinDischarged(rg, b, q)
			}
		}
		return true
	}
	return false
}

// veinDischarged is SculkVeinBlock.onDischarged: faces resting on
// sculk drop; a vein left without faces becomes air or water.
func veinDischarged(rg *Region, st world.Block, p BlockPos) {
	if !isBlock(st, "sculk_vein") {
		return
	}
	for d := 0; d < 6; d++ {
		o := dirOffsets[d]
		if multifaceHas(st, d) && isBlock(rg.Block(p[0]+o[0], p[1]+o[1], p[2]+o[2]), "sculk") {
			st = carpetWith(st, multifaceFaceProps[d], "false")
		}
	}
	if len(availableFaces(st)) == 0 {
		if hasFluid(rg.Block(p[0], p[1], p[2])) {
			st = blockNamed("water")
		} else {
			st = world.Air
		}
	}
	rg.SetBlock(p[0], p[1], p[2], st)
}

// sculkMovement is ChargeCursor.getValidMovementPos.
func sculkMovement(rg *Region, p BlockPos, r Random) (BlockPos, bool) {
	best := p
	offs := append([]BlockPos(nil), sculkNonCornerNeighbours...)
	for i := int32(len(offs)); i > 1; i-- {
		j := r.NextIntN(i)
		offs[i-1], offs[j] = offs[j], offs[i-1]
	}
	for _, o := range offs {
		q := offset(p, o[0], o[1], o[2])
		st := rg.Block(q[0], q[1], q[2])
		if !isSculkBehaviour(st) || !sculkUnobstructed(rg, p, q) {
			continue
		}
		best = q
		if veinSubstrate(rg, st, q) {
			break
		}
	}
	return best, best != p
}

// veinSubstrate is SculkVeinBlock.hasSubstrateAccess.
func veinSubstrate(rg *Region, st world.Block, p BlockPos) bool {
	if !isBlock(st, "sculk_vein") {
		return false
	}
	for d := 0; d < 6; d++ {
		o := dirOffsets[d]
		if multifaceHas(st, d) && blockInTag(rg.Block(p[0]+o[0], p[1]+o[1], p[2]+o[2]), "sculk_replaceable") {
			return true
		}
	}
	return false
}

// sculkUnobstructed is ChargeCursor.isMovementUnobstructed.
func sculkUnobstructed(rg *Region, from, to BlockPos) bool {
	d := BlockPos{to[0] - from[0], to[1] - from[1], to[2] - from[2]}
	if absI(d[0])+absI(d[1])+absI(d[2]) == 1 {
		return true
	}
	dx, dy, dz := dirEast, dirUp, dirSouth
	if d[0] < 0 {
		dx = dirWest
	}
	if d[1] < 0 {
		dy = dirDown
	}
	if d[2] < 0 {
		dz = dirNorth
	}
	open := func(dir int) bool {
		o := dirOffsets[dir]
		return !sturdyFull(rg.Block(from[0]+o[0], from[1]+o[1], from[2]+o[2]), oppositeDir(dir))
	}
	switch {
	case d[0] == 0:
		return open(dy) || open(dz)
	case d[1] == 0:
		return open(dx) || open(dz)
	}
	return open(dx) || open(dy)
}
