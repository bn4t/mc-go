package server

import (
	"math"
	"math/rand"
	"strings"

	"mc-go/internal/world"
)

// Igniter items — FlintAndSteelItem.useOn and FireChargeItem.useOn
// (measured on the vanilla 26.2 jar):
//
//   - campfire/candle/candle-cake light branch: the clicked block is
//     #campfires|#candles && !waterlogged && !lit, or #candle_cakes &&
//     !lit — playSound at the clicked pos, setBlock(LIT, flag 11),
//     wear the flint or shrink the charge, SUCCESS. (Fire charge
//     shrinks even lighting a campfire; flint wears either way.)
//   - otherwise the cell past the clicked face must pass
//     BaseFireBlock.canBePlacedAt: air, and the fire state canSurvive
//     or an obsidian frame completes a portal shape on the counter-
//     clockwise axis of the player's facing. Sound, flag-11 write of
//     BaseFireBlock.getState (soul_fire on a #soul_fire_base_blocks
//     floor, otherwise FireBlock.getStateForPlacement), then the same
//     wear/consume. FAIL does nothing beyond the ack.

const (
	sndFlintSteelUse int32 = 642 // item.flintandsteel.use
	sndFireChargeUse int32 = 626 // item.firecharge.use
)

// fireIgnitableClick is CampfireBlock.canLight / CandleBlock.canLight /
// CandleCakeBlock.canLight folded: the tag gate plus the unlit /
// unwaterlogged conditions each bakes in.
func fireIgnitableClick(b world.Block) bool {
	name, ok := world.StateOwner(b)
	if !ok {
		return false
	}
	lit, hasLit := world.StateProperty(b, "lit")
	if !hasLit || lit == "true" {
		return false
	}
	short := strings.TrimPrefix(name, "minecraft:")
	if blockTagHas("candle_cakes", short) {
		return true
	}
	if !blockTagHas("campfires", short) && !blockTagHas("candles", short) {
		return false
	}
	if w, ok := world.StateProperty(b, "waterlogged"); ok && w == "true" {
		return false
	}
	return true
}

// faceAxisX picks the horizontal axis the portal probe runs on:
// vanilla reads UseOnContext.getHorizontalDirection (the player's
// facing) and takes its counterClockWise axis — north/south faces
// give X, east/west give Z.
func sessFaceAxisX(sess *Session) bool {
	_, _, _, yaw, _, _ := sess.Pos()
	d := int(math.Floor(float64(yaw)*4.0/360.0+0.5)) & 3 // 0=S 1=W 2=N 3=E
	return d == 0 || d == 2
}

// useIgniter runs the shared flint_and_steel / fire_charge useOn body.
// Reports whether the packet is consumed (always — a FAIL only acks).
func (s *Server) useIgniter(sess *Session, d *dimension, clicked world.Pos, face int32) bool {
	return s.useIgniterInHand(sess, d, clicked, face, 0)
}

func (s *Server) useIgniterInHand(sess *Session, d *dimension, clicked world.Pos, face int32, hand int32) bool {
	held, slot := sess.stackForHand(hand)
	if held.Empty() {
		return true
	}
	iname, ok := itemNameOf(held.ID)
	if !ok {
		return true
	}
	charge := strings.TrimPrefix(iname, "minecraft:") == "fire_charge"
	if !charge && strings.TrimPrefix(iname, "minecraft:") != "flint_and_steel" {
		return false
	}
	cur := d.w.GetBlock(clicked)
	if fireIgnitableClick(cur) {
		s.igniterSound(d, clicked, charge)
		lit, ok := world.WithProperty(cur, "lit", "true")
		if !ok {
			return true
		}
		s.setNaturalBlock(d, clicked, lit)
		s.igniterConsumeSlot(sess, held, slot, charge)
		return true
	}
	dx, dy, dz, ok := faceOffset(face)
	if !ok {
		return true
	}
	target := world.Pos{X: clicked.X + int(dx), Y: clicked.Y + int(dy), Z: clicked.Z + int(dz)}
	if target.Y < d.minY || target.Y > d.minY+d.height-1 {
		return true
	}
	if !s.fireCanBePlacedAt(d, target, sessFaceAxisX(sess)) {
		return true
	}
	s.igniterSound(d, target, charge)
	placed := fireGetState(d, target)
	s.setNaturalBlock(d, target, placed)
	if bname, ok := world.StateOwner(placed); ok {
		s.firePlacedBlock(sess, strings.TrimPrefix(bname, "minecraft:"))
	}
	s.igniterConsumeSlot(sess, held, slot, charge)
	return true
}

