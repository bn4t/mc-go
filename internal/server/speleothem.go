package server

import "mc-go/internal/world"

// Speleothems — SpeleothemBlock + PointedDripstoneBlock, all measured
// against 26.2 bytecode. Two speleothem blocks exist in 26.2:
// pointed_dripstone (grows on dripstone_block, needs water one more
// cell above it, transfers drips into cauldrons) and sulfur_spike
// (grows on sulfur, no fluid rules, max column length 2).
//
// Growth (SpeleothemBlock.randomTick): a nextFloat()<0.011377778 gate,
// then only the topmost cell of a hanging column (a downward-pointing
// speleothem whose above cell is not the same block) proceeds.
// canGrow, findTip, the tip-state checks and the 50/50 coin between
// growing the hanging tip and growing a stalagmite below are all
// ported one to one — including updateShape's thickness rewrite
// (tip/frustum/middle/base ladder) and the +1/+2 support-loss tick
// that breaks stalagmites or drops stalactites.
//
// Fluid transfer (PointedDripstoneBlock.randomTick's extra first
// float): a hanging start cell whose root block carries water (or a
// mud cell, which reads as water) rolls 0.17578125 (lava 0.05859375),
// finds the tip, and either converts mud to clay outright or schedules
// a block tick on the first drippable cauldron within 10 cells of the
// tip with delay 50 + distance. The cauldron tick re-derives the tip
// and fluid before filling — CauldronBlock takes water/lava outright,
// LayeredCauldronBlock only water, everything else never.

// speleoDir maps vertical_direction to a y step: up-pointing
// stalagmites grow +1, down-pointing stalactites -1.
func speleoDir(b world.Block) int {
	if world.BlockProperties(b)["vertical_direction"] == "up" {
		return 1
	}
	return -1
}

func speleoName(b world.Block) string {
	name, _ := world.StateOwner(b)
	return name
}

func speleoIsSpeleothem(b world.Block) bool {
	n := speleoName(b)
	return n == "pointed_dripstone" || n == "sulfur_spike"
}

// speleoIsTip is isTip(state,false): a speleothem at thickness=tip.
// allowMerge also counts tip_merge.
func speleoIsTip(b world.Block, allowMerge bool) bool {
	if !speleoIsSpeleothem(b) {
		return false
	}
	th := world.BlockProperties(b)["thickness"]
	return th == "tip" || (allowMerge && th == "tip_merge")
}

// speleoSameDir is isSpeleothemWithDirection: any speleothem pointing
// the given way.
func speleoSameDir(b world.Block, dir int) bool {
	return speleoIsSpeleothem(b) && speleoDir(b) == dir
}

// speleoIsUnmergedTip is isUnmergedTipWithDirection: a same-block
// tip (not tip_merge) facing dir.
func speleoIsUnmergedTip(b world.Block, dir int, selfName string) bool {
	return speleoIsTip(b, false) && speleoDir(b) == dir && speleoName(b) == selfName
}

// speleoFluidAt is the fluid a cell presents for drip purposes:
// fluid blocks plus a waterlogged speleothem read as water.
func speleoFluidAt(d *dimension, pos world.Pos) (kind fluidKind, level int, ok bool) {
	b := d.w.GetBlock(pos)
	if k, l, o := fluidOf(b); o {
		return k, l, true
	}
	if world.BlockProperties(b)["waterlogged"] == "true" {
		return fluidWater, 0, true
	}
	return 0, 0, false
}

// speleoSturdy is the isFaceSturdy approximation the rest of the
// server uses: a non-fluid, collision-having block.
func speleoSturdy(d *dimension, pos world.Pos) bool {
	b := d.w.GetBlock(pos)
	if _, _, isFluid := fluidOf(b); isFluid {
		return false
	}
	return !world.NonSolid(b)
}

// speleoCanSurvive is SpeleothemBlock.canSurvive →
// isValidSpeleothemPlacement: the anchor cell (opposite the growth
// direction) is sturdy or a same-block speleothem pointing the same way.
func speleoCanSurvive(d *dimension, pos world.Pos, b world.Block) bool {
	dir := speleoDir(b)
	anchor := world.Pos{X: pos.X, Y: pos.Y - dir, Z: pos.Z}
	a := d.w.GetBlock(anchor)
	return speleoSturdy(d, anchor) || (speleoSameDir(a, dir) && speleoName(a) == speleoName(b))
}

