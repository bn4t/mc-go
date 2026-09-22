package server

import (
	"math"
	"strconv"
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
	connWall             // WallBlock: #walls, low/tall side props + up
)

// connectFamily reports the family of a block by registry name: the
// #fences tag for fences, the IronBarsBlock class set — every *_pane
// / *_bars name — for bars, and the #walls tag for walls.
func connectFamily(name string) connectFam {
	if blockTagHas("fences", name) {
		return connFence
	}
	if strings.HasSuffix(name, "_pane") || strings.HasSuffix(name, "_bars") {
		return connBars
	}
	if blockTagHas("walls", name) {
		return connWall
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
	case connWall:
		// WallBlock.connectsTo reaches further than the fence's:
		// #walls, sturdy non-exception sides, IronBarsBlock, and the
		// same perpendicular-gate rule.
		gate := strings.HasSuffix(nname, "_fence_gate") &&
			(dz != 0) == fenceGateFacingAxisX(nb)
		return blockTagHas("walls", nname) || sturdy ||
			strings.HasSuffix(nname, "_pane") || strings.HasSuffix(nname, "_bars") || gate
	}
	return false
}

// connectionUpdate runs the updateShape half of a neighbour write for
// the connection family: the block at pos rewrites its prop toward
// changed, and a rewrite cascades (vanilla's setBlock on the cell
// fans out again) until the connected run settles.
func (s *Server) connectionUpdate(d *dimension, pos, changed world.Pos) {
	before := d.w.GetBlock(pos)
	s.connectionUpdateNoCascade(d, pos, changed)
	if d.w.GetBlock(pos) != before {
		s.connectionCascade(d, pos)
	}
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
		for _, dy := range []int{1, -1} {
			n := world.Pos{X: cur.X, Y: cur.Y + dy, Z: cur.Z}
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
	short := strings.TrimPrefix(name, "minecraft:")
	if short == "chest" || short == "trapped_chest" {
		s.chestNeighborUpdate(d, pos, changed, b)
		return
	}
	if isStairName(short) && connHorizontal(pos, changed) {
		s.stairsNeighborUpdate(d, pos, changed, b)
		return
	}
	if short == "chorus_plant" {
		s.chorusNeighborUpdate(d, pos, changed, b)
		return
	}
	if changed.Y == pos.Y+1 && changed.X == pos.X && changed.Z == pos.Z {
		if _, hasSnowy := world.StateProperty(b, "snowy"); hasSnowy {
			s.snowyNeighborUpdate(d, pos, changed, b)
			return
		}
	}
	if coralLive(short) && changed != pos {
		s.coralNeighborUpdate(d, pos)
		return
	}
	if short == "scaffolding" && changed != pos {
		s.scheduleBlockTick(d, pos, 1)
		return
	}
	if strings.HasSuffix(short, "_concrete_powder") && changed != pos {
		s.powderNeighborUpdate(d, pos)
		return
	}
	if short == "tripwire" && connHorizontal(pos, changed) {
		s.tripwireNeighborUpdate(d, pos, changed, b)
		return
	}
	if short == "tripwire_hook" && changed != pos {
		s.hookNeighborUpdate(d, pos, changed, b)
		return
	}
	fam := connectFamily(short)
	if fam == connNone {
		return
	}
	if fam == connWall {
		s.wallNeighborUpdate(d, pos, changed, b)
		return
	}
	if !connHorizontal(pos, changed) {
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

// wallNeighborUpdate is WallBlock.updateShape: DOWN is ignored, UP
// re-runs the fold keeping the four current side bools (topUpdate),
// and a horizontal change replaces just that side's connect flag
// before the shared fold (sideUpdate).
func (s *Server) wallNeighborUpdate(d *dimension, pos, changed world.Pos, b world.Block) {
	n, e, so, w := wallSideBools(b)
	dx, dy, dz := changed.X-pos.X, changed.Y-pos.Y, changed.Z-pos.Z
	switch {
	case dy > 0:
		// topUpdate: block above changed — keep side connectivity.
	case dy < 0:
		return
	case !connHorizontal(pos, changed):
		return
	default:
		want := s.connects(d, connWall, "", changed, dx, dz)
		switch {
		case dz == -1:
			n = want
		case dz == 1:
			so = want
		case dx == 1:
			e = want
		default:
			w = want
		}
	}
	s.wallFold(d, pos, b, n, e, so, w)
}

// wallSideBools is WallBlock.isConnected over the four props: a side
// counts as connected when it isn't "none".
func wallSideBools(b world.Block) (n, e, so, w bool) {
	get := func(prop string) bool {
		v, _ := world.StateProperty(b, prop)
		return v != "none"
	}
	return get("north"), get("east"), get("south"), get("west")
}

// wallAboveCovers approximates "the above block's bottom face covers
// the wall's test shape" — TEST_SHAPES_WALL's 2x16x9 strip per side
// and TEST_SHAPE_POST's 2x2 centre column both reduce to a full
// bottom face. Bottom-half slabs/stairs and closed bottom trapdoors
// genuinely have full bottom faces; the rest fall back to the same
// non-full-cube name set the horizontal side check uses.
func wallAboveCovers(b world.Block, name string) bool {
	if strings.HasSuffix(name, "_slab") {
		t, _ := world.StateProperty(b, "type")
		return t == "bottom" || t == "double"
	}
	if strings.HasSuffix(name, "_stairs") {
		h, _ := world.StateProperty(b, "half")
		return h == "bottom"
	}
	if strings.HasSuffix(name, "_trapdoor") {
		open, _ := world.StateProperty(b, "open")
		h, _ := world.StateProperty(b, "half")
		return open != "true" && h == "bottom"
	}
	if _, _, isFluid := fluidOf(b); isFluid {
		return false
	}
	if world.NonSolid(b) {
		return false
	}
	return !connectPartialFace(name)
}

// wallFold is WallBlock's private updateShape: each side becomes
// tall when connected and covered by the block above's bottom face,
// low when connected but not covered, none otherwise; then
// shouldRaisePost decides the up prop.
func (s *Server) wallFold(d *dimension, pos world.Pos, b world.Block, n, e, so, w bool) {
	abovePos := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	ab := d.w.GetBlock(abovePos)
	aname, _ := world.StateOwner(ab)
	covers := wallAboveCovers(ab, strings.TrimPrefix(aname, "minecraft:"))
	out := b
	setSide := func(prop string, conn bool) {
		v := "none"
		if conn {
			if covers {
				v = "tall"
			} else {
				v = "low"
			}
		}
		if nb, ok := world.WithProperty(out, prop, v); ok {
			out = nb
		}
	}
	setSide("north", n)
	setSide("east", e)
	setSide("south", so)
	setSide("west", w)
	up := wallRaisePost(out, ab, strings.TrimPrefix(aname, "minecraft:"), covers)
	if nb, ok := world.WithProperty(out, "up", boolStr(up)); ok {
		out = nb
	}
	if out != b {
		s.setSilentBlock(d, pos, out)
	}
}

// wallRaisePost is shouldRaisePost: a wall topped by an up-raised
// wall keeps its post; a lone post, a corner, or a T junction raises
// one; a straight or flat-topped run drops it unless the block above
// is in #wall_post_override or covers the centre post shape.
func wallRaisePost(newState, above world.Block, aboveName string, aboveCovers bool) bool {
	av, _ := world.StateProperty(above, "up")
	if blockTagHas("walls", aboveName) && av == "true" {
		return true
	}
	side := func(prop string) string {
		v, _ := world.StateProperty(newState, prop)
		return v
	}
	n, e, so, w := side("north"), side("east"), side("south"), side("west")
	nN, sN, eN, wN := n == "none", so == "none", e == "none", w == "none"
	if nN && sN && wN && eN {
		return true
	}
	if nN != sN || wN != eN {
		return true
	}
	if (n == "tall" && so == "tall") || (e == "tall" && w == "tall") {
		return false
	}
	return blockTagHas("wall_post_override", aboveName) || aboveCovers
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
	if fam == connWall {
		n, e, so, w := s.connects(d, connWall, short, world.Pos{X: pos.X, Y: pos.Y, Z: pos.Z - 1}, 0, -1),
			s.connects(d, connWall, short, world.Pos{X: pos.X + 1, Y: pos.Y, Z: pos.Z}, 1, 0),
			s.connects(d, connWall, short, world.Pos{X: pos.X, Y: pos.Y, Z: pos.Z + 1}, 0, 1),
			s.connects(d, connWall, short, world.Pos{X: pos.X - 1, Y: pos.Y, Z: pos.Z}, -1, 0)
		abovePos := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
		ab := d.w.GetBlock(abovePos)
		aname, _ := world.StateOwner(ab)
		covers := wallAboveCovers(ab, strings.TrimPrefix(aname, "minecraft:"))
		setSide := func(prop string, conn bool) {
			v := "none"
			if conn {
				if covers {
					v = "tall"
				} else {
					v = "low"
				}
			}
			if nb, ok := world.WithProperty(out, prop, v); ok {
				out = nb
			}
		}
		setSide("north", n)
		setSide("east", e)
		setSide("south", so)
		setSide("west", w)
		up := wallRaisePost(out, ab, strings.TrimPrefix(aname, "minecraft:"), covers)
		if nb, ok := world.WithProperty(out, "up", boolStr(up)); ok {
			out = nb
		}
	} else {
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

// Chest merge — ChestBlock.updateShape: a chest that gains a
// same-name, same-facing partner chest takes the opposite type, and a
// chest that loses its partner returns to single. Only horizontal
// neighbours participate. getConnectedDirection: type left points
// clockwise of facing, right (and single) counter-clockwise.
func chestConnDir(typ, facing string) string {
	if typ == "left" {
		return dirCW(facing)
	}
	return dirCCW(facing)
}

// dirCW/CCW/opposite on the four horizontal directions, viewed from
// above — vanilla Direction semantics.
func dirCW(d string) string {
	switch d {
	case "north":
		return "east"
	case "east":
		return "south"
	case "south":
		return "west"
	}
	return "north"
}

func dirCCW(d string) string {
	switch d {
	case "north":
		return "west"
	case "west":
		return "south"
	case "south":
		return "east"
	}
	return "north"
}

func dirOpposite(d string) string {
	switch d {
	case "north":
		return "south"
	case "south":
		return "north"
	case "east":
		return "west"
	}
	return "east"
}

// dirByName maps a horizontal face delta to its direction name.
func dirName(dx, dz int) string {
	switch {
	case dz == -1:
		return "north"
	case dz == 1:
		return "south"
	case dx == 1:
		return "east"
	default:
		return "west"
	}
}

// chestNeighborUpdate applies ChestBlock.updateShape to the chest at
// pos when changed is a neighbour.
func (s *Server) chestNeighborUpdate(d *dimension, pos, changed world.Pos, b world.Block) {
	selfName, ok := world.StateOwner(b)
	if !ok {
		return
	}
	selfName = strings.TrimPrefix(selfName, "minecraft:")
	typ, _ := world.StateProperty(b, "type")
	facing, _ := world.StateProperty(b, "facing")
	nb := d.w.GetBlock(changed)
	nname, ok := world.StateOwner(nb)
	if !ok {
		nname = "air"
	}
	nname = strings.TrimPrefix(nname, "minecraft:")
	dx, dz := changed.X-pos.X, changed.Z-pos.Z
	dir := dirName(dx, dz)
	if nname == selfName && connHorizontal(pos, changed) {
		nt, _ := world.StateProperty(nb, "type")
		nf, _ := world.StateProperty(nb, "facing")
		if typ == "single" && nt != "single" && facing == nf &&
			chestConnDir(nt, nf) == dirOpposite(dir) {
			if nb, ok := world.WithProperty(b, "type", dirOpposite2(nt)); ok {
				s.setSilentBlock(d, pos, nb)
				s.connectionCascade(d, pos)
			}
		}
		return
	}
	if chestConnDir(typ, facing) == dir && connHorizontal(pos, changed) {
		if nb, ok := world.WithProperty(b, "type", "single"); ok {
			s.setSilentBlock(d, pos, nb)
			s.connectionCascade(d, pos)
		}
	}
}

// dirOpposite2 is ChestType.getOpposite: left<->right.
func dirOpposite2(t string) string {
	if t == "left" {
		return "right"
	}
	if t == "right" {
		return "left"
	}
	return "single"
}

// chestCandidateFacing is candidatePartnerFacing: the block dir-wise
// of pos that is the same chest block and still single yields its
// facing.
func chestCandidateFacing(d *dimension, pos world.Pos, selfName, dir string) string {
	nb := d.w.GetBlock(shiftPos(pos, dir))
	nname, ok := world.StateOwner(nb)
	if !ok || strings.TrimPrefix(nname, "minecraft:") != selfName {
		return ""
	}
	if t, _ := world.StateProperty(nb, "type"); t != "single" {
		return ""
	}
	f, _ := world.StateProperty(nb, "facing")
	return f
}

func shiftPos(pos world.Pos, dir string) world.Pos {
	switch dir {
	case "north":
		pos.Z--
	case "south":
		pos.Z++
	case "east":
		pos.X++
	case "west":
		pos.X--
	}
	return pos
}

func dirAxisX(dir string) bool { return dir == "east" || dir == "west" }

// chestPlaceState is ChestBlock.getStateForPlacement: the chest faces
// the player, and pairs with a neighbouring single same-facing chest
// — or, while sneaking on a horizontal face of a single chest, with
// that clicked chest (taking its facing and the side-appropriate
// type).
func (s *Server) chestPlaceState(d *dimension, pos world.Pos, blk world.Block, sess *Session, face int32) world.Block {
	name, ok := world.StateOwner(blk)
	if !ok {
		return blk
	}
	short := strings.TrimPrefix(name, "minecraft:")
	if short != "chest" && short != "trapped_chest" {
		return blk
	}
	_, _, _, yaw, _, _ := sess.Pos()
	facing := dirOpposite(yawDir(yaw))
	typ := "single"
	faceDir := faceDirName(int(face))
	if sess.sneaking && faceDir != "" {
		// candidatePartnerFacing(level, pos, face.opposite) — the
		// clicked block itself.
		if pf := chestCandidateFacing(d, pos, short, dirOpposite(faceDir)); pf != "" && dirAxisX(pf) != dirAxisX(faceDir) {
			facing = pf
			if dirCCW(pf) == dirOpposite(faceDir) {
				typ = "right"
			} else {
				typ = "left"
			}
		}
	}
	if typ == "single" && !sess.sneaking {
		if pf := chestCandidateFacing(d, pos, short, dirCW(facing)); pf == facing {
			typ = "left"
		} else if pf := chestCandidateFacing(d, pos, short, dirCCW(facing)); pf == facing {
			typ = "right"
		}
	}
	out := blk
	if nb, ok := world.WithProperty(out, "facing", facing); ok {
		out = nb
	}
	if nb, ok := world.WithProperty(out, "type", typ); ok {
		out = nb
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

// shiftPosBack steps pos against the given face direction.
func shiftPosBack(pos world.Pos, dir string) world.Pos {
	return shiftPos(pos, dirOpposite(dir))
}

// yawDir maps yaw to the horizontal direction the player faces
// (0=south, 1=west, 2=north, 3=east in vanilla's GetNearest).
// faceDirName maps a clicked-face protocol id (0=-Y 1=+Y 2=-Z
// 3=+Z 4=-X 5=+X) to a horizontal direction name; "" for vertical.
func faceDirName(face int) string {
	switch face {
	case 2:
		return "north"
	case 3:
		return "south"
	case 4:
		return "west"
	case 5:
		return "east"
	}
	return ""
}

func yawDir(yaw float32) string {
	switch int(math.Floor(float64(yaw)*4.0/360.0+0.5)) & 3 {
	case 1:
		return "west"
	case 2:
		return "north"
	case 3:
		return "east"
	}
	return "south"
}

// StairBlock.updateShape — a horizontal neighbour write re-evaluates
// the whole shape prop (vertical changes don't touch it).
func (s *Server) stairsNeighborUpdate(d *dimension, pos, changed world.Pos, b world.Block) {
	_ = changed
	if shape, ok := s.stairsShapeAt(d, pos, b); ok {
		cur, _ := world.StateProperty(b, "shape")
		if cur != shape {
			if nb, ok := world.WithProperty(b, "shape", shape); ok {
				s.setSilentBlock(d, pos, nb)
				s.connectionCascade(d, pos)
			}
		}
	}
}

func isStairName(name string) bool { return strings.HasSuffix(name, "_stairs") }

// stairsShapeAt is getStairsShape: the front stair (facing-side) makes
// an outer corner when it faces perpendicular and isn't claimed by an
// identically-oriented stair on its far side; the back stair makes an
// inner corner under the same rule.
func (s *Server) stairsShapeAt(d *dimension, pos world.Pos, b world.Block) (string, bool) {
	facing, ok := world.StateProperty(b, "facing")
	if !ok {
		return "", false
	}
	half, _ := world.StateProperty(b, "half")
	front := d.w.GetBlock(shiftPos(pos, facing))
	if fname, ok := world.StateOwner(front); ok && isStairName(strings.TrimPrefix(fname, "minecraft:")) {
		fh, _ := world.StateProperty(front, "half")
		ff, _ := world.StateProperty(front, "facing")
		if fh == half && dirAxisX(ff) != dirAxisX(facing) &&
			s.stairCanTake(d, pos, b, dirOpposite(ff)) {
			if ff == dirCCW(facing) {
				return "outer_left", true
			}
			return "outer_right", true
		}
	}
	back := d.w.GetBlock(shiftPos(pos, dirOpposite(facing)))
	if bname, ok := world.StateOwner(back); ok && isStairName(strings.TrimPrefix(bname, "minecraft:")) {
		bh, _ := world.StateProperty(back, "half")
		bf, _ := world.StateProperty(back, "facing")
		if bh == half && dirAxisX(bf) != dirAxisX(facing) &&
			s.stairCanTake(d, pos, b, bf) {
			if bf == dirCCW(facing) {
				return "inner_left", true
			}
			return "inner_right", true
		}
	}
	return "straight", true
}

// stairCanTake is canTakeShape: a corner is free for self unless the
// stair in that offset faces and halves exactly like self (then that
// stair is the corner instead).
func (s *Server) stairCanTake(d *dimension, pos world.Pos, self world.Block, dir string) bool {
	n := d.w.GetBlock(shiftPos(pos, dir))
	nname, ok := world.StateOwner(n)
	if !ok || !isStairName(strings.TrimPrefix(nname, "minecraft:")) {
		return true
	}
	sf, _ := world.StateProperty(self, "facing")
	sh, _ := world.StateProperty(self, "half")
	nf, _ := world.StateProperty(n, "facing")
	nh, _ := world.StateProperty(n, "half")
	return nf != sf || nh != sh
}

// stairsPlaceState is StairBlock.getStateForPlacement: facing is the
// player's horizontal direction, half is bottom on the top face / top
// on the bottom face / by click height on side faces, then the shape
// fold runs on the placed state.
func (s *Server) stairsPlaceState(d *dimension, pos world.Pos, blk world.Block, sess *Session, face int32, cursorY float64) world.Block {
	name, ok := world.StateOwner(blk)
	if !ok || !isStairName(strings.TrimPrefix(name, "minecraft:")) {
		return blk
	}
	_, _, _, yaw, _, _ := sess.Pos()
	out := blk
	if nb, ok := world.WithProperty(out, "facing", yawDir(yaw)); ok {
		out = nb
	}
	half := "bottom"
	switch face {
	case 0: // clicked bottom face: stair hangs top-down
		half = "top"
	case 1:
		half = "bottom"
	default:
		if cursorY > 0.5 {
			half = "top"
		}
	}
	if nb, ok := world.WithProperty(out, "half", half); ok {
		out = nb
	}
	if shape, ok := s.stairsShapeAt(d, pos, out); ok {
		if nb, ok2 := world.WithProperty(out, "shape", shape); ok2 {
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

// chorusNeighborUpdate is ChorusPlantBlock.updateShape: only the
// changed direction's prop refolds — the changed cell connects when
// it's a chorus plant or flower, and `down` also to
// #supports_chorus_plant (end stone). Vertical changes count.
func (s *Server) chorusNeighborUpdate(d *dimension, pos, changed world.Pos, b world.Block) {
	dx, dy, dz := changed.X-pos.X, changed.Y-pos.Y, changed.Z-pos.Z
	if dx*dx+dy*dy+dz*dz != 1 || changed == pos {
		return
	}
	var prop string
	switch {
	case dy > 0:
		prop = "up"
	case dy < 0:
		prop = "down"
	default:
		prop, _ = connPropByDir(dx, dz)
	}
	if prop == "" {
		return
	}
	nb := d.w.GetBlock(changed)
	nname, _ := world.StateOwner(nb)
	nname = strings.TrimPrefix(nname, "minecraft:")
	conn := nname == "chorus_plant" || nname == "chorus_flower" ||
		(prop == "down" && blockTagHas("supports_chorus_plant", nname))
	cur, _ := world.StateProperty(b, prop)
	if cur != boolStr(conn) {
		if nb2, ok := world.WithProperty(b, prop, boolStr(conn)); ok {
			s.setSilentBlock(d, pos, nb2)
			s.connectionCascade(d, pos)
		}
	}
}

// snowyNeighborUpdate is SnowyBlock.updateShape: a write to the cell
// directly above sets snowy = above.is(#snow).
func (s *Server) snowyNeighborUpdate(d *dimension, pos, changed world.Pos, b world.Block) {
	aname, _ := world.StateOwner(d.w.GetBlock(changed))
	aname = strings.TrimPrefix(aname, "minecraft:")
	want := boolStr(blockTagHas("snow", aname))
	cur, _ := world.StateProperty(b, "snowy")
	if cur != want {
		if nb, ok := world.WithProperty(b, "snowy", want); ok {
			s.setSilentBlock(d, pos, nb)
			s.connectionCascade(d, pos)
		}
	}
}

// selfFoldShapes mirrors Block.updateFromNeighbourShapes: a command
// write (BlockInput.place — setblock/fill) folds the placed block
// against each of its six neighbours before the write lands. It is
// why a setblock'd chorus_plant reads down=true on end_stone, a
// setblock'd tripwire reads its back-facing hooks, and a grass_block
// under a fresh snow layer arrives snowy. Gameplay writes
// (dig/piston/fluid flow) fold only via the neighbour fan-out.
func (s *Server) selfFoldShapes(d *dimension, pos world.Pos) {
	b := d.w.GetBlock(pos)
	name, _ := world.StateOwner(b)
	short := strings.TrimPrefix(name, "minecraft:")
	var anchor world.Pos
	var f string
	isHook := short == "tripwire_hook"
	if isHook {
		f, _ = world.StateProperty(b, "facing")
		anchor = hookAnchor(pos, f)
	}
	// UPDATE_SHAPE_ORDER: west, east, north, south, down, up.
	for _, p := range []world.Pos{
		{X: pos.X - 1, Y: pos.Y, Z: pos.Z},
		{X: pos.X + 1, Y: pos.Y, Z: pos.Z},
		{X: pos.X, Y: pos.Y, Z: pos.Z - 1},
		{X: pos.X, Y: pos.Y, Z: pos.Z + 1},
		posBelow(pos, 1),
		posAbove(pos, 1),
	} {
		// A setblock'd hook's updateShape can fold to air on a
		// missing anchor — BlockInput.place discards an air fold
		// and the requested state stands.
		if isHook && p == anchor && !s.hookCanSurvive(d, pos, f) {
			continue
		}
		s.connectionUpdateNoCascade(d, pos, p)
	}
}

// coralLive is the name-level CoralBlock/CoralPlantBlock/CoralFanBlock
// family — dead variants don't scan.
func coralLive(short string) bool {
	if strings.HasPrefix(short, "dead_") {
		return false
	}
	for _, c := range []string{"tube", "brain", "bubble", "fire", "horn"} {
		if short == c+"_coral" || short == c+"_coral_block" ||
			short == c+"_coral_fan" || short == c+"_coral_wall_fan" {
			return true
		}
	}
	return false
}

// coralWaterScan is scanForWater: any of the six neighbours' fluid is
// water — a waterlogged block's fluid state is water too.
func (s *Server) coralWaterScan(d *dimension, pos world.Pos) bool {
	for _, p := range []world.Pos{
		{X: pos.X, Y: pos.Y + 1, Z: pos.Z}, {X: pos.X, Y: pos.Y - 1, Z: pos.Z},
		{X: pos.X, Y: pos.Y, Z: pos.Z - 1}, {X: pos.X, Y: pos.Y, Z: pos.Z + 1},
		{X: pos.X - 1, Y: pos.Y, Z: pos.Z}, {X: pos.X + 1, Y: pos.Y, Z: pos.Z},
	} {
		nb := d.w.GetBlock(p)
		if k, _, ok := fluidOf(nb); ok && k == fluidWater {
			return true
		}
		if wl, ok := world.StateProperty(nb, "waterlogged"); ok && wl == "true" {
			return true
		}
	}
	return false
}

// coralNeighborUpdate is CoralBlock.updateShape: no water in contact
// schedules the die tick at 60+rng(40).
func (s *Server) coralNeighborUpdate(d *dimension, pos world.Pos) {
	if !s.coralWaterScan(d, pos) {
		s.scheduleBlockTick(d, pos, int64(60+s.rng.intn(40)))
	}
}

// coralTick is CoralBlock.tick/tryDie: still dry → dead variant keeps
// the facing/waterlogged props.
func (s *Server) coralTick(d *dimension, pos world.Pos, b world.Block) {
	name, ok := world.StateOwner(b)
	if !ok {
		return
	}
	short := strings.TrimPrefix(name, "minecraft:")
	if !coralLive(short) || s.coralWaterScan(d, pos) {
		return
	}
	dead := "dead_" + short
	props := map[string]string{}
	for _, p := range []string{"facing", "waterlogged"} {
		if v, ok := world.StateProperty(b, p); ok {
			props[p] = v
		}
	}
	if nb, ok := world.BlockStateOf(dead, props); ok {
		s.setSilentBlock(d, pos, nb)
	}
}

// scaffoldDistance is ScaffoldingBlock.getDistance: a scaffolding below
// shares its distance; a sturdy up-face below grounds to 0; otherwise
// one past the nearest horizontal scaffold, capped at 7.
func (s *Server) scaffoldDistance(d *dimension, pos world.Pos) int {
	below := d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	bname, _ := world.StateOwner(below)
	bname = strings.TrimPrefix(bname, "minecraft:")
	if bname == "scaffolding" {
		if dv, ok := world.StateProperty(below, "distance"); ok {
			if n, err := strconv.Atoi(dv); err == nil {
				return n
			}
		}
		return 7
	}
	if connectSturdy(d, world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}, below, bname) {
		return 0
	}
	dist := 7
	for _, dir := range cardinalDirs {
		nb := d.w.GetBlock(world.Pos{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz})
		nname, _ := world.StateOwner(nb)
		if strings.TrimPrefix(nname, "minecraft:") != "scaffolding" {
			continue
		}
		dv, ok := world.StateProperty(nb, "distance")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(dv); err == nil && n+1 < dist {
			dist = n + 1
			if dist == 1 {
				return dist
			}
		}
	}
	return dist
}

// scaffoldTick is ScaffoldingBlock.tick: distance and bottom refold; a
// newly-arrived distance 7 drops the block as an item, one that was
// already at 7 becomes a falling block.
func (s *Server) scaffoldTick(d *dimension, pos world.Pos, b world.Block) {
	name, ok := world.StateOwner(b)
	if !ok || strings.TrimPrefix(name, "minecraft:") != "scaffolding" {
		return
	}
	dist := s.scaffoldDistance(d, pos)
	below := d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	bname, _ := world.StateOwner(below)
	bottom := dist > 0 && strings.TrimPrefix(bname, "minecraft:") != "scaffolding"
	oldDist, _ := world.StateProperty(b, "distance")
	if dist == 7 {
		if oldDist == "7" {
			// FallingBlockEntity.fall — lands on the first sturdy
			// cell below (or drops out of the world) and re-places.
			y := pos.Y - 1
			for ; y >= d.minY; y-- {
				cb := d.w.GetBlock(world.Pos{X: pos.X, Y: y, Z: pos.Z})
				if !replaceableByFluid(cb) {
					break
				}
			}
			if y < d.minY {
				s.setNaturalBlock(d, pos, world.Air)
				return
			}
			s.setNaturalBlock(d, pos, world.Air)
			land := world.Pos{X: pos.X, Y: y + 1, Z: pos.Z}
			if nb, ok := world.BlockStateOf("scaffolding", map[string]string{
				"distance": strconv.Itoa(s.scaffoldDistance(d, land)),
				"bottom":   "false", "waterlogged": "false",
			}); ok {
				below2 := d.w.GetBlock(world.Pos{X: land.X, Y: land.Y - 1, Z: land.Z})
				b2name, _ := world.StateOwner(below2)
				if strings.TrimPrefix(b2name, "minecraft:") != "scaffolding" {
					nb, _ = world.WithProperty(nb, "bottom", "true")
				}
				s.setNaturalBlock(d, land, nb)
			}
			return
		}
		s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
		s.setNaturalBlock(d, pos, world.Air)
		if id, ok := world.ItemID("scaffolding"); ok {
			s.spawnItemDrop(d.name, pos, ItemStack{ID: id, Count: 1})
		}
		return
	}
	if oldDist != strconv.Itoa(dist) {
		if nb, ok := world.BlockStateOf("scaffolding", map[string]string{
			"distance":    strconv.Itoa(dist),
			"bottom":      boolStr(bottom),
			"waterlogged": propOr(b, "waterlogged", "false"),
		}); ok {
			s.setSilentBlock(d, pos, nb)
			s.connectionCascade(d, pos)
		}
	}
}

func propOr(b world.Block, prop, def string) string {
	if v, ok := world.StateProperty(b, prop); ok {
		return v
	}
	return def
}

// powderNeighborUpdate is ConcretePowderBlock.updateShape: any
// neighbour's fluid touching the cell (water, incl. waterlogged
// holders) solidifies it to the concrete block.
func (s *Server) powderNeighborUpdate(d *dimension, pos world.Pos) {
	b := d.w.GetBlock(pos)
	name, ok := world.StateOwner(b)
	if !ok {
		return
	}
	short := strings.TrimPrefix(name, "minecraft:")
	if !strings.HasSuffix(short, "_concrete_powder") {
		return
	}
	// touchesLiquid scans five faces — DOWN is skipped unless the
	// powder itself holds water (powder never does), so water below
	// never solidifies it.
	for _, p := range []world.Pos{
		{X: pos.X, Y: pos.Y + 1, Z: pos.Z},
		{X: pos.X, Y: pos.Y, Z: pos.Z - 1}, {X: pos.X, Y: pos.Y, Z: pos.Z + 1},
		{X: pos.X - 1, Y: pos.Y, Z: pos.Z}, {X: pos.X + 1, Y: pos.Y, Z: pos.Z},
	} {
		nb := d.w.GetBlock(p)
		if k, _, ok := fluidOf(nb); ok && k == fluidWater {
			if solid, ok := world.BlockStateOf(strings.TrimSuffix(short, "_powder"), nil); ok {
				s.setSilentBlock(d, pos, solid)
				s.connectionCascade(d, pos)
			}
			return
		}
		if wl, ok := world.StateProperty(nb, "waterlogged"); ok && wl == "true" {
			if solid, ok := world.BlockStateOf(strings.TrimSuffix(short, "_powder"), nil); ok {
				s.setSilentBlock(d, pos, solid)
				s.connectionCascade(d, pos)
			}
			return
		}
	}
}

// tripwireConnects is TripWireBlock.shouldConnectTo: a tripwire cell
// connects to a tripwire neighbour, or to a hook whose facing points
// back along the wire's direction (hook facing = toward the wire).
func tripwireConnects(nb world.Block, dir string) bool {
	nname, ok := world.StateOwner(nb)
	if !ok {
		return false
	}
	nname = strings.TrimPrefix(nname, "minecraft:")
	if nname == "tripwire_hook" {
		f, _ := world.StateProperty(nb, "facing")
		return f == dirOpposite(dir)
	}
	return nname == "tripwire"
}

// tripwireNeighborUpdate is TripWireBlock.updateShape: horizontal
// changes refold just that direction's bool.
func (s *Server) tripwireNeighborUpdate(d *dimension, pos, changed world.Pos, b world.Block) {
	prop, _ := connPropByDir(changed.X-pos.X, changed.Z-pos.Z)
	if prop == "" {
		return
	}
	conn := tripwireConnects(d.w.GetBlock(changed), prop)
	cur, _ := world.StateProperty(b, prop)
	if cur != boolStr(conn) {
		if nb, ok := world.WithProperty(b, prop, boolStr(conn)); ok {
			s.setSilentBlock(d, pos, nb)
			s.connectionCascade(d, pos)
		}
	}
}

// snowyPlaceState, tripwirePlaceState and scaffoldPlaceState are the
// getStateForPlacement halves of the fold family.
func (s *Server) snowyPlaceState(d *dimension, pos world.Pos, blk world.Block) world.Block {
	if _, has := world.StateProperty(blk, "snowy"); !has {
		return blk
	}
	aname, _ := world.StateOwner(d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}))
	aname = strings.TrimPrefix(aname, "minecraft:")
	if nb, ok := world.WithProperty(blk, "snowy", boolStr(blockTagHas("snow", aname))); ok {
		return nb
	}
	return blk
}

func (s *Server) tripwirePlaceState(d *dimension, pos world.Pos, blk world.Block) world.Block {
	name, ok := world.StateOwner(blk)
	if !ok || strings.TrimPrefix(name, "minecraft:") != "tripwire" {
		return blk
	}
	out := blk
	for _, dir := range cardinalDirs {
		prop, _ := connPropByDir(dir.dx, dir.dz)
		conn := tripwireConnects(d.w.GetBlock(world.Pos{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz}), prop)
		if nb, ok := world.WithProperty(out, prop, boolStr(conn)); ok {
			out = nb
		}
	}
	return out
}

func (s *Server) scaffoldPlaceState(d *dimension, pos world.Pos, blk world.Block) world.Block {
	name, ok := world.StateOwner(blk)
	if !ok || strings.TrimPrefix(name, "minecraft:") != "scaffolding" {
		return blk
	}
	dist := s.scaffoldDistance(d, pos)
	below := d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	bname, _ := world.StateOwner(below)
	if nb, ok := world.BlockStateOf("scaffolding", map[string]string{
		"distance":    strconv.Itoa(dist),
		"bottom":      boolStr(dist > 0 && strings.TrimPrefix(bname, "minecraft:") != "scaffolding"),
		"waterlogged": propOr(blk, "waterlogged", "false"),
	}); ok {
		return nb
	}
	return blk
}

// hookAnchor is the cell a tripwire hook mounts to: the cell on the
// side opposite its facing.
func hookAnchor(pos world.Pos, facing string) world.Pos {
	return shiftPos(pos, dirOpposite(facing))
}

// hookCanSurvive is TripWireHookBlock.canSurvive: the anchor's face
// toward the hook direction is sturdy.
func (s *Server) hookCanSurvive(d *dimension, pos world.Pos, facing string) bool {
	a := hookAnchor(pos, facing)
	ab := d.w.GetBlock(a)
	aname, ok := world.StateOwner(ab)
	if !ok {
		return false
	}
	return connectSturdy(d, a, ab, strings.TrimPrefix(aname, "minecraft:"))
}

// hookNeighborUpdate is TripWireHookBlock.updateShape: a change on the
// anchor side pops the hook; the wire cell writing beside it triggers
// the run recalc (calculateState).
func (s *Server) hookNeighborUpdate(d *dimension, pos, changed world.Pos, b world.Block) {
	facing, ok := world.StateProperty(b, "facing")
	if !ok {
		return
	}
	dx, dy, dz := changed.X-pos.X, changed.Y-pos.Y, changed.Z-pos.Z
	if dx*dx+dy*dy+dz*dz != 1 {
		return
	}
	var dir string
	switch {
	case dy > 0:
		dir = "up"
	case dy < 0:
		dir = "down"
	default:
		dir, _ = connPropByDir(dx, dz)
	}
	if dir == dirOpposite(facing) && !s.hookCanSurvive(d, pos, facing) {
		s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
		s.setNaturalBlock(d, pos, world.Air)
		if id, ok := world.ItemID("tripwire_hook"); ok {
			s.spawnItemDrop(d.name, pos, ItemStack{ID: id, Count: 1})
		}
		return
	}
	s.hookRecalc(d, pos, facing)
}

// hookRecalc is TripWireHookBlock.calculateState: walk the facing run
// (up to 42 cells); a contiguous wire run reaching an opposite-facing
// hook attaches both ends; the walk rewrites the far hook's facing to
// point back, and each end's powered flag ors the run's powered wires.
func (s *Server) hookRecalc(d *dimension, pos world.Pos, facing string) {
	contiguous := true
	powered := false
	partner := -1
	for j := 1; j < 42; j++ {
		cp := pos
		for k := 0; k < j; k++ {
			cp = shiftPos(cp, facing)
		}
		b := d.w.GetBlock(cp)
		n, _ := world.StateOwner(b)
		n = strings.TrimPrefix(n, "minecraft:")
		if n == "tripwire_hook" {
			f2, _ := world.StateProperty(b, "facing")
			if f2 == dirOpposite(facing) {
				partner = j
			}
			break
		}
		if n != "tripwire" {
			contiguous = false
			break
		}
		if p, _ := world.StateProperty(b, "powered"); p == "true" {
			powered = true
		}
	}
	attached := contiguous && partner > 1
	powered = powered && attached
	if partner > 0 {
		hp := pos
		for k := 0; k < partner; k++ {
			hp = shiftPos(hp, facing)
		}
		nb := d.w.GetBlock(hp)
		out := nb
		for k, v := range map[string]string{"attached": boolStr(attached), "powered": boolStr(powered), "facing": dirOpposite(facing)} {
			if w, ok := world.WithProperty(out, k, v); ok {
				out = w
			}
		}
		if out != nb {
			s.setSilentBlock(d, hp, out)
		}
	}
	self := d.w.GetBlock(pos)
	out := self
	for k, v := range map[string]string{"attached": boolStr(attached), "powered": boolStr(powered)} {
		if w, ok := world.WithProperty(out, k, v); ok {
			out = w
		}
	}
	if out != self {
		s.setSilentBlock(d, pos, out)
	}
}

// tripwireHooksAround reruns calculateState on every hook whose run
// reaches pos — called after a wire cell writes.
func (s *Server) tripwireHooksAround(d *dimension, pos world.Pos) {
	for _, dir := range cardinalDirs {
		for j := 1; j < 42; j++ {
			cp := world.Pos{X: pos.X + dir.dx*j, Y: pos.Y, Z: pos.Z + dir.dz*j}
			b := d.w.GetBlock(cp)
			n, _ := world.StateOwner(b)
			n = strings.TrimPrefix(n, "minecraft:")
			if n == "tripwire_hook" {
				dn, _ := connPropByDir(dir.dx, dir.dz)
				if f, ok := world.StateProperty(b, "facing"); ok && f == dirOpposite(dn) {
					s.hookRecalc(d, cp, f)
				}
				break
			}
			if n != "tripwire" {
				break
			}
		}
	}
}

// hookPlaceState is TripWireHookBlock.getStateForPlacement: iterate
// horizontal looking directions; facing = candidate.opposite for the
// first candidate whose anchor cell (at candidate) is sturdy.
func (s *Server) hookPlaceState(d *dimension, pos world.Pos, blk world.Block, sess *Session, face int32) world.Block {
	name, ok := world.StateOwner(blk)
	if !ok || strings.TrimPrefix(name, "minecraft:") != "tripwire_hook" {
		return blk
	}
	_, _, _, yaw, _, _ := sess.Pos()
	fdir := faceDirName(int(face))
	var cands []string
	if fdir == "north" || fdir == "south" || fdir == "east" || fdir == "west" {
		cands = append(cands, fdir)
	}
	yawD := yawDir(yaw)
	for _, c := range []string{yawD, dirCCW(yawD), dirCW(yawD), dirOpposite(yawD)} {
		dup := false
		for _, e := range cands {
			if e == c {
				dup = true
			}
		}
		if !dup {
			cands = append(cands, c)
		}
	}
	for _, c := range cands {
		anchor := shiftPos(pos, c)
		ab := d.w.GetBlock(anchor)
		an, ok := world.StateOwner(ab)
		if !ok || !connectSturdy(d, anchor, ab, strings.TrimPrefix(an, "minecraft:")) {
			continue
		}
		out := blk
		for k, v := range map[string]string{"facing": dirOpposite(c), "powered": "false", "attached": "false"} {
			if w, ok := world.WithProperty(out, k, v); ok {
				out = w
			}
		}
		return out
	}
	return blk
}
