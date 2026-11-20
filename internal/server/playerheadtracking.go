package server

import "mc-go/internal/protocol"

// playerHeadTrackingTick advances the real player's tracker independently of
// world simulation. Vanilla's ChunkMap.tick walks the whole entityMap —
// ServerPlayer included — so a player's TrackedEntity rides the same
// ServerEntity.sendChanges as every other tracked entity: a stationary
// player observed by another client answers its pairing with a tick-0
// zero-delta move_entity_pos, then forced tickCount%60 zero-delta
// periodics every 60 ticks, and one entity_position_sync once
// teleportDelay passes 400 gate-opens (401 × interval 2 = 802 ticks
// after the last absolute). mc-go's session path owns the movement echo
// and commitPlayerPoseWithEffectsOwned re-latches the send baselines
// behind each echo, so this pass measures the committed record: a
// stationary player rides the plain cadence and a moving one measures
// zero delta. Body movement still uses its existing presentation path.
//
// Riders keep the head-only half: mc-go seats a riding player through
// rideTickStep's session weld and the rider's movement echoes, never a
// per-tick record weld, so a tracker position packet from a rider whose
// client stops sending would carry a stale seat. Vanilla's rider
// cadence rides positionRider's per-tick record weld — a recorded
// open gap.
func (s *Server) playerHeadTrackingTick(sess *Session) {
	e, ok := s.entities.GetPtr(sess.entityID.Load())
	if !ok || e.presenceOnly {
		return
	}
	if sess.riding.Load() != 0 {
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
		return
	}
	s.mobSyncFlush(e)
}