// speleoCalcThickness is calculateSpeleothemThickness: look at the
// cell in the growth direction, then the anchor side.
func speleoCalcThickness(d *dimension, pos world.Pos, dir int, merged bool, selfName string) string {
	next := d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y + dir, Z: pos.Z})
	if speleoSameDir(next, -dir) && speleoName(next) == selfName {
		if merged || world.BlockProperties(next)["thickness"] == "tip_merge" {
			return "tip_merge"
		}
		return "tip"
	}
	if !speleoSameDir(next, dir) {
		return "tip"
	}
	nt := world.BlockProperties(next)["thickness"]
	if nt == "tip" || nt == "tip_merge" {
		return "frustum"
	}
	prev := d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y - dir, Z: pos.Z})
	if !speleoSameDir(prev, dir) {
		return "base"
	}
	return "middle"
}

// speleoFindTip is findTip + findBlockVertical: from a start cell,
// walk the growth direction up to maxScan-1 cells looking for a tip,
// continuing only through same-block cells pointing the same way. The
// start cell itself is checked first by isTip.
func speleoFindTip(d *dimension, pos world.Pos, b world.Block, maxScan int) (world.Pos, bool) {
	if speleoIsTip(b, false) {
		return pos, true
	}
	dir := speleoDir(b)
	selfName := speleoName(b)
	p := pos
	for i := 1; i < maxScan; i++ {
		p = world.Pos{X: p.X, Y: p.Y + dir, Z: p.Z}
		c := d.w.GetBlock(p)
		if speleoIsTip(c, false) {
			return p, true
		}
		if !(speleoName(c) == selfName && speleoDir(c) == dir) {
			return world.Pos{}, false
		}
	}
	return world.Pos{}, false
}

// speleoFreeHanging is isFreeHangingStalactite: a downward tip that
// is not waterlogged.
func speleoFreeHanging(b world.Block) bool {
	return speleoSameDir(b, -1) &&
		world.BlockProperties(b)["thickness"] == "tip" &&
		world.BlockProperties(b)["waterlogged"] != "true"
}

// speleoCanTipGrow is canTipGrow: the cell past the tip must have no
// fluid and be either air or an opposite-facing unmerged tip of the
// same block.
func speleoCanTipGrow(d *dimension, tipB world.Block, tipPos world.Pos) bool {
	dir := speleoDir(tipB)
	next := world.Pos{X: tipPos.X, Y: tipPos.Y + dir, Z: tipPos.Z}
	nb := d.w.GetBlock(next)
	if _, _, has := speleoFluidAt(d, next); has {
		return false
	}
	if nb == world.Air {
		return true
	}
	return speleoIsUnmergedTip(nb, -dir, speleoName(tipB))
}

// speleoCreate is createSpeleothem: the default state with the given
// direction/thickness, waterlogged iff the cell holds water, written
// flag-3 (a normal natural write — neighbours recompute).
func (s *Server) speleoCreate(d *dimension, pos world.Pos, dir int, thickness, selfName string) {
	dirStr := "down"
	if dir > 0 {
		dirStr = "up"
	}
	_, _, wet := speleoFluidAt(d, pos)
	nb, ok := world.BlockStateOf(selfName, map[string]string{
		"vertical_direction": dirStr,
		"thickness":          thickness,
		"waterlogged":        boolStr(wet),
	})
	if !ok {
		return
	}
	s.setNaturalBlock(d, pos, nb)
}

// speleoGrow is SpeleothemBlock.grow: write one cell at pos+dir — a
// merged tip pair when the target is an opposite-facing tip of the
// same block, else a fresh tip when the target is air or water.
func (s *Server) speleoGrow(d *dimension, pos world.Pos, dir int, selfName string) {
	target := world.Pos{X: pos.X, Y: pos.Y + dir, Z: pos.Z}
	tb := d.w.GetBlock(target)
	if speleoIsUnmergedTip(tb, -dir, selfName) {
		// createMergedTips: the cell that was the lone opposite tip
		// and the adjacent tip cell both become tip_merge.
		var a, b world.Pos
		if speleoDir(tb) > 0 {
			a = world.Pos{X: target.X, Y: target.Y + 1, Z: target.Z}
			b = target
		} else {
			a = target
			b = world.Pos{X: target.X, Y: target.Y - 1, Z: target.Z}
		}
		s.speleoCreate(d, a, -1, "tip_merge", selfName)
		s.speleoCreate(d, b, 1, "tip_merge", selfName)
		return
	}
	if tb == world.Air {
		s.speleoCreate(d, target, dir, "tip", selfName)
		return
	}
	if k, _, ok := speleoFluidAt(d, target); ok && k == fluidWater {
		s.speleoCreate(d, target, dir, "tip", selfName)
	}
}

