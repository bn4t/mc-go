package server

import (
	"math"
	"strconv"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Creative block placement (Use Item On) plus the held-item tracking it
// resolves against. Item ids come from the official item registry
// (world.ItemIDs); every block-item places its default block state.

// Item ids named for tests and builtin tooling; values are the exact
// protocol ids from the official report.
var (
	ItemStone = world.ItemIDs["stone"]
	ItemDirt  = world.ItemIDs["dirt"]
	ItemGrass = world.ItemIDs["grass_block"]
)

// itemBlock maps a held item to the block it places. Most block items
// resolve through ItemBlockState; items whose block carries a different
// name (seeds → crops, redstone → wire, ...) go through the alias table.
func itemBlock(id int32) (world.Block, bool) {
	if b, ok := world.ItemBlockState(id); ok {
		return b, true
	}
	if name, ok := itemNameOf(id); ok {
		if alias, ok := itemBlockAlias[name]; ok {
			return world.BlockState(alias)
		}
	}
	return 0, false
}

// itemBlockAlias is the vanilla item-name → block-name map for items
// that place a differently-named block (BlockItem States only cover
// the same-name set).
var itemBlockAlias = map[string]string{
	"wheat_seeds":       "wheat",
	"beetroot_seeds":    "beetroots",
	"melon_seeds":       "melon_stem",
	"pumpkin_seeds":     "pumpkin_stem",
	"torchflower_seeds": "torchflower_crop",
	"pitcher_pod":       "pitcher_crop",
	"nether_wart":       "nether_wart",
	"carrot":            "carrots",
	"potato":            "potatoes",
	"cocoa_beans":       "cocoa",
	"sweet_berries":     "sweet_berry_bush",
	"glow_berries":      "cave_vines",
	"redstone":          "redstone_wire",
	"string":            "tripwire",
}

// faceOffset resolves a clicked face to the adjacent cell, following
// the public protocol docs: 0=-Y 1=+Y 2=-Z 3=+Z 4=-X 5=+X.
func faceOffset(face int32) (dx, dy, dz int32, ok bool) {
	switch face {
	case 0:
		return 0, -1, 0, true
	case 1:
		return 0, 1, 0, true
	case 2:
		return 0, 0, -1, true
	case 3:
		return 0, 0, 1, true
	case 4:
		return -1, 0, 0, true
	case 5:
		return 1, 0, 0, true
	}
	return 0, 0, 0, false
}

// useMenuReach is how far the eyes may be from the clicked block's
// nearest point for a use to open its menu. Measured on vanilla 26.2
// with a chest shelf (opened through 5.93, silent from 6.86) and a
// vertical column (opened through 5.5): a 6-block limit fits every
// cell.
const useMenuReach = 6.0

// opensMenu reports whether using the block opens a menu instead of
// placing. It mirrors openBlockMenu's dispatch without side effects,
// so the use gates (reach, teleport confirmation) apply to exactly
// the blocks that open.
func opensMenu(blk world.Block) bool {
	return isChestBlock(blk) || isEnderChestBlock(blk) || isHopperBlock(blk) || isCraftingTableBlock(blk) || isFurnaceBlock(blk) || isContainerBlock(blk)
}

// useMenuReachOK reports whether the session's eyes are within reach
// of the clicked block's nearest point.
func useMenuReachOK(sess *Session, pos world.Pos) bool {
	px, py, pz, _, _, _ := sess.Pos()
	ex, ey, ez := px, py+1.62, pz
	nx := min(max(ex, float64(pos.X)), float64(pos.X)+1)
	ny := min(max(ey, float64(pos.Y)), float64(pos.Y)+1)
	nz := min(max(ez, float64(pos.Z)), float64(pos.Z)+1)
	dx, dy, dz := ex-nx, ey-ny, ez-nz
	return dx*dx+dy*dy+dz*dz <= useMenuReach*useMenuReach
}

// teleportPending reports whether the server sent the session a
// teleport it never confirmed. Vanilla 26.2 answers an in-reach use
// that arrives first with the build.tooHigh action bar instead of
// opening the menu; with no teleport issued yet there is nothing to
// confirm.
func teleportPending(sess *Session) bool {
	last := sess.teleportNext.Load() - 1
	return last > 0 && sess.teleportAck.Load() != last
}

// sendBuildTooHigh answers a use that arrived before the client
// confirmed its teleports: the action-bar "Height limit for building
// is 319 blocks". Measured byte for byte on vanilla 26.2; the limit
// is one below the dimension's top (minY+height-1).
func (s *Server) sendBuildTooHigh(sess *Session, d *dimension) {
	s.sendActionBar(sess, component{
		translate: "build.tooHigh",
		color:     "red",
		with:      []any{int32(d.minY + d.height - 1)},
	})
}

// handleHeld tracks the selected hotbar slot (0-8); anything else is
// ignored without touching state.
func (s *Server) handleHeld(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	slot, err := r.Short()
	if err != nil || slot < 0 || slot > 8 {
		return
	}
	sess.held.Store(int32(slot))
}

// handleCreativeSlot accepts only fully decoded, representable creative stacks.
// Measured775 ServerGamePacketListenerImpl permission/slot/count gates: creative
// materials, menu slots1..45, and count no larger than the item's max stack.
// Negative creative-drop slots remain outside this inventory-mutation path.
func (s *Server) handleCreativeSlot(sess *Session, p protocol.Packet) {
	if sess.mode() != GameModeCreative {
		return
	}
	r := protocol.NewPacketReader(p.Data)
	slot, err := r.Short()
	if err != nil || slot < 1 || slot >= slotCount {
		return
	}
	profile, err := sess.javaProfile()
	if err != nil {
		return
	}
	stack, err := profile.readUntrustedSlot(r)
	if err != nil || r.Remaining() != 0 {
		return
	}
	if stack.Empty() {
		stack = ItemStack{}
	} else if stack.Count > maxStackOfID(stack.ID) {
		return
	}
	sess.setStack(slot, stack)
}

// placeReachM mirrors the dig-side reach: eye to target-center.
const placeReachM = 6.5

// handleUseItem places the held block against the clicked face in
// creative mode: hand, BlockPos, face, 3 cursor floats, inside-block,
// then a version-varying tail whose LAST varint is the prediction
// sequence (a Bool border-hit flag, when present, harmlessly decodes
// as a one-byte varint first). Denials still ack the sequence so the
// client retires its prediction instead of desyncing.
func (s *Server) handleUseItem(sess *Session, p protocol.Packet) {
	// A use in the first moments of a session, before the client has
	// reported its terrain loaded, is dropped whole: no placement, no
	// editor, no acknowledgement.
	if !sess.interactionsOpen(s.TickCount()) {
		return
	}
	r := protocol.NewPacketReader(p.Data)
	hand, err := r.VarInt()
	if err != nil {
		return
	}
	x, y, z, err := r.Position()
	if err != nil {
		return
	}
	face, err := r.VarInt()
	if err != nil {
		return
	}
	var cursor [3]float64
	for i := 0; i < 3; i++ {
		f, err := r.Float()
		if err != nil { // cursor
			return
		}
		cursor[i] = float64(f)
	}
	if _, err := r.Bool(); err != nil { // inside block
		return
	}
	seq := int32(-1)
	for r.Remaining() > 0 {
		v, err := r.VarInt()
		if err != nil {
			break
		}
		seq = v
	}
	acked := false
	var dispatched bool
	var sendRevert func()
	var d *dimension
	var clicked, facePos world.Pos
	defer func() {
		if dispatched && sendRevert != nil {
			sendRevert()
		}
		if seq >= 0 && !acked {
			s.sendBlockAck(sess, seq)
		}
	}()

	const border = 29999984
	if x < -border || x > border || z < -border || z > border {
		return
	}
	d = sess.dimension()
	if int(y) < d.minY || int(y) > d.minY+d.height-1 {
		return
	}
	dx, dy, dz, ok := faceOffset(face)
	if !ok {
		return
	}
	clicked = world.Pos{X: int(x), Y: int(y), Z: int(z)}
	cur := d.w.GetBlock(clicked)
	if cur == world.Air {
		return // no block targeted: ack only
	}
	facePos = world.Pos{X: int(x + dx), Y: int(y + dy), Z: int(z + dz)}
	// handleUseItemOn's tail re-sends the live state at the clicked pos
	// and the clicked-face neighbor to the acting player — vanilla's
	// client-prediction revert, fired on every use_item_on that reaches
	// dispatch (even a no-op click on plain stone). Reading the world
	// at emit time lands the just-placed block on the face pos.
	revertSent := false
	sendRevert = func() {
		if revertSent {
			return
		}
		revertSent = true
		_ = sess.sendProjectedBlockUpdate(clicked, d.w.GetBlock(clicked))
		_ = sess.sendProjectedBlockUpdate(facePos, d.w.GetBlock(facePos))
	}
	useAck := func() {
		if acked {
			return
		}
		sendRevert()
		acked = true
		if seq >= 0 {
			s.sendBlockAck(sess, seq)
		}
	}
	plainAck := func() {
		if seq >= 0 && !acked {
			acked = true
			s.sendBlockAck(sess, seq)
		}
	}
	// Vanilla gates every use_item_on on isWithinBlockInteractionRange
	// (pos, 1.0): eye to the clicked block's AABB under
	// block_interaction_range (4.5) + 1 = 5.5.
	if !withinReach(sess, clicked, reachSurvival+1.0) {
		return
	}
	dispatched = true
	// item_used_on_block fires on the interaction itself — vanilla
	// evaluates it inside useOn before the use's outcome is known.
	if bname, ok := world.StateOwner(cur); ok {
		if held := sess.heldStack(); !held.Empty() {
			if iname, ok := itemNameOf(held.ID); ok {
				s.fireItemUsedOnBlock(sess, bname, iname)
			}
		}
	}
	// LeadItem.useOn: the clicked block is a fences-tag block and a
	// lead is held → bindPlayerMobs re-leashes the player's nearby
	// leashees onto a knot at the fence. The fence has no
	// useWithoutItem, so the item's useOn runs even while sneaking —
	// and the lead never "places".
	if name, _ := world.StateOwner(cur); blockTagHas("fences", name) {
		if held := sess.heldStack(); !held.Empty() {
			if iname, ok := itemNameOf(held.ID); ok && iname == "lead" {
				s.bindPlayerMobs(sess, d, clicked)
				useAck()
				return
			}
		}
	}
	// A sneaking player with an item in hand skips the clicked block's
	// whole use dispatch (vanilla isSecondaryUseActive): no TNT prime,
	// no bed, no sign, no menu — the held item's useOn runs instead.
	blockUse := !(sess.sneaking && !sess.heldStack().Empty())
	if name, _ := world.StateOwner(cur); blockUse && name == "tnt" && s.useTnt(sess, d, clicked) {
		useAck()
		return
	}
	// A bed click sleeps — it never places the held item, even
	// holding blocks. slept_in_bed fires when the gate passes.
	if blockUse && isBedBlock(cur) {
		s.useBed(sess, clicked)
		useAck()
		return
	}
	// A noteblock's useWithoutItem cycles its note and plays it —
	// NOTE_BLOCK_PLAY — when the held item doesn't consume the use.
	// mc-go tunes only with an empty hand; held non-placeable items
	// are a GOAL gap.
	if blockUse && isNoteBlock(cur) && sess.heldStack().Empty() {
		s.noteBlockUse(sess, d, clicked)
		useAck()
		return
	}
	// Interacting with a sign dyes it, waxes it, makes its text glow,
	// or opens its editor - it never places.
	if blockUse && isSignLikeBlock(cur) {
		s.useSign(sess, d, clicked, cur)
		useAck()
		return
	}
	// Face controls answer useWithoutItem first: a button presses and
	// schedules its release, a lever flips powered. Both consume the
	// click regardless of the held stack (vanilla runs the block's
	// use before the item's when the item has no useOn override).
	if cname, _ := world.StateOwner(cur); blockUse && cname != "" {
		short := strings.TrimPrefix(cname, "minecraft:")
		switch {
		case strings.HasSuffix(short, "_button"):
			if p, _ := world.StateProperty(cur, "powered"); p != "true" {
				s.rsButtonPress(d, clicked, short, cur)
			}
			useAck()
			return
		case short == "lever":
			s.rsLeverPull(d, clicked, cur)
			useAck()
			return
		case short == "repeater":
			// RepeaterBlock.useWithoutItem: mayBuild gates the DELAY
			// cycle (spectator/adventure pass through to the item).
			if sess.mode() != GameModeSpectator && sess.mode() != GameModeAdventure {
				delay := rsPropInt(rsPropsOf(cur)("delay"))
				if nb, ok := world.WithProperty(cur, "delay", strconv.Itoa(delay%4+1)); ok {
					s.rsWriteBlock(d, clicked, nb, nb, nil)
				}
				useAck()
				return
			}
		case short == "comparator":
			// ComparatorBlock.useWithoutItem: mayBuild gates the MODE
			// cycle; the comparator_click pitch keys off the NEW mode
			// (0.55 subtract, 0.5 compare), the write is flag-2, and a
			// still-standing comparator refreshes its output at once.
			if sess.mode() != GameModeSpectator && sess.mode() != GameModeAdventure {
				next, pitch := "compare", float32(0.5)
				if rsPropsOf(cur)("mode") == "compare" {
					next, pitch = "subtract", 0.55
				}
				s.explodeBlockSound(d, clicked, "comparator.click", 0.3, pitch)
				if nb, ok := world.WithProperty(cur, "mode", next); ok {
					s.rsWriteBlockFlag2(d, clicked, nb)
					if rsIsComparator(d.w.GetBlock(clicked)) {
						s.rsCompRefresh(d, clicked, nb)
					}
				}
				useAck()
				return
			}
		case s.rsToggleOpenable(d, clicked, short):
			useAck()
			return
		case short == "bee_nest" || short == "beehive":
			if s.useBeehive(sess, d, clicked, cur) {
				useAck()
				return
			}
		}
	}
	// LecternBlock.useItemOn runs before the menu path: a book stores
	// onto a bookless lectern, and every other interact on a bookless
	// one is vanilla's CONSUME — nothing opens and the held item does
	// not place. A lectern holding a book falls through to the menu.
	if blockUse && s.lecternUse(d, sess, clicked, cur, hand) {
		useAck()
		return
	}
	// JukeboxBlock.useItemOn/useWithoutItem: a loaded jukebox ejects
	// on any click; an empty one takes a music disc. Every other
	// click is a double-PASS — the held item places.
	if name, _ := world.StateOwner(cur); blockUse && name == "jukebox" && s.useJukebox(sess, d, clicked) {
		useAck()
		return
	}
	// Container blocks open their menu instead of placing, but only
	// in reach and only once every server teleport is confirmed:
	// out-of-reach uses die silently, unconfirmed ones get the
	// build.tooHigh action bar. Measured on vanilla 26.2; the
	// denials still ack the sequence.
	if blockUse && opensMenu(cur) {
		if !useMenuReachOK(sess, clicked) {
			plainAck()
			return
		}
		if teleportPending(sess) {
			plainAck()
			s.sendBuildTooHigh(sess, d)
			return
		}
	}
	if blockUse && s.openBlockMenu(sess, cur, clicked) {
		useAck()
		return
	}
	// BoneMealItem.useOn — a bonemealable target grows, consuming one
	// meal in survival (creative keeps it, like the igniter path).
	if s.useBonemeal(sess, d, clicked) {
		useAck()
		return
	}
	// EnderEyeItem.useOn — an eyeless portal frame takes the eye.
	// EndCrystalItem.useOn — obsidian or bedrock with room above.
	if s.useEndCrystal(sess, d, clicked) {
		useAck()
		return
	}
	if s.useEnderEye(sess, d, clicked) {
		useAck()
		return
	}
	// FlintAndSteelItem/FireChargeItem.useOn — held-item useOn runs
	// only after the clicked block's own use path passed.
	if s.useIgniter(sess, d, clicked, face) {
		useAck()
		return
	}
	// FireworkRocketItem.useOn — a gliding user gets vanilla's PASS;
	// otherwise a free rocket spawns at the click + face·0.15 point
	// and the stack shrinks one in every gamemode.
	if s.useFireworkPlace(sess, hand, x, y, z, dx, dy, dz, cursor) {
		useAck()
		return
	}
	target := facePos
	if target.Y < d.minY || target.Y > d.minY+d.height-1 {
		return
	}
	if cur := d.w.GetBlock(target); cur != world.Air {
		return // occupied: creative never overwrites solid
	}
	// Reach, measured like the anticheat break check: eye to
	// target-center, so the detector and the server agree.
	px, py, pz, _, _, _ := sess.Pos()
	ddx := px - (float64(target.X) + 0.5)
	ddy := (py + 1.62) - (float64(target.Y) + 0.5)
	ddz := pz - (float64(target.Z) + 0.5)
	if math.Sqrt(ddx*ddx+ddy*ddy+ddz*ddz) > placeReachM {
		return
	}
	item, ok := sess.heldItem()
	if !ok {
		return // empty hand: ack only
	}
	blk, ok := itemBlock(item)
	if !ok {
		return // not a placeable block-item: ack only
	}
	// getStateForPlacement folds the connection props (fence/pane/bars)
	// of a placed block before the write; chests fold facing + type.
	blk = s.connectPlaceState(d, target, blk)
	blk = s.chestPlaceState(d, target, blk, sess, face)
	blk = s.stairsPlaceState(d, target, blk, sess, face, cursor[1])
	blk = s.snowyPlaceState(d, target, blk)
	blk = s.tripwirePlaceState(d, target, blk)
	blk = s.scaffoldPlaceState(d, target, blk)
	blk = s.hookPlaceState(d, target, blk, sess, face)
	blk = s.multifacePlaceState(d, target, blk, sess, face)
	blk = s.diodePlaceState(d, target, blk, sess)
	blk = pistonPlaceState(blk, sess)
	blk = observerPlaceState(blk, sess)
	blk = s.crafterPlaceState(d, target, blk, sess)
	blk = leafPlaceState(d, target, blk)
	blk = blockStateComponentApply(blk, sess.heldStack())
	s.setBlockAndBroadcast(sess.getName(), target, blk)
	// BlockItem.place → Level.gameEvent(BLOCK_PLACE) at the placed
	// position with the new state as the context: wool muffles it.
	if n, ok := world.StateOwner(blk); ok {
		s.emitGameEvent(d, "block_place",
			float64(target.X)+0.5, float64(target.Y)+0.5, float64(target.Z)+0.5,
			sess, strings.TrimPrefix(n, "minecraft:"))
	}
	// TripWireHookBlock.setPlacedBy: a placed hook runs its
	// calculateState — attaches/powers the run it lands on. Command
	// writes never reach this path on vanilla.
	if n, ok := world.StateOwner(blk); ok && strings.TrimPrefix(n, "minecraft:") == "tripwire_hook" {
		if f, ok := world.StateProperty(blk, "facing"); ok {
			s.hookRecalc(d, target, f)
		}
	}
	// DiodeBlock.setPlacedBy: a hand-placed diode that already sees a
	// powered input schedules its first flip one tick out (command
	// writes never reach setPlacedBy — like the hook above).
	if n, ok := world.StateOwner(blk); ok {
		if short := strings.TrimPrefix(n, "minecraft:"); short == "repeater" || short == "comparator" {
			on := rsDiodeShouldOn(d, target, d.w.GetBlock(target))
			if short == "comparator" {
				on = rsCompShouldOn(d, target, d.w.GetBlock(target))
			}
			if on {
				s.ticks.schedulePrio(schedKey{dim: d.name, pos: target, kind: tickBlock}, s.TickCount()+1, tickPrioNormal)
			}
		}
		// CrafterBlock.setPlacedBy: a crafter placed with TRIGGERED
		// already set (the state it carries at placement) schedules its
		// first craft 4 ticks out.
		if strings.TrimPrefix(n, "minecraft:") == "crafter" {
			if t, ok := world.StateProperty(blk, "triggered"); ok && t == "true" {
				if c := s.containerAt(d, target); c != nil {
					d.containers.mu.Lock()
					c.craftTicks = 4
					c.triggered = true
					d.containers.mu.Unlock()
				}
			}
		}
	}
	sess.addItemStat(statUsed, item, 1)
	if bname, ok := world.StateOwner(blk); ok {
		s.firePlacedBlock(sess, bname)
		// WitherSkullBlock.setPlacedBy: placing the skull item that
		// completes the soul T spawns the wither. Command writes never
		// trigger it — vanilla's checkSpawn only runs here.
		if short := strings.TrimPrefix(bname, "minecraft:"); short == "wither_skeleton_skull" ||
			short == "wither_skeleton_wall_skull" {
			s.golemTryWither(d, target)
		}
	}
	// A bee nest placed from a stack carrying a bees component keeps the
	// occupants on the block (vanilla applies the item's implicit
	// components on placement).
	if isBeeNestBlock(blk) {
		held := sess.heldStack()
		if d.bees == nil {
			d.bees = map[world.Pos]int32{}
		}
		if held.Bees > 0 {
			d.bees[target] = held.Bees
		} else {
			delete(d.bees, target)
		}
	}
	// BlockItem.place → Nameable.setCustomName: a stack with
	// minecraft:custom_name names the block entity it creates.
	if held := sess.heldStack(); held.Name != "" {
		if d.beNames == nil {
			d.beNames = map[world.Pos]string{}
		}
		d.beNames[target] = held.Name
	}
	// A shulker box placed out of a stack that came with contents (it
	// was broken with items inside) restores them, so the box keeps its
	// contents across a move. container_loot re-arms the same way.
	if isShulkerBoxBlock(blk) {
		if held := sess.heldStack(); held.Contents != nil || held.LootTable != "" {
			if c := s.containerAt(sess.dimension(), target); c != nil {
				sess.dimension().containers.mu.Lock()
				if held.Contents != nil {
					copy(c.slots, held.Contents[:])
				}
				c.lootTable, c.lootSeed = held.LootTable, held.LootSeed
				sess.dimension().containers.mu.Unlock()
			}
		}
	}
	useAck()
	s.emitLightUpdate(d, target)
	// Survival consumes one item and syncs the slot; creative keeps
	// the stack, matching vanilla.
	if sess.mode() != GameModeCreative {
		sess.consumeHeld(1)
	}
}

// consumeHeld removes n from the held stack and syncs the slot.
func (sess *Session) consumeHeld(n int32) {
	slot := invSlotForHotbar(sess.held.Load())
	sess.invMu.Lock()
	it := sess.inv[slot]
	if it.Empty() {
		sess.invMu.Unlock()
		return
	}
	it.Count -= n
	if it.Count <= 0 {
		it = ItemStack{}
	}
	sess.inv[slot] = it
	sess.invMu.Unlock()
	_ = sess.server.sendContainerSlot(sess, slot, it)
}

// diodePlaceState is DiodeBlock.getStateForPlacement +
// RepeaterBlock's LOCKED snapshot: FACING is the horizontal look
// direction's opposite (the input side faces the player), and LOCKED
// carries the live isLocked read at place time.
func (s *Server) diodePlaceState(d *dimension, pos world.Pos, blk world.Block, sess *Session) world.Block {
	name, ok := world.StateOwner(blk)
	if !ok {
		return blk
	}
	short := strings.TrimPrefix(name, "minecraft:")
	if short != "repeater" && short != "comparator" {
		return blk
	}
	_, _, _, yaw, _, _ := sess.Pos()
	out := blk
	if nb, ok := world.WithProperty(out, "facing", dirOpposite(yawDir(yaw))); ok {
		out = nb
	}
	if short == "repeater" {
		if nb, ok := world.WithProperty(out, "locked", boolStr(rsDiodeLocked(d, pos, short, out))); ok {
			out = nb
		}
	}
	return out
}

// pistonPlaceState is PistonBaseBlock.getStateForPlacement: FACING is
// the player's nearest look direction flipped — look down and the
// piston points up. EXTENDED starts false.
func pistonPlaceState(blk world.Block, sess *Session) world.Block {
	name, ok := world.StateOwner(blk)
	if !ok {
		return blk
	}
	short := strings.TrimPrefix(name, "minecraft:")
	if short != "piston" && short != "sticky_piston" {
		return blk
	}
	_, _, _, yaw, pitch, _ := sess.Pos()
	dir := nearestLookDir(yaw, pitch)
	if nb, ok := world.WithProperty(blk, "facing", dirOpposite(dir)); ok {
		blk = nb
	}
	if nb, ok := world.WithProperty(blk, "extended", "false"); ok {
		blk = nb
	}
	return blk
}

// nearestLookDir is Direction.getNearest — the dominant axis of the
// view vector.
func nearestLookDir(yaw, pitch float32) string {
	yr := float64(yaw) * math.Pi / 180
	pr := float64(pitch) * math.Pi / 180
	lx := -math.Sin(yr) * math.Cos(pr)
	ly := -math.Sin(pr)
	lz := math.Cos(yr) * math.Cos(pr)
	ax, ay, az := math.Abs(lx), math.Abs(ly), math.Abs(lz)
	if ax >= ay && ax >= az {
		if lx > 0 {
			return "east"
		}
		return "west"
	}
	if ay >= az {
		if ly > 0 {
			return "up"
		}
		return "down"
	}
	if lz > 0 {
		return "south"
	}
	return "north"
}

// observerPlaceState is ObserverBlock.getStateForPlacement: facing is
// the look direction itself (nearestLooking.opposite.opposite) — the
// sensor points where the player looks; output exits the back.
func observerPlaceState(blk world.Block, sess *Session) world.Block {
	name, ok := world.StateOwner(blk)
	if !ok || strings.TrimPrefix(name, "minecraft:") != "observer" {
		return blk
	}
	_, _, _, yaw, pitch, _ := sess.Pos()
	if nb, ok := world.WithProperty(blk, "facing", nearestLookDir(yaw, pitch)); ok {
		blk = nb
	}
	if nb, ok := world.WithProperty(blk, "powered", "false"); ok {
		blk = nb
	}
	return blk
}

// useBonemeal — BoneMealItem.useOn: a bonemealable clicked block
// grows (bonemealGrow, same table the dispenser case drives), one
// meal is consumed in survival, and the 1505 growth event plays at
// the clicked cell. Returns whether the use was consumed.
func (s *Server) useBonemeal(sess *Session, d *dimension, clicked world.Pos) bool {
	held := sess.heldStack()
	if held.Empty() {
		return false
	}
	iname, ok := itemNameOf(held.ID)
	if !ok || strings.TrimPrefix(iname, "minecraft:") != "bone_meal" {
		return false
	}
	if !s.bonemealGrow(d, clicked) {
		return false
	}
	sess.addItemStat(statUsed, held.ID, 1)
	if sess.mode() != GameModeCreative {
		sess.consumeHeld(1)
	}
	s.broadcastLevelEvent(d.name, 1505, clicked, 15)
	return true
}

// blockStateComponentApply is BlockItem.updateBlockStateFromTag: the
// item's minecraft:block_state component folds its property map onto
// the state being placed, so a silk-touched nest drops back with its
// honey_level intact. Properties the block does not declare are
// ignored (BlockItemStateProperties.updateState is per-key).
func blockStateComponentApply(blk world.Block, held ItemStack) world.Block {
	if held.BlockState == "" {
		return blk
	}
	for _, kv := range strings.Split(held.BlockState, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		if nb, ok := world.WithProperty(blk, k, v); ok {
			blk = nb
		}
	}
	return blk
}
