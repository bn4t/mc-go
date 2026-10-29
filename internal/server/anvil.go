package server

import (
	"math/rand"
	"strings"
	"unicode"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Anvil menu: repair-combine, material repair, and renaming, measured
// click by click against the real 26.2 server
// (tests/blackbox/probe_anvil*_test.go, since removed and pinned by
// TestAnvilParity and TestAnvilDegradeParity).
//
// The probed rules:
//
//   - The menu is registry id 8 with 3 container slots (39 with the
//     player rows), title container.repair, and Container Property 0
//     carrying the current level cost (0 with no output).
//   - Both input slots take anything by hand (dirt sits in slot 0
//     without complaint); only the output computation cares what they
//     hold.
//   - The pending name is per open menu (the text field a real client
//     owns), starts empty, persists across takes, and dies with the
//     window. An empty field on a named input is a clear, not a no-op:
//     the output loses its custom_name and the operation costs 1.
//     Renaming to the name the input already carries computes nothing.
//   - The output's name is the pending field's when it differs from
//     the input's, so a rename alone produces output. A filtered name
//     past 50 UTF-16 code units is ignored outright: the packet is dropped, the
//     connection lives.
//   - Durability operations produce output from a combine of
//     two damageables of the same item with the left one damaged (a
//     fresh left + damaged right computes nothing); a material repair
//     when the right item is the left's repairable material and the
//     left is damaged. Accepted enchantments or a rename also produce
//     output; invalid sacrifice pairs compute nothing and cost 0.
//   - A combine's output damage is max minus (remaining target +
//     remaining sacrifice + 12% of max), floored at 0; the work costs
//     2 levels. A material repair consumes ceil(damage / floor(max/4))
//     units capped at the stack's count, each unit healing floor(max/4)
//     — a dead diamond pick (1561 of 1561) eats 5 diamonds — and costs
//     that many levels. Either way the output's repair_cost is
//     max(target, sacrifice)*2+1; a rename alone keeps the input's.
//   - The level cost is both inputs' repair_costs plus the work (2 for
//     a combine, the units for a material repair, 0 for a bare rename)
//     plus 1 when the name changes. Past 39 a survival output is not
//     even produced (the slot stays empty, the property still prices
//     it); creative sees and takes it at any cost. Rename-only costs
//     cap at 39 even when the input has a higher prior-work penalty.
//   - Taking the output plays level event 1030 at the anvil and
//     answers Container Slot (inputs emptied by their consume counts,
//     output cleared) plus the zeroed cost, the cursor stack on a hand
//     take, or the destination slots on a quick-move. A throw click on
//     the output pays and drops it as an item entity like any take.
//     Survival deducts the cost in levels and syncs Set Experience;
//     creative sends no XP packet. Taking with too few levels is a
//     silent full resync: nothing moves, the cost stays, no chat, no
//     sound.
//   - Number-key and clone clicks on the output are silent.
//   - Each take rolls a 12% chance to degrade the anvil block a stage
//     (anvil -> chipped_anvil -> damaged_anvil -> air, facing kept), a
//     Block Update and no extra sound; a broken anvil spills the
//     inputs and the menu dies with it.
//   - Clicking the empty output with something on the cursor only
//     echoes the cursor; with an empty cursor it is silent. Placing
//     into a non-empty output is silently ignored (no resync, unlike
//     the furnace).
//   - Closing returns the inputs to the player inventory forward from
//     the hotbar, each stack as Set Player Inventory plus a window-0
//     slot update, and leaves the cursor alone. The client-initiated
//     close itself is never echoed (no Container Close).
//
// Enchantment merging uses the measured crafting enchantments and datapack
// supported/exclusive sets; see anvil_enchant.go. The default hover-name
// rename refusal remains open.

// anvilMaxCost is the highest level cost Survival/Adventure will pay;
// past it no output is produced at all. Creative ignores the cap and
// both the slot and the take work at any price.
const anvilMaxCost = 39

// anvilMaxNameLength is the filtered field's UTF-16 code-unit cap; a longer
// name is ignored entirely, not disconnected.
const anvilMaxNameLength = 50

// anvilDegradeChance is each take's chance of wearing the anvil block
// down a stage.
const anvilDegradeChance = 0.12

// The output slot's take modes, keyed by the click's mode.
const (
	anvilTakeHand  = 0
	anvilTakeQuick = 1
	anvilTakeThrow = 4
)

// anvilCompute prices the anvil's inputs: it reports the output stack,
// the level cost the property carries, and how many items the take
// consumes from each input. A false-cost output (empty stack) still
// reports the cost — vanilla prices a refused operation instead of
// hiding it.
func anvilCompute(target, sacrifice ItemStack, pending string, creative bool) (out ItemStack, cost int32, useL, useR int32) {
	if target.Empty() {
		return ItemStack{}, 0, 0, 0
	}
	// StringUtil.isBlank includes both Java whitespace and space chars.
	// U+0085 is a Go whitespace rune but neither Java category.
	if strings.IndexFunc(pending, func(r rune) bool { return r == 0x85 || !unicode.IsSpace(r) }) < 0 {
		pending = ""
	}
	renamed := pending != target.Name
	maxDmg := MaxDamageOfID(target.ID)

	var work int32
	material := maxDmg > 0 && repairMaterialOf(target, sacrifice)
	book := sacrifice.ID == world.ItemIDs["enchanted_book"]
	if !sacrifice.Empty() && !material && !book && (sacrifice.ID != target.ID || maxDmg == 0) {
		return ItemStack{}, 0, 0, 0
	}
	switch {
	case !sacrifice.Empty() && sacrifice.ID == target.ID && maxDmg > 0 && target.Damage > 0:
		// Same-item combine: the sacrifice's remaining durability
		// plus a 12% bonus repairs the target.
		remT := maxDmg - target.Damage
		remS := maxDmg - sacrifice.Damage
		if remS < 0 {
			remS = 0
		}
		newRem := remT + remS + maxDmg*12/100
		if newRem > maxDmg {
			newRem = maxDmg
		}
		out = target.withCount(1)
		out.Damage = maxDmg - newRem
		if out.Damage < 0 {
			out.Damage = 0
		}
		out.RepairCost = anvilBumpedCost(target, sacrifice)
		work, useL, useR = 2, 1, 1
	case material && target.Damage > 0:
		// Material repair: each unit heals floor(max/4) of damage,
		// the floor dividing before the multiplication the way the
		// real per-unit loop does.
		heal := maxDmg / 4
		units := (target.Damage + heal - 1) / heal
		if units > sacrifice.Count {
			units = sacrifice.Count
		}
		out = target.withCount(1)
		out.Damage = target.Damage - units*heal
		if out.Damage < 0 {
			out.Damage = 0
		}
		out.RepairCost = anvilBumpedCost(target, sacrifice)
		work, useL, useR = units, 1, units
	default:
		if material {
			return ItemStack{}, 0, 0, 0
		}
		if !renamed && sacrifice.Empty() {
			return ItemStack{}, 0, 0, 0
		}
		out = target
		useL, useR = target.Count, 0
	}
	if !sacrifice.Empty() && !material {
		var enchCost int32
		var refused bool
		out, enchCost, refused = anvilMergeEnchantments(out, sacrifice, creative)
		if refused {
			return ItemStack{}, 0, 0, 0
		}
		work += enchCost
		useL, useR = target.Count, sacrifice.Count
	}
	if work == 0 && !renamed {
		return ItemStack{}, 0, 0, 0
	}
	if work > 0 {
		out.RepairCost = anvilBumpedCost(target, sacrifice)
	}
	if renamed {
		out.Name = pending
	}
	total := int64(target.RepairCost) + int64(sacrifice.RepairCost) + int64(work)
	if renamed {
		total++
	}
	if total > 2147483647 {
		total = 2147483647
	}
	if total < 0 {
		total = 0
	}
	cost = int32(total)
	// A rename alone remains affordable regardless of prior work penalty.
	if work == 0 && renamed && cost > anvilMaxCost {
		cost = anvilMaxCost
	}
	if !creative && cost > anvilMaxCost {
		return ItemStack{}, cost, useL, useR
	}
	return out, cost, useL, useR
}

// anvilValidatedName applies the measured 26.2 AnvilMenu.validateName
// rules: discard forbidden chat characters before checking the 50 UTF-16
// code-unit limit. Supplementary characters occupy two units.
func anvilValidatedName(name string) (string, bool) {
	var filtered strings.Builder
	units := 0
	for _, r := range name {
		if r < 32 || r == 127 || r == '§' {
			continue
		}
		filtered.WriteRune(r)
		units++
		if r > 0xffff {
			units++
		}
	}
	return filtered.String(), units <= anvilMaxNameLength
}

// anvilBumpedCost is a worked output's repair_cost: the higher prior
// work penalty doubled plus one.
func anvilBumpedCost(target, sacrifice ItemStack) int32 {
	rc := target.RepairCost
	if sacrifice.RepairCost > rc {
		rc = sacrifice.RepairCost
	}
	if rc > 1073741823 {
		return 2147483647
	}
	return rc*2 + 1
}

// recomputeAnvil re-derives the anvil's output slot and level cost
// from its inputs and the window's pending name, pricing for the
// viewing player's mode. It reports whether either changed.
func recomputeAnvil(sess *Session, w *openWindow, c *containerData) (outChanged, costChanged bool) {
	sess.windowMu.Lock()
	pending := w.pendingName
	sess.windowMu.Unlock()
	out, cost, useL, useR := anvilCompute(c.slots[anvilLeft], c.slots[anvilRight], pending, sess.mode() == GameModeCreative)
	if c.slots[anvilResult] != out {
		c.slots[anvilResult] = out
		outChanged = true
	}
	if c.anvilCost != cost {
		c.anvilCost = cost
		costChanged = true
	}
	c.anvilUse = [2]int32{useL, useR}
	return outChanged, costChanged
}

// sendAnvilCost syncs the anvil's level-cost property.
func (s *Server) sendAnvilCost(sess *Session, w *openWindow, c *containerData) {
	w.dim.containers.mu.Lock()
	cost := c.anvilCost
	w.dim.containers.mu.Unlock()
	s.sendContainerProperty(sess, w.id, 0, cost)
}

// sendAnvilLevelEvent plays the anvil-use sound at the block.
func (s *Server) sendAnvilLevelEvent(sess *Session, w *openWindow) {
	var b protocol.Buffer
	b.Int(1030) // level event: anvil use
	b.Position(int32(w.pos.X), int32(w.pos.Y), int32(w.pos.Z))
	b.Int(0)
	b.Bool(false)
	_ = sess.write(sess.ids().PlayLevelEvent, b.Bytes())
}

// sendPlayerInventorySlot syncs one player-inventory slot by its
// container index (0-8 hotbar, 9-35 main): the Set Player Inventory
// packet vanilla pairs with the window-0 update when it moves a stack
// outside a click, measured on anvil close.
func (s *Server) sendPlayerInventorySlot(sess *Session, index int32, it ItemStack) {
	var b protocol.Buffer
	b.VarInt(index)
	sess.writeSlot(&b, it)
	_ = sess.write(sess.ids().PlaySetPlayerInventory, b.Bytes())
}

// playerContainerIndex maps a window-0 slot to the player-inventory
// container index Set Player Inventory uses, or -1 when the slot has
// none (crafting, armor, offhand). The hotbar/main halves are the
// /item registry the slot probes pinned: hotbar.N is container.N,
// inventory.N is container.9+N.
func playerContainerIndex(w0 int16) int32 {
	switch {
	case w0 >= slotHotbarStart && w0 < slotHotbarStart+9:
		return int32(w0 - slotHotbarStart)
	case w0 >= slotMainStart && w0 < slotHotbarStart:
		return int32(w0)
	}
	return -1
}

// anvilSendMenuSlot sends one window slot update under its own state
// id: vanilla bumps the state for every slot it reports, so a pair of
// updates reads st, st+1.
func (s *Server) anvilSendMenuSlot(sess *Session, w *openWindow, slot int) {
	sess.windowMu.Lock()
	sess.windowState++
	state := sess.windowState
	sess.windowMu.Unlock()
	var b protocol.Buffer
	b.VarInt(w.id)
	b.VarInt(state)
	b.Short(int16(slot))
	sess.writeSlot(&b, sess.menuStack(slot))
	_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
}

// handleAnvilClick implements click modes 0 (pickup/place/take), 1
// (quick move), 2 (hotbar swap), 3 (creative clone) and 4 (throw) for
// the anvil menu. Input slots behave like storage and recompute the
// output; the result slot takes with the measured payment rules.
func (s *Server) handleAnvilClick(sess *Session, w *openWindow, c *containerData, slot int16, button byte, mode int32) {
	if slot < 0 || int(slot) >= w.menuSize() {
		return
	}
	sess.invMu.RLock()
	cur := sess.carried
	sess.invMu.RUnlock()

	switch mode {
	case 0:
		s.anvilClick(sess, w, c, int(slot), button, cur)
	case 1:
		s.anvilQuickMove(sess, w, c, int(slot))
	case 2:
		// Hotbar swaps ignore the accept rules, like manual
		// placement; the result slot never swaps.
		swap := w.menuSize() - 9 + int(button)
		if int(slot) == anvilResult || swap < w.menuSize()-9 || swap >= w.menuSize() {
			break
		}
		a := sess.menuStack(int(slot))
		b := sess.menuStack(swap)
		sess.setMenuStack(int(slot), b)
		sess.setMenuStack(swap, a)
		s.anvilSyncInput(sess, w, c, []int{int(slot), swap})
	case 3:
		if int(slot) == anvilResult {
			break // the result slot answers a clone with nothing
		}
		if out, ok := creativeClone(sess, sess.menuStack(int(slot)), button); ok {
			s.setCarriedLocked(sess, out)
			s.anvilSendMenuSlot(sess, w, int(slot))
			_ = s.sendCarriedItem(sess, out)
			break
		}
		s.sendContainerMenuContent(sess)
	case 4:
		if int(slot) == anvilResult {
			// A throw on the output is a take whose prize drops in
			// front of the player instead of landing on the cursor.
			s.takeAnvilResult(sess, w, c, cur, anvilTakeThrow)
			break
		}
		it := sess.menuStack(int(slot))
		if it.Empty() {
			break
		}
		drop := it
		if button == 0 {
			drop = it.withCount(1)
		}
		it.Count -= drop.Count
		if it.Count <= 0 {
			it = ItemStack{}
		}
		sess.setMenuStack(int(slot), it)
		s.dropFromMenu(sess, drop)
		s.anvilSyncInput(sess, w, c, []int{int(slot)})
	default:
		s.sendContainerMenuContent(sess)
	}
}

// anvilClick applies a mode-0 click: storage semantics on every slot
// but the result, the take rules on the result.
func (s *Server) anvilClick(sess *Session, w *openWindow, c *containerData, slot int, button byte, cur ItemStack) {
	if slot == anvilResult {
		s.takeAnvilResult(sess, w, c, cur, anvilTakeHand)
		return
	}
	target := sess.menuStack(slot)
	if button != 0 {
		cur, target = clickRight(cur, target, c.spec, slot)
	} else {
		cur, target = clickLeft(cur, target, c.spec, slot)
	}
	sess.setMenuStack(slot, target)
	s.setCarriedLocked(sess, cur)
	s.anvilSyncInput(sess, w, c, []int{slot})
}

// anvilSyncInput resyncs after an input-side change: the touched slots
// plus a recomputed output, the cost when it moved, and the cursor
// when it is non-empty (vanilla omits the cursor packet when it
// empties).
func (s *Server) anvilSyncInput(sess *Session, w *openWindow, c *containerData, touched []int) {
	w.dim.containers.mu.Lock()
	outChanged, costChanged := recomputeAnvil(sess, w, w.container)
	w.dim.containers.mu.Unlock()
	if outChanged {
		touched = append(touched, anvilResult)
	}
	seen := map[int]bool{}
	for _, sl := range touched {
		if seen[sl] {
			continue
		}
		seen[sl] = true
		s.anvilSendMenuSlot(sess, w, sl)
	}
	sess.invMu.RLock()
	cur := sess.carried
	sess.invMu.RUnlock()
	if !cur.Empty() {
		_ = s.sendCarriedItem(sess, cur)
	}
	if costChanged {
		s.sendAnvilCost(sess, w, w.container)
	}
}

// anvilSyncResult answers a rename packet: the recomputed output when
// it moved, the cost when it moved, nothing else — the real server
// does not echo the cursor for a name change.
func (s *Server) anvilSyncResult(sess *Session, w *openWindow, c *containerData) {
	w.dim.containers.mu.Lock()
	outChanged, costChanged := recomputeAnvil(sess, w, c)
	w.dim.containers.mu.Unlock()
	if outChanged {
		s.anvilSendMenuSlot(sess, w, anvilResult)
	}
	if costChanged {
		s.sendAnvilCost(sess, w, c)
	}
}

// takeAnvilResult runs a take of the output slot: a hand take puts it
// on the cursor (how=anvilTakeHand), a quick move rides the player
// rows backward (anvilTakeQuick), and a throw click pays and drops it
// as an item entity in front of the player (anvilTakeThrow).
func (s *Server) takeAnvilResult(sess *Session, w *openWindow, c *containerData, cur ItemStack, how int32) {
	w.dim.containers.mu.Lock()
	out := c.slots[anvilResult]
	cost := c.anvilCost
	use := c.anvilUse
	w.dim.containers.mu.Unlock()
	if out.Empty() {
		// An empty result takes nothing: the cursor is echoed when
		// it holds something, and the click is silent otherwise.
		if !cur.Empty() && how == anvilTakeHand {
			_ = s.sendCarriedItem(sess, cur)
		}
		return
	}
	if how == anvilTakeHand && !cur.Empty() {
		// Manual placement into the output is silently ignored.
		return
	}
	free := sess.mode() == GameModeCreative
	if !free && sess.xpLevel < cost {
		// Too poor: nothing moves, the whole window resyncs carrying
		// the live state, with no chat, no sound, and the cost left
		// standing.
		s.sendContainerMenuContent(sess)
		s.sendAnvilCost(sess, w, c)
		return
	}
	s.sendAnvilLevelEvent(sess, w)
	w.dim.containers.mu.Lock()
	for i, sl := range []int{anvilLeft, anvilRight} {
		st := c.slots[sl]
		st.Count -= use[i]
		if st.Count <= 0 {
			st = ItemStack{}
		}
		c.slots[sl] = st
	}
	c.slots[anvilResult] = ItemStack{}
	c.anvilCost = 0
	c.anvilUse = [2]int32{}
	w.dim.containers.mu.Unlock()

	touched := []int{anvilLeft, anvilRight, anvilResult}
	toCursor := ItemStack{}
	switch how {
	case anvilTakeThrow:
		s.dropFromMenu(sess, out)
	case anvilTakeQuick:
		moving := out
		var filled []int
		quickMoveIntoPlayer(sess, c, &moving, &filled)
		if !moving.Empty() {
			// Nowhere fits: park the remainder back in the output.
			sess.setMenuStack(anvilResult, moving)
		}
		touched = append(touched, filled...)
	default:
		s.setCarriedLocked(sess, out)
		toCursor = out
	}
	for _, sl := range touched {
		s.anvilSendMenuSlot(sess, w, sl)
	}
	if !toCursor.Empty() {
		_ = s.sendCarriedItem(sess, toCursor)
	}
	s.sendAnvilCost(sess, w, c)
	if !free {
		sess.xpLevel -= cost
		_ = sess.server.sendSetExperience(sess, sess.xpProgress, sess.xpLevel, sess.xpTotal)
	}
	s.anvilDegradeRoll(sess, w)
}

// anvilQuickMove applies a mode-1 click: the output takes with
// payment, anything else rides the generic container/player paths.
func (s *Server) anvilQuickMove(sess *Session, w *openWindow, c *containerData, slot int) {
	if slot == anvilResult {
		sess.invMu.RLock()
		cur := sess.carried
		sess.invMu.RUnlock()
		s.takeAnvilResult(sess, w, c, cur, anvilTakeQuick)
		return
	}
	moving := sess.menuStack(slot)
	if moving.Empty() {
		return
	}
	touched := []int{slot}
	if slot < c.spec.slots {
		quickMoveIntoPlayer(sess, c, &moving, &touched)
	} else {
		quickMoveIntoContainer(sess, c, &moving, &touched, slot)
	}
	sess.setMenuStack(slot, moving)
	s.anvilSyncInput(sess, w, c, touched)
}

// anvilDegradeRoll wears the anvil down a stage 12% of the takes:
// anvil -> chipped_anvil -> damaged_anvil -> air, keeping the facing.
// A broken anvil spills whatever the menu held and the open windows on
// it close.
func (s *Server) anvilDegradeRoll(sess *Session, w *openWindow) {
	if rand.Float64() >= anvilDegradeChance {
		return
	}
	d := w.dim
	cur := d.w.GetBlock(w.pos)
	name, ok := world.StateOwner(cur)
	if !ok {
		return
	}
	next, ok := map[string]string{
		"anvil":         "chipped_anvil",
		"chipped_anvil": "damaged_anvil",
		"damaged_anvil": "",
	}[name]
	if !ok {
		return
	}
	if next == "" {
		s.setBlockAndBroadcast(sess.getName(), w.pos, world.Air)
		s.removeContainer(d, w.pos)
		return
	}
	facing, _ := world.StateProperty(cur, "facing")
	blk, ok := world.BlockStateOf(next, map[string]string{"facing": facing})
	if !ok {
		return
	}
	s.setBlockAndBroadcast(sess.getName(), w.pos, blk)
}

// closeAnvil retires an anvil menu: the anvil keeps no storage, so the
// inputs return to the player inventory forward from the hotbar, each
// as Set Player Inventory plus a window-0 update, and the cursor is
// left alone. The output slot is virtual (recompute derives it from
// the inputs), so it clears without returning: sending it back would
// duplicate the inputs. A remainder that fits nowhere parks back in
// its slot.
func (s *Server) closeAnvil(sess *Session, w *openWindow, c *containerData) {
	for _, sl := range []int{anvilLeft, anvilRight} {
		w.dim.containers.mu.Lock()
		st := c.slots[sl]
		w.dim.containers.mu.Unlock()
		if st.Empty() {
			continue
		}
		w.dim.containers.mu.Lock()
		c.slots[sl] = ItemStack{}
		c.anvilCost = 0
		w.dim.containers.mu.Unlock()
		rest, _ := sess.addStack(st)
		if !rest.Empty() {
			w.dim.containers.mu.Lock()
			c.slots[sl] = rest
			w.dim.containers.mu.Unlock()
			continue
		}
		// addStack syncs the window-0 slot itself; pair it with
		// the Set Player Inventory the real server sends. The
		// destination is the last stackable-or-empty slot addStack
		// touched: re-derive it by scan order is overkill, so find
		// the moved stack by value.
		s.sendAnvilReturnSlot(sess, st)
	}
	// The output never existed as an item, so it clears without
	// returning; the next open recomputes it from empty inputs.
	w.dim.containers.mu.Lock()
	c.slots[anvilResult] = ItemStack{}
	c.anvilCost = 0
	c.anvilUse = [2]int32{}
	w.dim.containers.mu.Unlock()
}

// sendAnvilReturnSlot pairs a close-returned stack with its Set Player
// Inventory update, locating the window-0 slot addStack filled. Stacks
// merge, so the search matches by value and prefers the hotbar, the
// order addStack fills.
func (s *Server) sendAnvilReturnSlot(sess *Session, moved ItemStack) {
	sess.invMu.RLock()
	defer sess.invMu.RUnlock()
	best := int16(-1)
	for i := int16(slotHotbarStart); i <= slotOffhand; i++ {
		if sess.inv[i] == moved {
			best = i
			break
		}
	}
	if best < 0 {
		for i := int16(slotMainStart); i < slotHotbarStart; i++ {
			if sess.inv[i] == moved {
				best = i
				break
			}
		}
	}
	if best < 0 {
		return
	}
	if idx := playerContainerIndex(best); idx >= 0 {
		s.sendPlayerInventorySlot(sess, idx, moved)
	}
}

// openWindowKindAnvil reports whether an open window is an anvil menu.
func openWindowKindAnvil(w *openWindow) bool {
	return w != nil && w.container != nil && w.container.spec.menu == menuAnvil
}
