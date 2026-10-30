package server

import "mc-go/internal/protocol"

// playerHeadTrackingTick advances the real player's tracker independently of
// world simulation. Body movement still uses its existing presentation path.
func (s *Server) playerHeadTrackingTick(sess *Session) {
	e, ok := s.entities.GetPtr(sess.entityID.Load())
	if !ok || e.presenceOnly {
		return
	}
	if !e.syncInit {
		s.latchSyncBaseline(e)
	}
	if e.SyncCount%s.entitySyncInterval(e) == 0 || e.PosDirty || e.MetaDirty || e.NeedsSync {
		head := angleByte(e.HeadYaw)
		if head != e.lastYHeadB {
			e.lastYHeadB = head
			var body protocol.Buffer
			body.VarInt(e.ID)
			body.Byte(head)
			s.broadcastToTrackers(e, body.Bytes(), func(ids idSet) int32 { return ids.PlayRotateHead })
		}
	}
	e.SyncCount++
}
