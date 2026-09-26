package server

import (
	"mc-go/internal/data"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Crafting-table menu: 0 result, 1-9 grid, 10-36 player main, 37-45
// hotbar.
const (
	menuCrafting      = 12 // minecraft:crafting in the menu registry
	craftTableGrid    = 9
	craftTableSlots   = 1 + craftTableGrid + 36
	craftTableResult  = 0
	craftTableGridOff = 1
)

// craftTable is one open crafting table's grid and result.
type craftTable struct {
	// slots[0] is the result; slots[1..9] are the 3x3 grid.
	slots [10]ItemStack
}

// isCraftingTableBlock reports whether a block state is a crafting
// table.
func isCraftingTableBlock(b world.Block) bool {
	name, ok := world.StateOwner(b)
	return ok && name == "crafting_table"
}

// openCraftingTable shows the 3x3 crafting menu.
func (s *Server) openCraftingTable(sess *Session, pos world.Pos) {
	id := sess.server.nextWindow.Add(1)
	table := &craftTable{}
	sess.windowMu.Lock()
	sess.window = &openWindow{id: id, pos: pos, craft: table, kind: menuCrafting}
	sess.windowState = 0
	sess.windowMu.Unlock()

	var b protocol.Buffer
	b.VarInt(id)
	b.VarInt(menuCrafting)
	b.Raw(menuTitle("container.crafting"))
	_ = sess.write(sess.ids().PlayOpenScreen, b.Bytes())
	s.refreshTableResult(sess, table)
	s.sendCraftTableContent(sess, table)
}

// sendCraftTableContent syncs the full crafting menu.
func (s *Server) sendCraftTableContent(sess *Session, table *craftTable) {
	sess.windowMu.Lock()
	state := sess.windowState
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil {
		return
	}
	var b protocol.Buffer
	b.VarInt(w.id)
	b.VarInt(state)
	b.VarInt(craftTableSlots)
	for i := 0; i < craftTableSlots; i++ {
		writeSlot(&b, sess.craftMenuStack(table, i))
	}
	writeSlot(&b, sess.carried)
	_ = sess.write(sess.ids().PlayContainerContent, b.Bytes())
}

// craftMenuStack reads one crafting-menu slot.
func (sess *Session) craftMenuStack(table *craftTable, slot int) ItemStack {
	switch {
	case slot == craftTableResult:
		return table.slots[craftTableResult]
	case slot < craftTableGridOff+craftTableGrid:
		return table.slots[slot]
	case slot < craftTableGridOff+craftTableGrid+27:
		return sess.stackAt(int16(slotMainStart + (slot - (craftTableGridOff + craftTableGrid))))
	default:
		return sess.stackAt(int16(slotHotbarStart + (slot - (craftTableGridOff + craftTableGrid + 27))))
	}
}

// setCraftMenuStack writes one crafting-menu slot.
func (sess *Session) setCraftMenuStack(table *craftTable, slot int, it ItemStack) {
	switch {
	case slot == craftTableResult:
		table.slots[craftTableResult] = it
	case slot < craftTableGridOff+craftTableGrid:
		table.slots[slot] = it
	case slot < craftTableGridOff+craftTableGrid+27:
		sess.setStack(int16(slotMainStart+(slot-(craftTableGridOff+craftTableGrid))), it)
	default:
		sess.setStack(int16(slotHotbarStart+(slot-(craftTableGridOff+craftTableGrid+27))), it)
	}
}

// closeCraftTable retires a crafting menu: the table keeps no storage,
// so the grid returns to the player inventory after the cursor does,
// and the result ghost clears with it.
func (s *Server) closeCraftTable(sess *Session, table *craftTable) {
	s.returnCarried(sess)
	for i := 1; i <= craftTableGrid; i++ {
		if st := sess.craftMenuStack(table, i); !st.Empty() {
			sess.setCraftMenuStack(table, i, ItemStack{})
			rest, _ := sess.addStack(st)
			if !rest.Empty() {
				// Nowhere fits everything: keep the grid's order by
				// parking the remainder back where it came from.
				sess.setCraftMenuStack(table, i, rest)
			}
		}
	}
	s.refreshTableResult(sess, table)
}

// refreshTableResult recomputes the table's result slot, returning
// whether it changed.
func (s *Server) refreshTableResult(sess *Session, table *craftTable) bool {
	grid := craftGrid(table.slots[1:10])
	want, _, ok := craftResult(grid)
	if !ok {
		want = ItemStack{}
	}
	prev := table.slots[craftTableResult]
	table.slots[craftTableResult] = want
	return prev != want
}

// handleCraftTableClick implements the core modes for the crafting
// menu.
func (s *Server) handleCraftTableClick(sess *Session, w *openWindow, table *craftTable, slot int16, button byte, mode int32) {
	if slot == craftTableResult {
		s.takeTableResult(sess, w, table, mode)
		return
	}
	if slot < 0 || int(slot) >= craftTableSlots {
		return
	}
	if int(slot) == craftTableGridOff+9-1 && false {
		return
	}
	var touched []int
	switch mode {
	case 0:
		cur := sess.carried
		target := sess.craftMenuStack(table, int(slot))
		if button == 0 {
			if cur.Empty() {
				cur, target = target, ItemStack{}
			} else if target.Empty() {
				target, cur = cur, ItemStack{}
			} else if target.Stackable(cur) {
				room := maxStackOfID(target.ID) - target.Count
				move := cur.Count
				if move > room {
					move = room
				}
				target.Count += move
				cur.Count -= move
				if cur.Count <= 0 {
					cur = ItemStack{}
				}
			} else {
				target, cur = cur, target
			}
		} else if cur.Empty() {
			if !target.Empty() {
				half := (target.Count + 1) / 2
				cur = target.withCount(half)
				target.Count -= half
				if target.Count <= 0 {
					target = ItemStack{}
				}
			}
		} else if target.Empty() {
			target = cur.withCount(1)
			cur.Count--
			if cur.Count <= 0 {
				cur = ItemStack{}
			}
		}
		sess.setCraftMenuStack(table, int(slot), target)
		sess.carried = cur
		touched = append(touched, int(slot))
	case 1:
		moving := sess.craftMenuStack(table, int(slot))
		if moving.Empty() {
			break
		}
		inGrid := int(slot) >= craftTableGridOff && int(slot) < craftTableGridOff+craftTableGrid
		touched = append(touched, int(slot))
		for i := 0; i < craftTableSlots && !moving.Empty(); i++ {
			destGrid := i >= craftTableGridOff && i < craftTableGridOff+craftTableGrid
			if destGrid == inGrid || i == craftTableResult {
				continue
			}
			other := sess.craftMenuStack(table, i)
			if !other.Stackable(moving) {
				continue
			}
			room := maxStackOfID(other.ID) - other.Count
			move := moving.Count
			if move > room {
				move = room
			}
			other.Count += move
			moving.Count -= move
			sess.setCraftMenuStack(table, i, other)
			touched = append(touched, i)
		}
		for i := 0; i < craftTableSlots && !moving.Empty(); i++ {
			destGrid := i >= craftTableGridOff && i < craftTableGridOff+craftTableGrid
			if destGrid == inGrid || i == craftTableResult {
				continue
			}
			if !sess.craftMenuStack(table, i).Empty() {
				continue
			}
			sess.setCraftMenuStack(table, i, moving)
			touched = append(touched, i)
			moving = ItemStack{}
		}
		sess.setCraftMenuStack(table, int(slot), moving)
	case 3:
		// Creative middle-click clone: a full stack onto the cursor,
		// the slot unchanged. A clone carries the slot's update so the
		// client stays live; anything else resyncs like an
		// unsupported mode instead of diverging.
		if out, ok := creativeClone(sess, sess.craftMenuStack(table, int(slot)), button); ok {
			sess.carried = out
			touched = append(touched, int(slot))
			break
		}
		s.sendCraftTableContent(sess, table)
		return
	default:
		s.sendCraftTableContent(sess, table)
		return
	}
	if s.refreshTableResult(sess, table) {
		touched = append(touched, craftTableResult)
	}
	s.syncCraftTableSlots(sess, w, table, touched)
}

// takeTableResult crafts from the table's result slot.
func (s *Server) takeTableResult(sess *Session, w *openWindow, table *craftTable, mode int32) {
	if mode != 0 && mode != 1 {
		return
	}
	crafts := int32(1)
	if mode == 1 {
		crafts = maxStack
	}
	var touched []int
	for c := int32(0); c < crafts; c++ {
		result := table.slots[craftTableResult]
		if result.Empty() {
			break
		}
		if mode == 0 {
			if !sess.carried.Empty() && sess.carried.ID != result.ID {
				return
			}
			if sess.carried.Empty() {
				sess.carried = result
			} else {
				sess.carried.Count += result.Count
			}
		} else {
			remainder, picked := sess.addStack(result)
			if !picked {
				break
			}
			sess.carriedMuSet(remainder)
		}
		// recipe_crafted sees the grid as it was when this craft
		// resolved — vanilla passes craftSlots.getItems() in onTake,
		// before the ingredients are consumed.
		grid := craftGrid(table.slots[1:10])
		_, rid, ridOK := craftResult(grid)
		if ridOK {
			s.fireRecipeCrafted(sess, rid, grid)
		}
		rtype := ""
		if ridOK {
			if r, ok := data.RecipeByID(rid); ok {
				rtype = r.Type
			}
		}
		for i := 1; i <= craftTableGrid; i++ {
			if table.slots[i].Empty() {
				continue
			}
			table.slots[i].Count--
			if table.slots[i].Count <= 0 {
				table.slots[i] = ItemStack{}
			}
			touched = append(touched, i)
		}
		// ResultSlot.onTake's remainder tail: each input's crafting
		// remainder lands in its grid slot (post-shrink), merges when
		// the same item is still there, else Inventory.add then drop.
		for i := 1; i <= craftTableGrid; i++ {
			rem, ok := craftRemainderOf(grid[i-1].ID)
			if !ok && rtype == data.CraftingSpecialBookcloning &&
				grid[i-1].BookWritten != "" {
				// BookCloningRecipe.getRemainingItems: a stack with
				// written_book_content leaves a count-1 copy of
				// itself — the source book is not consumed.
				rem = grid[i-1].withCount(1)
				ok = true
			}
			if !ok {
				continue
			}
			switch {
			case table.slots[i].Empty():
				table.slots[i] = rem
				touched = append(touched, i)
			case table.slots[i].Stackable(rem):
				table.slots[i].Count += rem.Count
				touched = append(touched, i)
			default:
				s.placeCraftLeftovers(sess, []ItemStack{rem})
			}
		}
		table.slots[craftTableResult] = ItemStack{}
		s.refreshTableResult(sess, table)
		touched = append(touched, craftTableResult)
		if mode == 0 {
			break
		}
	}
	s.syncCraftTableSlots(sess, w, table, touched)
	_ = s.sendCarriedItem(sess, sess.carriedBuffer())
}

// carriedMuSet replaces the cursor stack (session lock-free accessor
// used only from the session read loop).
func (sess *Session) carriedMuSet(it ItemStack) {
	sess.invMu.Lock()
	sess.carried = it
	sess.invMu.Unlock()
}

func (sess *Session) carriedBuffer() ItemStack {
	sess.invMu.RLock()
	defer sess.invMu.RUnlock()
	return sess.carried
}

// syncCraftTableSlots sends Container Slot updates for the touched
// menu slots.
func (s *Server) syncCraftTableSlots(sess *Session, w *openWindow, table *craftTable, touched []int) {
	sess.windowMu.Lock()
	sess.windowState++
	state := sess.windowState
	sess.windowMu.Unlock()
	seen := map[int]bool{}
	for _, slot := range touched {
		if seen[slot] {
			continue
		}
		seen[slot] = true
		var b protocol.Buffer
		b.VarInt(w.id)
		b.VarInt(state)
		b.Short(int16(slot))
		writeSlot(&b, sess.craftMenuStack(table, slot))
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
	}
	_ = s.sendCarriedItem(sess, sess.carriedBuffer())
}

// handleCraftTableClickPacket parses and applies a click on the
// crafting menu.
func (s *Server) handleCraftTableClickPacket(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	win, err := r.VarInt()
	if err != nil {
		return
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.craft == nil || w.id != win {
		return
	}
	st, err := r.VarInt() // state
	if err != nil {
		return
	}
	if !s.checkClickState(sess, w, st) {
		return
	}
	slot, err := r.Short()
	if err != nil {
		return
	}
	button, err := r.Byte()
	if err != nil {
		return
	}
	mode, err := r.VarInt()
	if err != nil {
		return
	}
	readChangedSlots(r)
	if _, err := readSlot(r); err != nil {
		return
	}
	s.handleCraftTableClick(sess, w, w.craft, slot, button, mode)
}