// speleoGrowStalagmiteBelow is growStalagmiteBelow: scan up to 10
// cells below the hanging tip; fluids stop the scan, an up-tip or a
// supported floor cell grows the spike, and for pointed dripstone a
// cell the drip can't pass through blocks it.
func (s *Server) speleoGrowStalagmiteBelow(d *dimension, tipPos world.Pos, selfName string) {
	p := tipPos
	for i := 0; i < 10; i++ {
		p = world.Pos{X: p.X, Y: p.Y - 1, Z: p.Z}
		st := d.w.GetBlock(p)
		if _, _, has := speleoFluidAt(d, p); has {
			return
		}
		if speleoIsUnmergedTip(st, 1, selfName) && s.speleoCanTipGrowCheck(d, st, p) {
			s.speleoGrow(d, p, 1, selfName)
			return
		}
		if s.speleoValidPlacement(d, p, 1, selfName) {
			if k, _, ok := speleoFluidAt(d, world.Pos{X: p.X, Y: p.Y - 1, Z: p.Z}); !ok || k != fluidWater {
				s.speleoGrow(d, world.Pos{X: p.X, Y: p.Y - 1, Z: p.Z}, 1, selfName)
				return
			}
		}
		if selfName == "pointed_dripstone" && !speleoCanDripThrough(d, p, st) {
			return
		}
	}
}

// speleoCanTipGrowCheck mirrors canTipGrow for a cell the scan found.
func (s *Server) speleoCanTipGrowCheck(d *dimension, b world.Block, pos world.Pos) bool {
	return speleoCanTipGrow(d, b, pos)
}

// speleoValidPlacement is isValidSpeleothemPlacement.
func (s *Server) speleoValidPlacement(d *dimension, pos world.Pos, dir int, selfName string) bool {
	anchor := world.Pos{X: pos.X, Y: pos.Y - dir, Z: pos.Z}
	a := d.w.GetBlock(anchor)
	return speleoSturdy(d, anchor) || (speleoSameDir(a, dir) && speleoName(a) == selfName)
}

// speleoCanDripThrough is canDripThrough: air passes, a solid-render
// or fluid cell blocks, anything else is treated as passable (the
// voxel-space check is approximated by the nonsolid set).
func speleoCanDripThrough(d *dimension, pos world.Pos, b world.Block) bool {
	if b == world.Air {
		return true
	}
	if _, _, has := speleoFluidAt(d, pos); has {
		return false
	}
	if speleoIsSpeleothem(b) {
		return true
	}
	return world.NonSolid(b)
}

// speleoStartPos is isStalactiteStartPos: a downward speleothem whose
// above cell is not the same block — the top cell of a hanging column.
func speleoStartPos(d *dimension, pos world.Pos, b world.Block) bool {
	return speleoSameDir(b, -1) &&
		speleoName(d.w.GetBlock(posAbove(pos, 1))) != speleoName(b)
}

// speleoCanGrow is PointedDripstoneBlock.canGrow / the
// SpeleothemBlock base for sulfur_spike: the anchor block above, and
// for dripstone also water one cell above that.
func speleoCanGrow(d *dimension, pos world.Pos, selfName string) bool {
	anchorName, _ := world.StateOwner(d.w.GetBlock(posAbove(pos, 1)))
	switch selfName {
	case "pointed_dripstone":
		if anchorName != "dripstone_block" {
			return false
		}
		k, _, ok := speleoFluidAt(d, posAbove(pos, 2))
		return ok && k == fluidWater
	case "sulfur_spike":
		return anchorName == "sulfur"
	}
	return false
}

// speleoGrowIfPossible is growStalactiteOrStalagmiteIfPossible.
func (s *Server) speleoGrowIfPossible(d *dimension, pos world.Pos, b world.Block, selfName string, maxLen int) {
	if !speleoCanGrow(d, pos, selfName) {
		return
	}
	tipPos, ok := speleoFindTip(d, pos, b, maxLen)
	if !ok {
		return
	}
	tipB := d.w.GetBlock(tipPos)
	if !speleoFreeHanging(tipB) || !speleoCanTipGrow(d, tipB, tipPos) {
		return
	}
	if s.rng.nextDouble() < 0.5 {
		s.speleoGrow(d, tipPos, -1, selfName)
	} else {
		s.speleoGrowStalagmiteBelow(d, tipPos, selfName)
	}
}

