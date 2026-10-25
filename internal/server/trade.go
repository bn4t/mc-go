package server

// Villager trading: the merchant menu. Measured on vanilla 26.2:
// Villager.mobInteract -> startTrading -> ServerPlayer sends
// open_screen (menu type 19, title = the villager's display name,
// entity.minecraft.villager.<profession>) followed by
// merchant_offers (container id, offer list, villager level/xp,
// showProgress, canRestock) and a container_content sync of the
// menu's three slots plus the player inventory.
//
// MerchantMenu mechanics modeled: select_trade (0x33 serverbound)
// sets the selected offer and tryMoveItems pulls the cost items out
// of the player inventory into the two payment slots; the result
// slot shows the sell stack while the inputs cover it; taking the
// result consumes the costs, increases the offer's use count, spawns
// the xp orbs vanilla's rewardTradeXp throws, and fires
// villager_trade.

import (
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// merchantOffer is one recipe from a merchant's Offers.Recipes NBT.
// buyB is the optional second cost (empty when absent).
type merchantOffer struct {
	buy, buyB, sell  ItemStack
	uses, maxUses    int32
	xp               int32
	rewardExp        bool
	specialPriceDiff int32
	priceMul         float32
	demand           int32
}

// merchantState is the per-window merchant menu state.
type merchantState struct {
	entID int32
	sel   int32 // selected offer index, -1 until select_trade
	slots [3]ItemStack
}

const (
	menuMerchant    = 19
	merchantBuyA    = 0
	merchantBuyB    = 1
	merchantResult  = 2
	merchantOffNone = -1
)

// parseMerchantOffers reads the Recipes list of a summon Offers tag.
// Each entry: buy{buyB?} ItemCost compounds (id + count) and sell,
// with uses/maxUses/xp/rewardExp.
func parseMerchantOffers(items []*snbtTag) []merchantOffer {
	var out []merchantOffer
	for _, rc := range items {
		var o merchantOffer
		o.maxUses = 12
		o.rewardExp = true
		o.xp = 1
		for key, v := range rc.vals {
			switch key {
			case "buy":
				o.buy = snbtItemCost(v)
			case "buyB":
				o.buyB = snbtItemCost(v)
			case "sell":
				o.sell = snbtItemCost(v)
			case "uses":
				o.uses = int32(snbtNum(v))
			case "maxUses":
				o.maxUses = int32(snbtNum(v))
			case "xp":
				o.xp = int32(snbtNum(v))
			case "rewardExp":
				o.rewardExp = v.num == "1b" || v.num == "1" || v.str == "true"
			case "specialPrice":
				o.specialPriceDiff = int32(snbtNum(v))
			case "priceMultiplier":
				o.priceMul = float32(snbtNum(v))
			case "demand":
				o.demand = int32(snbtNum(v))
			}
		}
		if o.buy.Empty() || o.sell.Empty() {
			continue
		}
		out = append(out, o)
	}
	return out
}

// snbtItemCost reads an ItemCost/ItemStack compound {id, count}.
func snbtItemCost(v *snbtTag) ItemStack {
	var it ItemStack
	it.Count = 1
	if iv, ok := v.vals["id"]; ok && iv.str != "" {
		if id, ok := world.ItemID(strings.TrimPrefix(iv.str, "minecraft:")); ok {
			it.ID = id
		}
	}
	if cv, ok := v.vals["count"]; ok {
		if n := snbtNum(cv); n > 0 {
			it.Count = int32(n)
		}
	}
	return it
}

// tradeVillager is Villager.mobInteract's trade branch: a right-click
// on a living, adult trader with a non-empty offer list opens the
// merchant screen. Babies and offer-less villagers (nitwit, no
// profession) don't trade; vanilla plays an unhappy sound there.
func (s *Server) tradeVillager(sess *Session, eid int32, hand int32) {
	if hand != 0 {
		return
	}
	e, ok := s.entities.GetPtr(eid)
	if !ok || e.Dying || e.Baby || len(e.Offers) == 0 {
		return
	}
	name, ok := EntityTypeName(e.Type)
	if !ok || (name != "villager" && name != "wandering_trader") {
		return
	}
	// An already-open merchant menu's payment slots go back before the
	// new window replaces it.
	sess.windowMu.Lock()
	prev := sess.window
	sess.windowMu.Unlock()
	if prev != nil && prev.merchant != nil {
		s.merchantClose(sess, prev)
	}
	m := &merchantState{entID: eid, sel: merchantOffNone}
	id := sess.server.nextWindow.Add(1)
	w := &openWindow{id: id, dim: sess.dimension(), merchant: m, kind: menuMerchant}
	sess.windowMu.Lock()
	sess.window = w
	sess.windowState = 0
	sess.windowMu.Unlock()

	var b protocol.Buffer
	b.VarInt(id)
	b.VarInt(menuMerchant)
	prof := e.Profession
	if prof == "" || prof == "none" || prof == "nitwit" {
		b.Raw(menuTitle("entity.minecraft.villager"))
	} else {
		b.Raw(menuTitle("entity.minecraft.villager." + prof))
	}
	_ = sess.write(sess.ids().PlayOpenScreen, b.Bytes())
	s.sendMerchantOffers(sess, w, e)
	s.sendMerchantContent(sess, w)
}

// sendMerchantOffers emits the merchant_offers packet for an open
// merchant window: offers with the entity's current use counts.
func (s *Server) sendMerchantOffers(sess *Session, w *openWindow, e *Entity) {
	var b protocol.Buffer
	b.VarInt(w.id)
	b.VarInt(int32(len(e.Offers)))
	for _, o := range e.Offers {
		// ItemCost: item id varint, count varint, empty
		// DataComponentExactPredicate.
		b.VarInt(o.buy.ID)
		b.VarInt(o.buy.Count)
		b.VarInt(0)
		writeSlot(&b, o.sell)
		if o.buyB.Empty() {
			b.Bool(false)
		} else {
			b.Bool(true)
			b.VarInt(o.buyB.ID)
			b.VarInt(o.buyB.Count)
			b.VarInt(0)
		}
		b.Bool(o.uses >= o.maxUses) // outOfStock
		b.Int(o.uses)
		b.Int(o.maxUses)
		b.Int(o.xp)
		b.Int(o.specialPriceDiff)
		b.Float(o.priceMul)
		b.Int(o.demand)
	}
	b.VarInt(1) // villagerLevel: summoned traders are novices
	b.VarInt(0) // villagerXp
	b.Bool(true)
	b.Bool(true)
	_ = sess.write(sess.ids().PlayMerchantOffers, b.Bytes())
}

// sendMerchantContent syncs the merchant window: 3 menu slots + the
// 36 player slots + carried, like sendContainerMenuContent.
func (s *Server) sendMerchantContent(sess *Session, w *openWindow) {
	sess.windowMu.Lock()
	state := sess.windowState
	sess.windowMu.Unlock()
	var b protocol.Buffer
	b.VarInt(w.id)
	b.VarInt(state)
	b.VarInt(int32(w.menuSize()))
	for i := 0; i < w.menuSize(); i++ {
		writeSlot(&b, sess.menuStack(i))
	}
	writeSlot(&b, sess.carried)
	_ = sess.write(sess.ids().PlayContainerContent, b.Bytes())
}

// handleSelectTrade is MerchantMenu.setSelectionHint + tryMoveItems:
// the client picked an offer — remember it, pull the cost items out
// of the inventory into the payment slots, and show the result when
// the inputs cover the offer.
func (s *Server) handleSelectTrade(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	sel, err := r.VarInt()
	if err != nil {
		return
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.merchant == nil {
		return
	}
	m := w.merchant
	e, ok := s.entities.GetPtr(m.entID)
	if !ok || sel < 0 || int(sel) >= len(e.Offers) {
		return
	}
	m.sel = sel
	o := e.Offers[sel]
	// MerchantMenu.tryMoveItems: move the buy cost into slot 0 and
	// buyB into slot 1 out of the player inventory.
	s.merchantFill(sess, m, merchantBuyA, o.buy)
	if !o.buyB.Empty() {
		s.merchantFill(sess, m, merchantBuyB, o.buyB)
	}
	s.merchantRefreshResult(m, e)
	touched := []int{merchantBuyA, merchantBuyB, merchantResult}
	s.syncMerchantSlots(sess, w, touched)
}

// merchantFill moves items matching `cost` from the player inventory
// into a payment slot until the slot covers it (vanilla's
// moveFromInventoryToPaymentSlot pulls whole stacks as needed).
func (s *Server) merchantFill(sess *Session, m *merchantState, slot int, cost ItemStack) {
	cur := m.slots[slot]
	need := cost.Count - cur.Count
	if cur.Empty() {
		need = cost.Count
	}
	if need <= 0 || cur.ID != 0 && cur.ID != cost.ID {
		return
	}
	sess.invMu.Lock()
	defer sess.invMu.Unlock()
	for sl := int16(slotMainStart); sl <= slotOffhand && need > 0; sl++ {
		it := sess.inv[sl]
		if it.Empty() || it.ID != cost.ID {
			continue
		}
		take := it.Count
		if take > need {
			take = need
		}
		it.Count -= take
		sess.inv[sl] = it
		if cur.Empty() {
			cur = ItemStack{ID: cost.ID, Count: 0}
		}
		cur.Count += take
		need -= take
	}
	m.slots[slot] = cur
}

// merchantRefreshResult shows the sell stack in the result slot when
// the payment slots cover the selected offer, empty otherwise.
func (s *Server) merchantRefreshResult(m *merchantState, e *Entity) {
	if m.sel == merchantOffNone || int(m.sel) >= len(e.Offers) {
		m.slots[merchantResult] = ItemStack{}
		return
	}
	o := e.Offers[m.sel]
	a, bb := m.slots[merchantBuyA], m.slots[merchantBuyB]
	if a.ID == o.buy.ID && a.Count >= o.buy.Count &&
		(o.buyB.Empty() || bb.ID == o.buyB.ID && bb.Count >= o.buyB.Count) {
		m.slots[merchantResult] = o.sell
		return
	}
	m.slots[merchantResult] = ItemStack{}
}

// syncMerchantSlots sends Container Slot updates for touched merchant
// menu slots plus the player inventory rows.
func (s *Server) syncMerchantSlots(sess *Session, w *openWindow, touched []int) {
	sess.windowMu.Lock()
	sess.windowState++
	state := sess.windowState
	sess.windowMu.Unlock()
	for _, slot := range touched {
		var b protocol.Buffer
		b.VarInt(w.id)
		b.VarInt(state)
		b.Short(int16(slot))
		writeSlot(&b, sess.menuStack(slot))
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
	}
	// The inventory rows changed too (merchantFill removed items).
	s.sendMerchantContent(sess, w)
	_ = s.sendCarriedItem(sess, sess.carriedBuffer())
}

// handleMerchantClick applies a click inside the merchant window.
// Modeled: pickup on the result slot executes the trade; clicks on
// the payment slots move stacks between cursor and slot like vanilla
// (payment slots accept stacks; the result slot is take-only).
func (s *Server) handleMerchantClick(sess *Session, w *openWindow, slot int16, button byte, mode int32) {
	m := w.merchant
	if mode != 0 || slot < 0 {
		return
	}
	e, ok := s.entities.GetPtr(m.entID)
	if !ok {
		return
	}
	switch int(slot) {
	case merchantResult:
		if button != 0 {
			return
		}
		s.merchantTake(sess, w, m, e)
	case merchantBuyA, merchantBuyB:
		// Simple swap: cursor in / stack out.
		cur := sess.carriedBuffer()
		m.slots[int(slot)] = cur
		sess.carriedMuSet(m.slots[int(slot)])
		s.syncMerchantSlots(sess, w, []int{int(slot)})
	default:
		// Player inventory rows: nothing menu-specific — let the
		// generic place/swap handle it? The merchant menu's own
		// slots are what matter for the trade; inventory clicks are
		// left for the container path.
	}
}

// merchantTake runs MerchantResultSlot.onTake: consume the payment
// slots' share of the offer, hand the result over, bump uses, reward
// xp at the trader, and fire the villager_trade trigger + stat.
func (s *Server) merchantTake(sess *Session, w *openWindow, m *merchantState, e *Entity) {
	if m.sel == merchantOffNone || int(m.sel) >= len(e.Offers) {
		return
	}
	res := m.slots[merchantResult]
	if res.Empty() {
		return
	}
	o := &e.Offers[m.sel]
	m.slots[merchantBuyA].Count -= o.buy.Count
	if m.slots[merchantBuyA].Count <= 0 {
		m.slots[merchantBuyA] = ItemStack{}
	}
	if !o.buyB.Empty() {
		m.slots[merchantBuyB].Count -= o.buyB.Count
		if m.slots[merchantBuyB].Count <= 0 {
			m.slots[merchantBuyB] = ItemStack{}
		}
	}
	o.uses++
	remainder, _ := sess.addStack(res)
	if !remainder.Empty() {
		s.dropItemLocked(sess, remainder)
	}
	sess.addCustomStat("traded_with_villager", 1)
	if o.rewardExp && o.xp > 0 {
		s.spawnXPOrb(e.Dim, e.X, e.Y+1, e.Z, o.xp)
	}
	s.fireVillagerTrade(sess, e, res)
	s.merchantRefreshResult(m, e)
	s.syncMerchantSlots(sess, w, []int{merchantBuyA, merchantBuyB, merchantResult})
}

// merchantClose returns the payment slots' contents to the player
// (vanilla MerchantMenu.removed places them back in the inventory).
func (s *Server) merchantClose(sess *Session, w *openWindow) {
	m := w.merchant
	for i := merchantBuyA; i <= merchantBuyB; i++ {
		it := m.slots[i]
		if it.Empty() {
			continue
		}
		m.slots[i] = ItemStack{}
		remainder, _ := sess.addStack(it)
		if !remainder.Empty() {
			s.dropItemLocked(sess, remainder)
		}
	}
	s.returnCarried(sess)
}
