package server

import (
	"mc-go/internal/protocol"
	"sort"
)

// Snapshot only objectives visible in a display slot. A repeated objective
// shares one creation and score snapshot, with all its display assignments.
func (s *Server) sendScoreboardSnapshot(sess *Session) error {
	s.board.mu.Lock()
	var slots []int
	for slot, name := range s.board.displaySlots {
		if name != "" {
			slots = append(slots, slot)
		}
	}
	sort.Ints(slots)
	holders := append([]string(nil), s.board.holderOrder...)
	javaHashOrder(holders, func(holder string) string { return holder })
	seen := map[string]bool{}
	var packets []protocol.Packet
	for _, slot := range slots {
		name := s.board.displaySlots[slot]
		if seen[name] {
			continue
		}
		seen[name] = true
		packets = append(packets, protocol.Packet{ID: IDPlayObjectiveCB, Data: scoreboardObjectivePacket(name, 0, s.board.objectives[name])})
		for _, assignment := range s.scoreboardSlotsLocked(name) {
			packets = append(packets, protocol.Packet{ID: IDPlayDisplayObjective, Data: scoreboardDisplayPacket(int32(assignment), name)})
		}
		for _, holder := range holders {
			if score := s.board.scores[holder][name]; score != nil {
				packets = append(packets, protocol.Packet{ID: IDPlayUpdateScore, Data: scoreboardScorePacket(holder, name, score.value, score)})
			}
		}
	}
	s.board.mu.Unlock()
	for _, packet := range packets {
		if err := sess.write(packet.ID, packet.Data); err != nil {
			return err
		}
	}
	return nil
}

func scoreboardObjectivePacket(name string, mode byte, o *sbObjective) []byte {
	var b protocol.Buffer
	b.String(name)
	b.Byte(mode)
	if mode != 1 {
		display := any(name)
		if o != nil && o.display != nil {
			display = o.display
		}
		b.Raw(componentAnyContent(display))
		rt := 0
		var format *sbNumberFormat
		if o != nil {
			rt = o.renderType
			format = o.numberFormat
		}
		b.VarInt(int32(rt))
		writeNumberFormat(&b, format)
	}
	return b.Bytes()
}

func scoreboardDisplayPacket(position int32, name string) []byte {
	var b protocol.Buffer
	b.VarInt(position)
	b.String(name)
	return b.Bytes()
}

func scoreboardScorePacket(entity, objective string, value int32, e *sbScore) []byte {
	var b protocol.Buffer
	b.String(entity)
	b.String(objective)
	b.VarInt(value)
	if e != nil && e.display != nil {
		b.Bool(true)
		b.Raw(componentAnyContent(e.display))
	} else {
		b.Bool(false)
	}
	if e != nil {
		writeNumberFormat(&b, e.format)
	} else {
		b.Bool(false)
	}
	return b.Bytes()
}