// speleoFindRoot is findRootBlock: walk up from the start cell to the
// first cell that is not the same block (the hanging root).
func speleoFindRoot(d *dimension, pos world.Pos, b world.Block, maxScan int) (world.Pos, bool) {
	dir := speleoDir(b)
	selfName := speleoName(b)
	p := pos
	for i := 1; i < maxScan; i++ {
		p = world.Pos{X: p.X, Y: p.Y - dir, Z: p.Z}
		c := d.w.GetBlock(p)
		if speleoName(c) != selfName {
			return p, true
		}
		if !(speleoDir(c) == dir) {
			return world.Pos{}, false
		}
	}
	return world.Pos{}, false
}

// speleoMaybeTransferFluid is PointedDripstoneBlock.maybeTransferFluid.
func speleoMaybeTransferFluid(s *Server, d *dimension, pos world.Pos, b world.Block) {
	f := s.rng.nextDouble()
	if f > 0.17578125 {
		return
	}
	if !speleoStartPos(d, pos, b) {
		return
	}
	root, ok := speleoFindRoot(d, pos, b, 11)
	if !ok {
		return
	}
	infoPos := posAbove(root, 1)
	infoB := d.w.GetBlock(infoPos)
	// Mud above the root reads as water unless the dimension
	// evaporates it — mc-go has no evaporating dimension.
	var fluid fluidKind
	if name, _ := world.StateOwner(infoB); name == "mud" {
		fluid = fluidWater
	} else if k, _, ok := fluidOf(infoB); ok {
		fluid = k
	} else {
		return
	}
	var threshold float64
	switch fluid {
	case fluidWater:
		threshold = 0.17578125
	case fluidLava:
		threshold = 0.05859375
	default:
		return
	}
	if f >= threshold {
		return
	}
	tipPos, ok := speleoFindTip(d, pos, b, 11)
	if !ok {
		return
	}
	// A mud source turns to clay on the spot instead of dripping.
	if name, _ := world.StateOwner(infoB); name == "mud" && fluid == fluidWater {
		if clay, ok := world.BlockStateOf("clay", nil); ok {
			s.setNaturalBlock(d, infoPos, clay)
		}
		return
	}
	// findFillableCauldronBelowStalactiteTip: scan down from the tip,
	// pass drippable cells, stop at a cauldron that accepts the fluid.
	p := tipPos
	for i := 1; i < 11; i++ {
		p = world.Pos{X: p.X, Y: p.Y - 1, Z: p.Z}
		c := d.w.GetBlock(p)
		name, _ := world.StateOwner(c)
		accepts := name == "cauldron" || (name == "water_cauldron" && fluid == fluidWater)
		if accepts {
			s.scheduleBlockTick(d, p, int64(50+tipPos.Y-p.Y))
			return
		}
		if !speleoCanDripThrough(d, p, c) {
			return
		}
	}
}

// speleoRule is the combined randomTick: pointed dripstone rolls the
// fluid transfer first, then both speleothems run the growth gate.
func speleoRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	selfName := speleoName(b)
	if selfName == "pointed_dripstone" {
		speleoMaybeTransferFluid(s, d, pos, b)
	}
	if s.rng.nextDouble() >= 0.011377778 {
		return
	}
	if !speleoStartPos(d, pos, b) {
		return
	}
	maxLen := 2 // sulfur_spike MAX_GROWING_LENGTH
	if selfName == "pointed_dripstone" {
		maxLen = 7
	}
	s.speleoGrowIfPossible(d, pos, b, selfName, maxLen)
}

