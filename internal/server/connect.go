package server

import (
	"strings"

	"mc-go/internal/world"
)

// CrossCollisionBlock connection props — measured on the vanilla 26.2
// jar (FenceBlock/IronBarsBlock updateShape + connectsTo/attachsTo,
// Block.isExceptionForConnection). Walls (WallBlock's low/tall/up
// fold) are a separate, larger fold — GOAL.md residual.
//
// The model mirrors vanilla: a write fans out updateShape to the six
// face neighbours first (this file), then neighbourChanged (the
// survival table in survive.go). For the connection family the
// updateShape half only looks at horizontal neighbours — the prop
// facing the changed cell is rewritten to connectsTo/attachsTo of the
// changed state. A rewrite itself fans out (vanilla's setBlock
// recurses), so a line of fences settles by fixpoint.

// connectFam identifies the measured connection family the block
// belongs to.
type connectFam int

const (
	connNone  connectFam = iota
	connFence            // FenceBlock: fences (wooden + nether_brick)
	connBars             // IronBarsBlock: panes + bars (incl. copper bars)
)

// connectFamily reports the family of a block by registry name: the
// #fences tag for fences, and the IronBarsBlock class set — every
// *_pane / *_bars name — for bars.
func connectFamily(name string) connectFam {
	if blockTagHas("fences", name) {
		return connFence
	}
	if strings.HasSuffix(name, "_pane") || strings.HasSuffix(name, "_bars") {
		return connBars
	}
	return connNone
}

// connHorizontal reports whether changed is a face-sharing
// horizontal neighbour of pos.
func connHorizontal(pos, changed world.Pos) bool {
	dx, dz := changed.X-pos.X, changed.Z-pos.Z
	return changed.Y == pos.Y && dx*dx+dz*dz == 1
}

// connPropByDir maps the direction self->changed to the block property
// it rewrites (CrossCollisionBlock.PROPERTY_BY_DIRECTION).
func connPropByDir(dx, dz int) (string, bool) {
	switch {
	case dz == -1:
		return "north", true
	case dz == 1:
		return "south", true
	case dx == 1:
		return "east", true
	case dx == -1:
		return "west", true
	}
	return "", false
}

// connectException is Block.isExceptionForConnection, 26.2's list:
// leaves, barrier, the pumpkin heads, melon/pumpkin, shulker boxes.
func connectException(name string) bool {
	if strings.HasSuffix(name, "_leaves") {
		return true
	}
	switch name {
	case "barrier", "carved_pumpkin", "jack_o_lantern", "melon", "pumpkin":
		return true
	}
	return blockTagHas("shulker_boxes", name)
}

// connectSturdy approximates the neighbour's isFaceSturdy(dir.opposite)
// the way the rest of mc-go does — non-fluid and not in the washed-
// away set — plus a name-level list of non-full-cube solids whose side
// faces don't count (stairs, slabs, doors, thin blocks).
func connectSturdy(d *dimension, pos world.Pos, b world.Block, name string) bool {
	if _, _, isFluid := fluidOf(b); isFluid {
		return false
	}
	if world.NonSolid(b) {
		return false
	}
	return !connectPartialFace(name)
}

// connectPartialFace is the name-level list of solid-but-not-full-cube
// blocks whose side faces vanilla's isFaceSturdy reports as not
// sturdy — so connections never attach through them.
func connectPartialFace(name string) bool {
	for _, suf := range []string{
		"_stairs", "_slab", "_trapdoor", "_door", "_fence",
		"_fence_gate", "_wall", "_pane", "_bars", "_sign", "_banner",
		"_head", "_bed", "_anvil", "_shulker_box", "_flower_pot",
		"_dripleaf", "_button", "_pressure_plate", "_rail", "_torch",
	} {
		if strings.HasSuffix(name, suf) {
			return true
		}
	}
	switch name {
	case "chest", "trapped_chest", "ender_chest", "hopper",
		"enchanting_table", "brewing_stand", "cauldron", "composter",
		"lectern", "grindstone", "stonecutter", "bell", "lantern",
		"soul_lantern", "chain", "end_rod", "lightning_rod", "conduit",
		"decorated_pot", "bamboo", "scaffolding", "chorus_plant",
		"chorus_flower", "pointed_dripstone", "cake", "snow",
		"farmland", "dirt_path", "daylight_detector", "soul_sand",
		"mud", "honey_block", "slime_block", "piston", "sticky_piston",
		"moving_piston", "ladder", "vines", "snow_layer":
		return true
	}
	return false
}

// fenceGateFacingAxisX reports whether a fence_gate's facing prop runs
// along the X axis (east/west).
func fenceGateFacingAxisX(b world.Block) bool {
	f, ok := world.StateProperty(b, "facing")
	return ok && (f == "east" || f == "west")
}

