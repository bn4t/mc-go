package server

import (
	"sort"
	"strings"
)

// Restored records do not go through ordinary scheduling's duplicate gate.
// Membership is separate: polling any loaded duplicate removes the owner's
// membership even if another loaded record for it remains pending.
func (t *tickScheduler) restoreTick(k schedKey, at int64, priority int8) {
	k.recordID = 0
	t.mu.Lock()
	defer t.mu.Unlock()
	t.restoreTickLocked(k, at, priority)
}

func (t *tickScheduler) restoreTickLocked(k schedKey, at int64, priority int8) {
	if t.owners == nil {
		t.owners = map[schedKey]bool{}
	}
	t.owners[k] = true
	t.nextRecordID++
	k.recordID = t.nextRecordID
	t.queued[k] = at
	t.prio[k] = priority
	t.due[at] = append(t.due[at], k)
	t.indexTick(k, at)
}

func (s *Server) restoreScheduledChunkTicks() {
	if s.ticks == nil {
		return
	}
	now := s.TickCount()
	for dim, d := range s.dims {
		chunks := d.w.SavedChunkTicks()
		keys := make([][2]int32, 0, len(chunks))
		for key := range chunks {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i][0] != keys[j][0] {
				return keys[i][0] < keys[j][0]
			}
			return keys[i][1] < keys[j][1]
		})
		for _, key := range keys {
			chunk := chunks[key]
			for _, tick := range chunk.Blocks {
				name := strings.TrimPrefix(tick.Type, "minecraft:")
				kind := tickBlock
				switch name {
				case "command_block":
					kind = tickCommandImpulse
				case "repeating_command_block":
					kind = tickCommandRepeat
				case "chest":
					kind = tickChestRecheck
				case "trapped_chest":
					kind = tickTrappedChestRecheck
				}
				s.ticks.restoreTick(schedKey{dim: dim, pos: tick.Pos, kind: kind, block: name}, now+int64(tick.Delay), int8(tick.Priority))
			}
			for _, tick := range chunk.Fluids {
				s.ticks.restoreTick(schedKey{dim: dim, pos: tick.Pos, kind: tickFluid, fluid: strings.TrimPrefix(tick.Type, "minecraft:")}, now+int64(tick.Delay), int8(tick.Priority))
			}
		}
	}
}
