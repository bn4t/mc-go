package server

import (
	"mc-go/internal/data"
	"mc-go/internal/world"
)

// The stonecutter keeps a filtered recipe list — every stonecutting
// recipe whose input accepts the current input item, in datapack
// recipe order — and a selected index into it. A container button
// click sets the index; the result slot mirrors the selected recipe's
// output. slotsChanged rebuilds the list whenever the input's item
// type changes and drops the selection, matching StonecutterMenu's
// setupResultSlot.
//
// Measured on 26.2: the input slot has no mayPlace filter (dirt swaps
// in fine), selecting plays no wire sound (the client's local
// select_recipe), each take shrinks the input by one and plays
// ui.stonecutter.take_result at the block, and the menu's container
// is transient — closing returns the input to the player.

// stonecutterEnsure rebuilds c's recipe list when the input's item id
// differs from what the list was built for, resetting the selection.
func stonecutterEnsure(c *containerData, in ItemStack) {
	stonecutterEnsureProfile(c, in, nil)
}

func stonecutterEnsureProfile(c *containerData, in ItemStack, p *javaProfile) {
	number := int32(776)
	if p != nil {
		number = p.version.Protocol
	}
	if in.Empty() {
		c.stoneInput = -1
		c.stoneSel = -1
		c.stoneRecipes = nil
		return
	}
	if c.stoneInput == in.ID && c.stoneProtocol == number && c.stoneRecipes != nil {
		return
	}
	c.stoneInput = in.ID
	c.stoneProtocol = number
	c.stoneSel = -1
	c.stoneRecipes = nil
	name, ok := itemNameOf(in.ID)
	if !ok {
		return
	}
	for i := range data.Recipes {
		r := &data.Recipes[i]
		if r.Type != data.Stonecutting || len(r.Ingredients) != 1 {
			continue
		}
		if p != nil {
			ids, err := p.recipeDisplayIDs(i)
			if err != nil || len(ids) == 0 {
				continue
			}
		}
		if r.Ingredients[0].Accepts(name) {
			c.stoneRecipes = append(c.stoneRecipes, i)
		}
	}
}

// stonecutterOutput is the selected recipe's result stack, empty when
// nothing is selected.
func stonecutterOutput(c *containerData) ItemStack {
	if c.stoneSel < 0 || int(c.stoneSel) >= len(c.stoneRecipes) {
		return ItemStack{}
	}
	r := &data.Recipes[c.stoneRecipes[c.stoneSel]]
	id, ok := world.ItemID(r.Result.Item)
	if !ok {
		return ItemStack{}
	}
	return ItemStack{ID: id, Count: r.Result.Count}
}

// stonecutterSync refreshes the menu's result slot under
// slotsChanged — the same place vanilla recomputes it — so the update
// rides the click's broadcast batch and window state.
func stonecutterSync(sess *Session, c *containerData, w *openWindow, touched *[]int) {
	in := sess.menuStack(stonecutterInput)
	w.dim.containers.mu.Lock()
	p, _ := sess.javaProfile()
	beforeSelection := c.stoneSel
	stonecutterEnsureProfile(c, in, p)
	out := stonecutterOutput(c)
	selection := c.stoneSel
	w.dim.containers.mu.Unlock()
	if beforeSelection != selection {
		sess.server.sendContainerProperty(sess, w.id, 0, selection)
	}
	if sess.menuStack(stonecutterResult) != out {
		sess.setMenuStack(stonecutterResult, out)
		*touched = append(*touched, stonecutterResult)
	}
}

// stonecutterSelect is StonecutterMenu.clickMenuButton: a valid index
// selects the recipe and the result slot updates; an out-of-range
// index is dropped without resync.
func (s *Server) stonecutterSelect(sess *Session, w *openWindow, button int32) {
	c := w.container
	in := sess.menuStack(stonecutterInput)
	w.dim.containers.mu.Lock()
	p, _ := sess.javaProfile()
	stonecutterEnsureProfile(c, in, p)
	valid := button >= 0 && int(button) < len(c.stoneRecipes)
	changedSelection := valid && c.stoneSel != button
	if valid {
		c.stoneSel = button
	}
	out := stonecutterOutput(c)
	w.dim.containers.mu.Unlock()
	if !valid {
		return
	}
	if changedSelection {
		s.sendContainerProperty(sess, w.id, 0, button)
	}
	if sess.menuStack(stonecutterResult) != out {
		sess.setMenuStack(stonecutterResult, out)
		s.syncContainerMenuSlots(sess, w, []int{stonecutterResult})
	}
}

// stonecutterTake is StonecutterResultSlot.onTake: the input shrinks
// by one and the take_result sound plays at the block — every take
// path (hand, quick-move) lands here. The slot update after this
// recomputes the result against what's left.
func (s *Server) stonecutterTake(sess *Session, w *openWindow) {
	in := sess.menuStack(stonecutterInput)
	in.Count--
	if in.Count <= 0 {
		in = ItemStack{}
	}
	sess.setMenuStack(stonecutterInput, in)
	// The result slot sound lambda suppresses repeated takes in one tick.
	if now := s.gameTime(); now != w.lastTakeSoundTick {
		w.lastTakeSoundTick = now
		if id, ok := SoundEventID("ui.stonecutter.take_result"); ok {
			s.playSoundIn(w.dim.name, w.pos, id)
		}
	}
}

// closeStonecutter returns the transient input to the player and
// clears the result and selection — StonecutterMenu.removed's
// clearContainer. The return lands forward from the hotbar as Set
// Player Inventory pairs, same as the anvil's inputs.
func (s *Server) closeStonecutter(sess *Session, w *openWindow, c *containerData) {
	w.dim.containers.mu.Lock()
	st := c.slots[stonecutterInput]
	c.slots[stonecutterInput] = ItemStack{}
	c.slots[stonecutterResult] = ItemStack{}
	c.stoneSel = -1
	c.stoneInput = -1
	c.stoneRecipes = nil
	w.dim.containers.mu.Unlock()
	if st.Empty() {
		return
	}
	rest, _ := sess.addStack(st)
	if !rest.Empty() {
		w.dim.containers.mu.Lock()
		c.slots[stonecutterInput] = rest
		w.dim.containers.mu.Unlock()
		return
	}
	s.sendAnvilReturnSlot(sess, st)
}
