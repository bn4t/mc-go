package server

import (
	"sync"

	"mc-go/internal/world"
)

// Maps carry two component values on the wire: minecraft:map_id (a
// VarInt MapId) and minecraft:map_post_processing (a VarInt enum —
// lock 0, scale 1 — that resolves when a cartography result is
// taken). Everything else about a map lives server-side in the
// level's data storage; this store models just scale and locked, the
// two fields the cartography recipes read.
//
// Measured on 26.2 (MapItem, EmptyMapItem, MapItemSavedData bytecode):
//   - EmptyMapItem.use: consume(1), award item_used, play
//     ui.cartography_table.take_result at the player's own position,
//     then MapItem.create → createNewSavedData → getFreeMapId + a
//     createFresh entry (player block pos, scale 0, unlocked). An
//     emptied hand becomes the filled map; a leftover empty-map stack
//     keeps it and the filled map lands in the inventory instead.
//   - scaleMap/lockMap: getFreeMapId + setMapData(scaled()/locked())
//     + rewrite the stack's map_id — the post-processing component is
//     gone by then (onCraftedPostProcess removed it first).

// mapData is the slice of MapItemSavedData the menu gates on.
type mapData struct {
	scale  int32
	locked bool
}

// mapStore is a dimension's MapIdDataStorage: a free-id counter and
// the per-id data, both guarded because use paths and menu clicks run
// on different goroutines.
type mapStore struct {
	mu   sync.Mutex
	next int32
	data map[int32]mapData
}

// create stores a fresh scale-0 unlocked entry and returns its id.
func (ms *mapStore) create() int32 {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.data == nil {
		ms.data = map[int32]mapData{}
	}
	id := ms.next
	ms.next++
	ms.data[id] = mapData{}
	return id
}

// get reads one id's data; a stack carrying a never-created map_id
// has no saved data, which the cartography recipes treat as no result.
func (ms *mapStore) get(id int32) (mapData, bool) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	md, ok := ms.data[id]
	return md, ok
}

// derive stores src's data under a fresh id with the post-processing
// mutation applied — scale+1 for mapPostScale, locked for
// mapPostLock. A missing source id leaves the stack's map_id
// untouched, matching scaleMap/lockMap's null-data bail.
func (ms *mapStore) derive(src int32, post int32) int32 {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	md, ok := ms.data[src]
	if !ok {
		return -1
	}
	switch post {
	case mapPostScale:
		md.scale++
	case mapPostLock:
		md.locked = true
	}
	nid := ms.next
	ms.next++
	ms.data[nid] = md
	return nid
}

// useEmptyMap is EmptyMapItem.use: the hand's empty map becomes a
// filled_map keyed to a new scale-0 data entry, playing
// ui.cartography_table.take_result at the player.
func (s *Server) useEmptyMap(sess *Session, st ItemStack, slot int16) {
	if sess.mode() != GameModeCreative {
		st.Count--
	}
	d := sess.dimension()
	filled := ItemStack{ID: int32(world.ItemIDs["filled_map"]), Count: 1}
	filled.MapID = d.maps.create() + 1
	if id, ok := SoundEventID("ui.cartography_table.take_result"); ok {
		s.playSoundEntity(d.name, sess.entityID.Load(), id, 7, 1, 1, nil)
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if st.Empty() {
		// heldItemTransformedTo: the filled map lands in the hand slot.
		s.syncInventorySlot(sess, slot, filled)
		return
	}
	s.syncInventorySlot(sess, slot, st)
	if w != nil {
		s.addStackMenu(sess, w, filled)
	} else if rest, _ := sess.addStack(filled); !rest.Empty() {
		s.dropItemLocked(sess, rest)
	}
}