// connects evaluates FenceBlock.connectsTo / IronBarsBlock.attachsTo:
// the value the prop toward `changed` takes after the updateShape.
// dirX/dirZ is self->changed; sturdy asks whether the changed block's
// face back toward self is sturdy.
func (s *Server) connects(d *dimension, fam connectFam, selfName string, changed world.Pos, dx, dz int) bool {
	nb := d.w.GetBlock(changed)
	nname, ok := world.StateOwner(nb)
	if !ok {
		return false
	}
	nname = strings.TrimPrefix(nname, "minecraft:")
	sturdy := !connectException(nname) && connectSturdy(d, changed, nb, nname)
	switch fam {
	case connFence:
		// isSameFence: #fences member with matching #wooden_fences
		// membership (wooden fences join wooden, nether_brick only
		// nether_brick).
		same := blockTagHas("fences", nname) &&
			blockTagHas("wooden_fences", nname) == blockTagHas("wooden_fences", selfName)
		// FenceGateBlock.connectsToDirection: the gate connects when
		// its facing axis is perpendicular to the self->gate
		// direction — gate.FACING.axis == dir.opposite.clockwise.axis.
		// For self->gate = north/south the opposite's clockwise is
		// west/east (axis X); for east/west it's north/south (axis Z).
		gate := strings.HasSuffix(nname, "_fence_gate") &&
			(dz != 0) == fenceGateFacingAxisX(nb)
		return sturdy || same || gate
	case connBars:
		return sturdy || strings.HasSuffix(nname, "_pane") ||
			strings.HasSuffix(nname, "_bars") || blockTagHas("walls", nname)
	}
	return false
}

// connectionUpdate runs the updateShape half of a neighbour write for
// the connection family: the block at pos rewrites its prop toward
// changed, and a rewrite cascades (vanilla's setBlock on the cell
// fans out again) until the connected run settles.
func (s *Server) connectionUpdate(d *dimension, pos, changed world.Pos) {
	b := d.w.GetBlock(pos)
	name, ok := world.StateOwner(b)
	if !ok {
		return
	}
	fam := connectFamily(strings.TrimPrefix(name, "minecraft:"))
	if fam == connNone {
		return
	}
	if !connHorizontal(pos, changed) {
		return // updateShape only rewrites horizontal neighbours
	}
	prop, ok := connPropByDir(changed.X-pos.X, changed.Z-pos.Z)
	if !ok {
		return
	}
	want := s.connects(d, fam, strings.TrimPrefix(name, "minecraft:"), changed, changed.X-pos.X, changed.Z-pos.Z)
	cur, _ := world.StateProperty(b, prop)
	wantStr := "false"
	if want {
		wantStr = "true"
	}
	if cur == wantStr {
		return
	}
	nb, ok := world.WithProperty(b, prop, wantStr)
	if !ok {
		return
	}
	s.setSilentBlock(d, pos, nb)
	s.connectionCascade(d, pos)
}

// connectionCascade re-runs the horizontal updateShape pass around a
// cell whose connection props just changed — vanilla's recursive
// setBlock — bounded by the connected run.
func (s *Server) connectionCascade(d *dimension, pos world.Pos) {
	seen := map[world.Pos]bool{pos: true}
	queue := []world.Pos{pos}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dir := range cardinalDirs {
			n := world.Pos{X: cur.X + dir.dx, Y: cur.Y, Z: cur.Z + dir.dz}
			if seen[n] {
				continue
			}
			before := d.w.GetBlock(n)
			s.connectionUpdateNoCascade(d, n, cur)
			if d.w.GetBlock(n) != before {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
}

// connectionUpdateNoCascade is connectionUpdate without the cascade —
// the worklist driver owns propagation.
func (s *Server) connectionUpdateNoCascade(d *dimension, pos, changed world.Pos) {
	b := d.w.GetBlock(pos)
	name, ok := world.StateOwner(b)
	if !ok {
		return
	}
	fam := connectFamily(strings.TrimPrefix(name, "minecraft:"))
	if fam == connNone || !connHorizontal(pos, changed) {
		return
	}
	prop, ok := connPropByDir(changed.X-pos.X, changed.Z-pos.Z)
	if !ok {
		return
	}
	want := s.connects(d, fam, strings.TrimPrefix(name, "minecraft:"), changed, changed.X-pos.X, changed.Z-pos.Z)
	wantStr := "false"
	if want {
		wantStr = "true"
	}
	cur, _ := world.StateProperty(b, prop)
	if cur == wantStr {
		return
	}
	if nb, ok := world.WithProperty(b, prop, wantStr); ok {
		s.setSilentBlock(d, pos, nb)
	}
}

// connectPlaceState is the getStateForPlacement half: a placed
// connectable block folds its four horizontal props from the current
// neighbours (and waterlogged from the cell's fluid), exactly what
// vanilla's placement context does. Command writes never fold — they
// place the exact state.
func (s *Server) connectPlaceState(d *dimension, pos world.Pos, blk world.Block) world.Block {
	name, ok := world.StateOwner(blk)
	if !ok {
		return blk
	}
	short := strings.TrimPrefix(name, "minecraft:")
	fam := connectFamily(short)
	if fam == connNone {
		return blk
	}
	out := blk
	for _, dir := range cardinalDirs {
		prop, ok := connPropByDir(dir.dx, dir.dz)
		if !ok {
			continue
		}
		n := world.Pos{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz}
		want := "false"
		if s.connects(d, fam, short, n, dir.dx, dir.dz) {
			want = "true"
		}
		if nb, ok := world.WithProperty(out, prop, want); ok {
			out = nb
		}
	}
	if _, hasWL := world.StateProperty(out, "waterlogged"); hasWL {
		if _, level, isFluid := fluidOf(d.w.GetBlock(pos)); isFluid && level == 0 {
			if nb, ok := world.WithProperty(out, "waterlogged", "true"); ok {
				out = nb
			}
		}
	}
	return out
}
