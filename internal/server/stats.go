package server

import (
	"sort"

	"mc-go/internal/protocol"
)

// Player statistics: the per-player counters the client shows in its
// Statistics screen, sent as Award Statistics in reply to Client Status
// action 1. Categories and their id spaces come from the
// minecraft:stat_type registry; within a category the id is a block,
// item, entity-type, or custom-stat id.
const (
	statMined     = 0
	statCrafted   = 1
	statUsed      = 2
	statBroken    = 3
	statPickedUp  = 4
	statDropped   = 5
	statKilled    = 6
	statKilledBy  = 7
	statCustom    = 8
	statCategoryN = 9
)

// statKey identifies one counter.
type statKey struct {
	category int32
	id       int32
}

// statStore is a player's counter table plus the baseline last sent to
// the client. Vanilla only sends values that changed since the previous
// request, so the baseline is what makes repeat requests cheap.
type statStore struct {
	counts map[statKey]int32
	sent   map[statKey]int32
}

// addStat increments one counter, ignoring negative deltas that would
// take it below zero (vanilla clamps the same way).
func (sess *Session) addStat(category, id, delta int32) {
	if delta == 0 || id < 0 {
		return
	}
	sess.statMu.Lock()
	defer sess.statMu.Unlock()
	if sess.stats == nil {
		sess.stats = &statStore{
			counts: map[statKey]int32{},
			sent:   map[statKey]int32{},
		}
	}
	k := statKey{category, id}
	next := sess.stats.counts[k] + delta
	if next < 0 {
		next = 0
	}
	sess.stats.counts[k] = next
}

// addBlockStat records a statistic about a block, resolving the block
// state to its registry id (which is the same id space the report uses
// for blocks).
func (sess *Session) addBlockStat(category int32, blk BlockRef, delta int32) {
	sess.addStat(category, int32(blk), delta)
}

// addItemStat records a statistic about an item.
func (sess *Session) addItemStat(category int32, itemID int32, delta int32) {
	sess.addStat(category, itemID, delta)
}

// addCustomStat records a custom statistic by name.
func (sess *Session) addCustomStat(name string, delta int32) {
	id, ok := CustomStatID("minecraft:" + name)
	if !ok {
		return
	}
	sess.addStat(statCustom, id, delta)
}

// statCount reads one counter.
func (sess *Session) statCount(category, id int32) int32 {
	sess.statMu.Lock()
	defer sess.statMu.Unlock()
	if sess.stats == nil {
		return 0
	}
	return sess.stats.counts[statKey{category, id}]
}

// sendStatistics answers Client Status action 1 with every counter that
// changed since the last request, then advances the baseline. An
// unchanged table produces an empty packet, which is what vanilla
// sends when nothing moved.
func (s *Server) sendStatistics(sess *Session) error {
	sess.statMu.Lock()
	if sess.stats == nil {
		sess.stats = &statStore{counts: map[statKey]int32{}, sent: map[statKey]int32{}}
	}
	changed := make([]statKey, 0, len(sess.stats.counts))
	for k, v := range sess.stats.counts {
		if sess.stats.sent[k] != v {
			changed = append(changed, k)
			sess.stats.sent[k] = v
		}
	}
	values := make(map[statKey]int32, len(changed))
	for _, k := range changed {
		values[k] = sess.stats.counts[k]
	}
	sess.statMu.Unlock()

	sort.Slice(changed, func(i, j int) bool {
		if changed[i].category != changed[j].category {
			return changed[i].category < changed[j].category
		}
		return changed[i].id < changed[j].id
	})

	var b protocol.Buffer
	b.VarInt(int32(len(changed)))
	for _, k := range changed {
		b.VarInt(k.category)
		b.VarInt(k.id)
		b.VarInt(values[k])
	}
	return sess.write(sess.ids().PlayAwardStats, b.Bytes())
}

// BlockRef is a block state id as the statistics registry records it.
type BlockRef = uint16