// igniterSound is the per-item playSound: flint pitch 0.8+rand*0.4 at
// the lit/ignited block; fire charge pitch 1+(rand-0.5)*0.2.
func (s *Server) igniterSound(d *dimension, pos world.Pos, charge bool) {
	if charge {
		s.playSoundSrc(d.name, float64(pos.X), float64(pos.Y), float64(pos.Z),
			sndFireChargeUse, 4, 1, 1+float32(rand.Float64()-0.5)*0.2, nil)
		return
	}
	s.playSoundSrc(d.name, float64(pos.X), float64(pos.Y), float64(pos.Z),
		sndFlintSteelUse, 4, 1, float32(0.8+rand.Float64()*0.4), nil)
}

// igniterConsume applies the item cost: flint hurtAndBreak(1) —
// creative-exempt through wearSlot. The item use creative wrapper
// restores the fire-charge count after its successful use.
func (s *Server) igniterConsume(sess *Session, held ItemStack, charge bool) {
	s.igniterConsumeSlot(sess, held, invSlotForHotbar(sess.held.Load()), charge)
}

func (s *Server) igniterConsumeSlot(sess *Session, held ItemStack, slot int16, charge bool) {
	sess.addItemStat(statUsed, held.ID, 1)
	if charge {
		if sess.mode() != GameModeCreative {
			sess.consumeSlot(slot, 1)
		}
		return
	}
	s.wearSlot(sess, slot, 1)
}

// fireGetState is BaseFireBlock.getState: soul_fire on a soul base,
// else the direction-folded placement state.
func fireGetState(d *dimension, pos world.Pos) world.Block {
	if soulFireBase(d.w.GetBlock(posBelow(pos, 1))) {
		return soulFireBlock
	}
	return fireStateForPlacement(d, pos)
}

// fireCanBePlacedAt is BaseFireBlock.canBePlacedAt: the cell is air and
// the placement state survives, or the cell closes an empty portal
// shape on the facing axis.
func (s *Server) fireCanBePlacedAt(d *dimension, pos world.Pos, axisX bool) bool {
	if !isAirCell(d.w.GetBlock(pos)) {
		return false
	}
	return fireCanSurvive(d, pos, fireGetState(d, pos)) || s.fireIsPortal(d, pos, axisX)
}

// fireIsPortal is BaseFireBlock.isPortal: a portal dimension, some
// neighbor obsidian, and an empty shape found.
func (s *Server) fireIsPortal(d *dimension, pos world.Pos, axisX bool) bool {
	if !firePortalDim(d) {
		return false
	}
	for _, np := range neighbourPositions(pos) {
		if isBlockNamed(d.w.GetBlock(np), "obsidian") {
			return s.findEmptyPortalShape(d, pos, axisX) != nil
		}
	}
	return false
}

// firePortalDim is inPortalDimension: overworld or nether.
func firePortalDim(d *dimension) bool {
	return d.name == dimOverworld || d.name == dimNether
}

// portalShape is the measured PortalShape fields the lighting path
// needs: axis, bounds anchor, extents and the interior portal-cell
// count (nonzero fails the empty-shape predicate).
type portalShape struct {
	axisX        bool
	portalBlocks int
	bottomLeft   world.Pos
	width        int
	height       int
}

// findEmptyPortalShape is PortalShape.findEmptyPortalShape:
// findPortalShape filtered to isValid && numPortalBlocks == 0.
func (s *Server) findEmptyPortalShape(d *dimension, pos world.Pos, axisX bool) *portalShape {
	if p := s.findAnyShape(d, pos, axisX); p != nil && p.valid() && p.portalBlocks == 0 {
		return p
	}
	if p := s.findAnyShape(d, pos, !axisX); p != nil && p.valid() && p.portalBlocks == 0 {
		return p
	}
	return nil
}

func (p *portalShape) valid() bool {
	return p.width >= 2 && p.width <= 21 && p.height >= 3 && p.height <= 21
}

// findAnyShape is PortalShape.findAnyShape: rightDir scans WEST along
// X portals and SOUTH along Z portals.
func (s *Server) findAnyShape(d *dimension, pos world.Pos, axisX bool) *portalShape {
	right := [2]int{-1, 0}
	if !axisX {
		right = [2]int{0, 1}
	}
	bottomLeft, ok := portalBottomLeft(d, right, pos)
	if !ok {
		return nil
	}
	width := portalDistanceUntilEdge(d, bottomLeft, right)
	if width < 2 || width > 21 {
		return nil
	}
	p := &portalShape{axisX: axisX, bottomLeft: bottomLeft, width: width}
	p.height = portalHeight(d, bottomLeft, right, width, p)
	return p
}

// portalIsEmpty is PortalShape.isEmpty: air, #fire or nether_portal.
func portalIsEmpty(b world.Block) bool {
	if b == world.Air || isAirCell(b) {
		return true
	}
	if name, ok := world.StateOwner(b); ok {
		short := strings.TrimPrefix(name, "minecraft:")
		return blockTagHas("fire", short) || short == "nether_portal"
	}
	return false
}

