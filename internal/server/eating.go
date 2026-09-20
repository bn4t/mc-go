package server

import (
	"time"

	"mc-go/internal/protocol"
)

// foodStats is a food item's hunger and saturation restoration.
type foodStats struct {
	food       int32
	saturation float32
	eatTicks   int32
	always     bool // edible at full hunger (golden apples)
}

// foodItems covers the common foods; eating takes 1.6s (32 ticks).
var foodItems = map[string]foodStats{
	"apple":           {food: 4, saturation: 2.4, eatTicks: 32},
	"bread":           {food: 5, saturation: 6, eatTicks: 32},
	"porkchop":        {food: 3, saturation: 1.8, eatTicks: 32},
	"cooked_porkchop": {food: 8, saturation: 12.8, eatTicks: 32},
	"beef":            {food: 3, saturation: 1.8, eatTicks: 32},
	"steak":           {food: 8, saturation: 12.8, eatTicks: 32},
	"chicken":         {food: 2, saturation: 1.2, eatTicks: 32},
	"cooked_chicken":  {food: 6, saturation: 7.2, eatTicks: 32},
	"mutton":          {food: 2, saturation: 1.2, eatTicks: 32},
	"cooked_mutton":   {food: 6, saturation: 9.6, eatTicks: 32},
	"baked_potato":    {food: 5, saturation: 6, eatTicks: 32},
	"golden_apple":    {food: 4, saturation: 9.6, eatTicks: 32, always: true},
	"carrot":          {food: 3, saturation: 3.6, eatTicks: 32},
	"cooked_cod":      {food: 5, saturation: 6, eatTicks: 32},
	"cod":             {food: 2, saturation: 0.4, eatTicks: 32},
	"cooked_salmon":   {food: 6, saturation: 9.6, eatTicks: 32},
	"salmon":          {food: 2, saturation: 0.4, eatTicks: 32},
	"rotten_flesh":    {food: 4, saturation: 0.8, eatTicks: 32},
}

// handleUseItem processes Use Item: food begins eating; everything else
// is acknowledged by coverage only for now. Like a use on a block it is
// dropped until the client reports its terrain loaded.
func (s *Server) handleUseItemPacket(sess *Session, p protocol.Packet) {
	if !sess.interactionsOpen(s.TickCount()) {
		return
	}
	r := protocol.NewPacketReader(p.Data)
	hand, err := r.VarInt()
	if err != nil {
		return
	}
	if _, err := r.VarInt(); err != nil { // sequence
		return
	}
	if _, err := r.Float(); err != nil { // yaw
		return
	}
	if _, err := r.Float(); err != nil { // pitch
		return
	}
	if sess.dead {
		return
	}
	// A shield raises instead of eating/drawing; the raised flag lives
	// until the release_use_item action. Vanilla reads the hand the
	// packet asked for — an offhand use ignores the main hand stack.
	st := sess.heldStack()
	if hand == 1 {
		st = sess.stackAt(slotOffhand)
	}
	if un, ok := itemNameOf(st.ID); ok && !st.Empty() && un == "shield" {
		sess.blocking.Store(true)
		return
	}
	held := sess.heldStack()
	if held.Empty() {
		return
	}
	name, ok := itemNameOf(held.ID)
	if !ok {
		return
	}
	if name == "bow" {
		// Vanilla bows draw on use even in creative (which skips the
		// arrow check until release); the shot lands on release.
		sess.bowStartTick = s.TickCount()
		return
	}
	if name == "crossbow" {
		s.useCrossbow(sess)
		return
	}
	stats, ok := foodItems[name]
	if !ok || (!stats.always && sess.food >= defaultFood) {
		return
	}
	sess.eatingTicks = stats.eatTicks
	sess.eatingItem = held.ID
	_ = stats
}

// eatTickStep advances eating; finishing restores hunger and consumes
// the item.
func (s *Server) eatTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.eatingTicks <= 0 {
			return true
		}
		held := sess.heldStack()
		if held.Empty() || held.ID != sess.eatingItem {
			sess.eatingTicks = 0
			return true
		}
		sess.eatingTicks--
		if sess.eatingTicks > 0 {
			return true
		}
		name, ok := itemNameOf(held.ID)
		if !ok {
			return true
		}
		stats, ok := foodItems[name]
		if !ok {
			return true
		}
		sess.food += stats.food
		if sess.food > defaultFood {
			sess.food = defaultFood
		}
		sess.saturation += stats.saturation
		if sess.saturation > float32(sess.food) {
			sess.saturation = float32(sess.food)
		}
		sess.consumeHeld(1)
		sess.addItemStat(statUsed, held.ID, 1)
		sess.healthDirty.Store(true)
		s.fireConsumeItem(sess, name)
		return true
	})
}

// eatTickInterval is the hunger tick cadence helper for tests.
var _ = time.Second
