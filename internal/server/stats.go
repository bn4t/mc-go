package server

import (
	"strconv"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Player statistics: the per-player counters the client shows in its
// Statistics screen, sent as Award Statistics in reply to Client Status
// action 1 (and pushed again on respawn). Categories and their id
// spaces come from the minecraft:stat_type registry; within a category
// the id is a block, item, entity-type, or custom-stat id.
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

// statTypeNames is the stat_type registry name for each category, used
// to build a stat's hashable name.
var statTypeNames = [statCategoryN]string{
	"mined", "crafted", "used", "broken", "picked_up",
	"dropped", "killed", "killed_by", "custom",
}

// statKey identifies one counter.
type statKey struct {
	category int32
	id       int32
}

// statStore is a player's counter table. Two insertion histories are
// kept because the wire order of an Award Statistics packet depends on
// them (see statEmitOrder):
//
//   - insertOrder is first-award order, mirroring the key insertion
//     order of vanilla's Object2IntOpenHashMap stat table.
//   - dirtyOrder is the order keys entered vanilla's dirty HashSet
//     (award order, or markAllDirty's table iteration at join/respawn).
type statStore struct {
	counts      map[statKey]int32
	insertOrder []statKey
	dirty       map[statKey]bool
	dirtyOrder  []statKey
}

// ensureStats lazily builds the store.
func (sess *Session) ensureStats() *statStore {
	if sess.stats == nil {
		sess.stats = &statStore{
			counts: map[statKey]int32{},
			dirty:  map[statKey]bool{},
		}
	}
	return sess.stats
}

// addStat increments one counter, ignoring negative deltas that would
// take it below zero (vanilla clamps the same way). A changed counter
// enters the dirty set so the next stats request reports it.
func (sess *Session) addStat(category, id, delta int32) {
	if delta == 0 || id < 0 {
		return
	}
	sess.statMu.Lock()
	defer sess.statMu.Unlock()
	st := sess.ensureStats()
	k := statKey{category, id}
	next := st.counts[k] + delta
	if next < 0 {
		next = 0
	}
	if _, seen := st.counts[k]; !seen {
		st.insertOrder = append(st.insertOrder, k)
	}
	st.counts[k] = next
	if !st.dirty[k] {
		st.dirty[k] = true
		st.dirtyOrder = append(st.dirtyOrder, k)
	}
}

// setStat forces one counter's value (respawn resets, loads).
func (sess *Session) setStat(category, id, value int32) {
	sess.statMu.Lock()
	defer sess.statMu.Unlock()
	st := sess.ensureStats()
	k := statKey{category, id}
	if _, seen := st.counts[k]; !seen {
		st.insertOrder = append(st.insertOrder, k)
	}
	st.counts[k] = value
	if !st.dirty[k] {
		st.dirty[k] = true
		st.dirtyOrder = append(st.dirtyOrder, k)
	}
}

// addBlockStat records a statistic about a block; the value is the
// block-registry id (not the block-state id).
func (sess *Session) addBlockStat(category int32, b world.Block, delta int32) {
	name, ok := world.StateOwner(b)
	if !ok {
		return
	}
	id, ok := BlockID(name)
	if !ok {
		return
	}
	sess.addStat(category, id, delta)
}

// addItemStat records a statistic about an item.
func (sess *Session) addItemStat(category int32, itemID int32, delta int32) {
	sess.addStat(category, itemID, delta)
}

// addCustomStat records a custom statistic by short name.
func (sess *Session) addCustomStat(name string, delta int32) {
	id, ok := CustomStatID(name)
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

// markAllStatsDirty flags every existing counter dirty, the way
// vanilla's markAllDirty does at join and respawn. Vanilla iterates
// its stat map (a fastutil open hash map), so the dirty set receives
// keys in descending-slot order — that order matters because it feeds
// the wire emit order.
func (sess *Session) markAllStatsDirty() {
	sess.statMu.Lock()
	defer sess.statMu.Unlock()
	st := sess.ensureStats()
	for _, k := range fastutilIterDesc(st.insertOrder) {
		if !st.dirty[k] {
			st.dirty[k] = true
			st.dirtyOrder = append(st.dirtyOrder, k)
		}
	}
}

// sendStatistics answers Client Status action 1 with every dirty
// counter, then clears the dirty set. The emit order reproduces
// vanilla's chain: the dirty HashSet is copied (iterating in HashMap
// bucket order), entries are inserted into an Object2IntOpenHashMap in
// that order, and the packet codec iterates the map's slots
// descending.
func (s *Server) sendStatistics(sess *Session) error {
	sess.statMu.Lock()
	st := sess.ensureStats()
	order := statEmitOrder(st.dirtyOrder)
	values := make(map[statKey]int32, len(order))
	for _, k := range order {
		values[k] = st.counts[k]
	}
	st.dirty = map[statKey]bool{}
	st.dirtyOrder = nil
	sess.statMu.Unlock()

	var b protocol.Buffer
	b.VarInt(int32(len(order)))
	for _, k := range order {
		b.VarInt(k.category)
		b.VarInt(k.id)
		b.VarInt(values[k])
	}
	return sess.write(sess.ids().PlayAwardStats, b.Bytes())
}

// statEmitOrder computes the wire order for a set of dirty stats:
// Java-HashSet bucket order of the copy, then descending fastutil
// slots of the map that copy inserts into.
func statEmitOrder(dirtyOrder []statKey) []statKey {
	return fastutilIterDesc(javaHashSetIterOrder(dirtyOrder))
}

// statNameHash hashes a stat the way Stat.hashCode does: the Java
// String hash of "locationKey(typeId):locationKey(valueId)" where
// locationKey renders the identifier with '.' for ':'.
func statNameHash(k statKey) uint32 {
	var vname string
	var ok bool
	switch k.category {
	case statMined:
		vname, ok = blockIDName(k.id)
	case statCrafted, statUsed, statBroken, statPickedUp, statDropped:
		vname, ok = world.ItemName(k.id)
	case statKilled, statKilledBy:
		vname, ok = EntityTypeName(k.id)
	case statCustom:
		vname, ok = customStatName(k.id)
	}
	if !ok || k.category < 0 || int(k.category) >= len(statTypeNames) {
		// Unknown value: hash its numeric id — still deterministic,
		// just not the java name (these never reach a real client
		// because only resolvable stats get incremented).
		return uint32(k.category*1000003) ^ uint32(k.id)
	}
	return uint32(javaStringHash("minecraft." + statTypeNames[k.category] + ":minecraft." + vname))
}

// statPersistKey encodes a stat key for the data file.
func statPersistKey(k statKey) string {
	return strconv.Itoa(int(k.category)) + ":" + strconv.Itoa(int(k.id))
}

// parseStatPersistKey reads back a statPersistKey.
func parseStatPersistKey(s string) (statKey, bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return statKey{}, false
	}
	cat, err1 := strconv.Atoi(s[:i])
	id, err2 := strconv.Atoi(s[i+1:])
	if err1 != nil || err2 != nil {
		return statKey{}, false
	}
	return statKey{int32(cat), int32(id)}, true
}

// hashSpread is java.util.HashMap's supplemental hash.
func hashSpread(h uint32) uint32 { return h ^ (h >> 16) }

// javaHashSetIterOrder orders keys the way iterating a java HashSet
// does: bucket index ascending, insertion order within a bucket. The
// table starts at capacity 16 and doubles whenever size exceeds 3/4 of
// capacity; splits preserve relative order, so bucket-sorting the
// insertion sequence at the final capacity reproduces iteration order.
func javaHashSetIterOrder(keys []statKey) []statKey {
	n := hashMapCap(len(keys))
	mask := uint32(n - 1)
	buckets := make([][]statKey, n)
	for _, k := range keys {
		b := hashSpread(statNameHash(k)) & mask
		buckets[b] = append(buckets[b], k)
	}
	out := make([]statKey, 0, len(keys))
	for _, list := range buckets {
		out = append(out, list...)
	}
	return out
}

// fastutilIterDesc inserts keys into a simulated
// Object2IntOpenHashMap (open addressing, forward probing, table
// doubled once size reaches the .75 fill) and returns them in the
// map's iteration order: slot index descending.
func fastutilIterDesc(insertOrder []statKey) []statKey {
	n := 32 // arraySize(16, 0.75) — the map's default-constructor size
	for len(insertOrder) >= n*3/4 {
		n *= 2
	}
	mask := uint32(n - 1)
	table := make([]statKey, n)
	used := make([]bool, n)
	for _, k := range insertOrder {
		p := uint32(fastutilMix(int32(statNameHash(k)))) & mask
		for used[p] {
			p = (p + 1) & mask
		}
		used[p] = true
		table[p] = k
	}
	out := make([]statKey, 0, len(insertOrder))
	for i := n - 1; i >= 0; i-- {
		if used[i] {
			out = append(out, table[i])
		}
	}
	return out
}