// portalIsFrame is the FRAME predicate: obsidian.
func portalIsFrame(b world.Block) bool { return isBlockNamed(b, "obsidian") }

// portalBottomLeft is calculateBottomLeft: sink the seed through empty
// cells (bounded at y-21 / minY), then walk rightDir.getOpposite()
// while cells are empty and above frame.
func portalBottomLeft(d *dimension, right [2]int, pos world.Pos) (world.Pos, bool) {
	limit := pos.Y - 21
	if d.minY > limit {
		limit = d.minY
	}
	for pos.Y > limit && portalIsEmpty(d.w.GetBlock(posBelow(pos, 1))) {
		pos = posBelow(pos, 1)
	}
	opp := [2]int{-right[0], -right[1]}
	i := portalDistanceUntilEdge(d, pos, opp) - 1
	if i < 0 {
		return world.Pos{}, false
	}
	return world.Pos{X: pos.X + opp[0]*i, Y: pos.Y, Z: pos.Z + opp[1]*i}, true
}

// portalDistanceUntilEdge is getDistanceUntilEdgeAboveFrame: the count
// of empty-above-frame cells until the first non-empty FRAME cell
// along dir, capped at 22. Zero when the line is broken early.
func portalDistanceUntilEdge(d *dimension, pos world.Pos, dir [2]int) int {
	for i := 0; i <= 21; i++ {
		cell := world.Pos{X: pos.X + dir[0]*i, Y: pos.Y, Z: pos.Z + dir[1]*i}
		b := d.w.GetBlock(cell)
		if !portalIsEmpty(b) {
			if portalIsFrame(b) {
				return i
			}
			return 0
		}
		if !portalIsFrame(d.w.GetBlock(posBelow(cell, 1))) {
			return 0
		}
	}
	return 0
}

// portalHeight is calculateHeight + getDistanceUntilTop + hasTopFrame.
func portalHeight(d *dimension, bottomLeft world.Pos, right [2]int, width int, p *portalShape) int {
	i := portalDistanceUntilTop(d, bottomLeft, right, width, p)
	if i < 3 || i > 21 {
		return 0
	}
	for k := 0; k < width; k++ {
		cell := world.Pos{
			X: bottomLeft.X + right[0]*k,
			Y: bottomLeft.Y + i,
			Z: bottomLeft.Z + right[1]*k,
		}
		if !portalIsFrame(d.w.GetBlock(cell)) {
			return 0
		}
	}
	return i
}

// portalDistanceUntilTop scans each row up to 21: the cells one step
// outside both edges and every interior cell must be frame-bordered
// and empty respectively; interior nether_portal cells count toward
// numPortalBlocks.
func portalDistanceUntilTop(d *dimension, bottomLeft world.Pos, right [2]int, width int, p *portalShape) int {
	for i := 0; i < 21; i++ {
		y := bottomLeft.Y + i
		l := world.Pos{X: bottomLeft.X - right[0], Y: y, Z: bottomLeft.Z - right[1]}
		if !portalIsFrame(d.w.GetBlock(l)) {
			return i
		}
		r := world.Pos{X: bottomLeft.X + right[0]*width, Y: y, Z: bottomLeft.Z + right[1]*width}
		if !portalIsFrame(d.w.GetBlock(r)) {
			return i
		}
		for k := 0; k < width; k++ {
			cell := world.Pos{X: bottomLeft.X + right[0]*k, Y: y, Z: bottomLeft.Z + right[1]*k}
			b := d.w.GetBlock(cell)
			if !portalIsEmpty(b) {
				return i
			}
			if isBlockNamed(b, "nether_portal") {
				p.portalBlocks++
			}
		}
	}
	return 21
}

// createPortalBlocks fills the shape with nether_portal[axis] — the
// flag-18 write (client-visible, no neighbour updates) is closest to
// setSilentBlock.
func (s *Server) createPortalBlocks(d *dimension, p *portalShape) {
	axis := "x"
	if !p.axisX {
		axis = "z"
	}
	blk, ok := world.BlockState("nether_portal")
	if !ok {
		return
	}
	blk, ok = world.WithProperty(blk, "axis", axis)
	if !ok {
		return
	}
	right := [2]int{-1, 0}
	if !p.axisX {
		right = [2]int{0, 1}
	}
	for k := 0; k < p.width; k++ {
		for i := 0; i < p.height; i++ {
			cell := world.Pos{
				X: p.bottomLeft.X + right[0]*k,
				Y: p.bottomLeft.Y + i,
				Z: p.bottomLeft.Z + right[1]*k,
			}
			s.setSilentBlock(d, cell, blk)
		}
	}
}