// speleoNeighborChange is SpeleothemBlock.updateShape for a written
// neighbour: waterlogged cells re-arm their fluid tick, a support-side
// write that breaks survival arms the +2/+1 fall-or-break tick (unless
// one is already pending on a stalactite), and any other vertical
// write recomputes the cell's thickness in place.
func (s *Server) speleoNeighborChange(d *dimension, pos, changed world.Pos) {
	b := d.w.GetBlock(pos)
	selfName := speleoName(b)
	props := world.BlockProperties(b)
	if props["waterlogged"] == "true" {
		s.scheduleFluidTick(d, pos, waterTickDelay)
	}
	dy := changed.Y - pos.Y
	if dy == 0 {
		return
	}
	dir := speleoDir(b)
	writeFrom := -1
	if dy > 0 {
		writeFrom = 1
	}
	if dir < 0 && s.ticks != nil && s.ticks.isQueued(schedKey{dim: d.name, pos: pos, kind: tickBlock}) {
		return
	}
	if writeFrom == -dir && !speleoCanSurvive(d, pos, b) {
		delay := int64(2)
		if dir > 0 {
			delay = 1
		}
		s.scheduleBlockTick(d, pos, delay)
		return
	}
	th := speleoCalcThickness(d, pos, dir, props["thickness"] == "tip_merge", selfName)
	if th != props["thickness"] {
		dirStr := "down"
		if dir > 0 {
			dirStr = "up"
		}
		if nb, ok := world.BlockStateOf(selfName, map[string]string{
			"vertical_direction": dirStr,
			"thickness":          th,
			"waterlogged":        props["waterlogged"],
		}); ok {
			s.setSilentBlock(d, pos, nb)
		}
	}
}

// speleoTick is SpeleothemBlock.tick — the support-loss tick: a
// stalagmite breaks; a stalactite falls. mc-go has no falling-block
// entity, so a falling stalactite leaves the cell empty, which also
// lets the next cell re-derive support on its own tick.
func (s *Server) speleoTick(d *dimension, pos world.Pos, b world.Block) {
	if !speleoIsSpeleothem(b) {
		return
	}
	// Stalagmites re-check survival and break; stalactites fall
	// unconditionally once the tick was armed (spawnFallingStalactite
	// — mc-go has no falling-block entity, so the cell just empties
	// and the next cell's own armed tick continues the cascade).
	if speleoDir(b) < 0 || !speleoCanSurvive(d, pos, b) {
		s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
		s.setNaturalBlock(d, pos, world.Air)
	}
}

// cauldronDripTick is AbstractCauldronBlock.tick: find the stalactite
// tip above, re-derive its fluid, and fill if this cauldron takes it.
func (s *Server) cauldronDripTick(d *dimension, pos world.Pos) {
	// findStalactiteTipAboveCauldron: scan up through drippable cells
	// for a downward tip.
	p := pos
	var tipB world.Block
	found := false
	for i := 1; i < 11; i++ {
		p = world.Pos{X: p.X, Y: p.Y + 1, Z: p.Z}
		c := d.w.GetBlock(p)
		if speleoName(c) == "pointed_dripstone" &&
			speleoDir(c) < 0 &&
			world.BlockProperties(c)["thickness"] == "tip" {
			tipB = c
			found = true
			break
		}
		if !speleoCanDripThrough(d, p, c) {
			return
		}
	}
	if !found {
		return
	}
	root, ok := speleoFindRoot(d, p, tipB, 11)
	if !ok {
		return
	}
	infoPos := posAbove(root, 1)
	infoB := d.w.GetBlock(infoPos)
	var fluid fluidKind
	if name, _ := world.StateOwner(infoB); name == "mud" {
		fluid = fluidWater
	} else if k, _, ok := fluidOf(infoB); ok {
		fluid = k
	} else {
		return
	}
	cb := d.w.GetBlock(pos)
	cname, _ := world.StateOwner(cb)
	switch cname {
	case "cauldron":
		var nb world.Block
		var ok bool
		if fluid == fluidWater {
			nb, ok = world.BlockStateOf("water_cauldron", nil)
		} else if fluid == fluidLava {
			nb, ok = world.BlockStateOf("lava_cauldron", nil)
		}
		if ok {
			s.setNaturalBlock(d, pos, nb)
		}
	case "water_cauldron":
		if fluid != fluidWater {
			return
		}
		lvl := world.BlockProperties(cb)["level"]
		if lvl == "1" {
			if nb, ok := world.BlockStateOf("water_cauldron", map[string]string{"level": "2"}); ok {
				s.setNaturalBlock(d, pos, nb)
			}
		} else if lvl == "2" {
			if nb, ok := world.BlockStateOf("water_cauldron", map[string]string{"level": "3"}); ok {
				s.setNaturalBlock(d, pos, nb)
			}
		}
	}
}
