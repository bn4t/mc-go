package server

import (
	"strconv"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// Leaf decay, measured off the 26.2 jar (LeavesBlock):
//
//   - getDistanceAt(state): a #prevents_nearby_leaf_decay block (every
//     log, wood, stem and hyphae, stripped or not) reads 0; anything
//     carrying a `distance` property reads it — scaffolding leaks in
//     by name, exactly as vanilla's hasProperty(DISTANCE) does; every
//     other block reads 7.
//   - updateShape: a leaf whose neighbour changed schedules a block
//     tick (+1) unless the changed neighbour is distance-source 0 and
//     the leaf is already at 1 — `if (i != 1 || distance != i)
//     scheduleTick(pos, this, 1)`.
//   - tick: rewrites the cell to min(6 neighbours' getDistanceAt)+1
//     with flags 3, so neighbour updates cascade the recompute along
//     a chain.
//   - randomTick (gated by isRandomlyTicking = distance==7 &&
//     !persistent): dropResources (the leaf's loot table rolled with
//     no tool — saplings/sticks/apples) + removeBlock to air.
//   - Placement folds through the same updateShape, so a /setblock'd
//     leaf settles its distance and, being persistent=false, decays.

// leafDistanceOf is LeavesBlock.getDistanceAt.
func leafDistanceOf(b world.Block) int {
	name, ok := world.StateOwner(b)
	if !ok {
		return 7
	}
	if blockTagHas("prevents_nearby_leaf_decay", name) {
		return 0
	}
	if d, ok := intProp(b, "distance"); ok {
		return d
	}
	return 7
}

// isLeafBlock reports the LeavesBlock family by name.
func isLeafBlock(name string) bool { return strings.HasSuffix(name, "_leaves") }

// leafShape is the leaf half of updateShape: neighbour change at
// changed may alter the leaf's distance, so schedule the +1 recompute.
func (s *Server) leafShape(d *dimension, pos, changed world.Pos) {
	b := d.w.GetBlock(pos)
	cur, ok := intProp(b, "distance")
	if !ok {
		return
	}
	i := leafDistanceOf(d.w.GetBlock(changed)) + 1
	if i != 1 || cur != i {
		s.scheduleBlockTick(d, pos, 1)
	}
}

// leafDistanceTick is LeavesBlock.tick: recompute distance from the
// six neighbours and write it with the full update fan-out so the
// change cascades down a chain.
func (s *Server) leafDistanceTick(d *dimension, pos world.Pos) {
	b := d.w.GetBlock(pos)
	dist := 7
	for _, np := range neighbourPositions(pos) {
		if n := leafDistanceOf(d.w.GetBlock(np)) + 1; n < dist {
			dist = n
			if dist == 1 {
				break
			}
		}
	}
	nb, ok := world.WithProperty(b, "distance", strconv.Itoa(dist))
	if !ok || nb == b {
		return
	}
	s.setNaturalBlock(d, pos, nb)
}

// leafDecayRule is LeavesBlock.randomTick: a leaf at distance 7 that
// isn't player-pinned crumbles away — dropResources first, so a
// decaying leaf rolls its blocks/<leaf> loot table (saplings, sticks,
// apples) with no tool context, then removeBlock folds it to air.
func leafDecayRule(s *Server, d *dimension, pos world.Pos, _ world.Block) {
	b := d.w.GetBlock(pos)
	if p, _ := world.StateProperty(b, "persistent"); p == "true" {
		return
	}
	dist, ok := intProp(b, "distance")
	if !ok || dist != 7 {
		return
	}
	if name, ok := world.StateOwner(b); ok {
		table := "blocks/" + strings.TrimPrefix(name, "minecraft:")
		if _, hasTable := data.LootTables()[table]; hasTable {
			for _, st := range rollFluidLoot(table, b, pos, d) {
				s.spawnItemDrop(d.name, pos, st)
			}
		}
	}
	s.setNaturalBlock(d, pos, world.Air)
}

// leafPlaceState is LeavesBlock.getStateForPlacement: a hand-placed
// leaf is persistent and takes its distance from the neighbours on
// the spot (updateDistance) instead of the default 7, so a leaf put
// beside a log lands at distance 1 with no settle tick. Waterlogged
// never occurs on this path — the target cell must already be air.
func leafPlaceState(d *dimension, pos world.Pos, blk world.Block) world.Block {
	name, ok := world.StateOwner(blk)
	if !ok || !isLeafBlock(strings.TrimPrefix(name, "minecraft:")) {
		return blk
	}
	dist := 7
	for _, np := range neighbourPositions(pos) {
		if n := leafDistanceOf(d.w.GetBlock(np)) + 1; n < dist {
			dist = n
			if dist == 1 {
				break
			}
		}
	}
	if nb, ok := world.WithProperty(blk, "persistent", "true"); ok {
		blk = nb
	}
	if nb, ok := world.WithProperty(blk, "distance", strconv.Itoa(dist)); ok {
		blk = nb
	}
	return blk
}

// scheduleBlockTick queues a +delay tickBlock on the scheduler; the
// tick dispatcher routes _leaves to leafDistanceTick.
func (s *Server) scheduleBlockTick(d *dimension, pos world.Pos, delay int64) {
	if s.ticks == nil || d == nil {
		return
	}
	if delay < 1 {
		delay = 1
	}
	s.ticks.schedule(schedKey{dim: d.name, pos: pos, kind: tickBlock}, s.TickCount()+delay)
}
