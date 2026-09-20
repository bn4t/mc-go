package server

import (
	"mc-go/internal/protocol"
)

// Join-time attribute sync, measured field-for-field against a live
// 26.2 server in creative mode.
//
// A joining player always ends up with the same three attributes: block
// interaction range (registry id 8, base 4.5, +0.5 from
// creative_mode_block_range), entity interaction range (id 13, base 3.0,
// +2.0 from creative_mode_entity_range), and movement speed (id 26, base
// float32(0.1) widened to float64, no modifiers). Each entry is an
// attribute id, a big-endian double base, and modifier
// (key string, big-endian double amount, op) triples.
//
// A name the world has never seen gets one Update Attributes with all
// three entries. A returning name gets a reset stub - the three bare
// entries with no modifiers - followed by a second packet with the two
// ranged modifiers, which overwrite the bare entries. Entry order
// shuffles per send on the real server, so tests compare canonically.
const (
	attrBlockInteractionRange  = 8
	attrEntityInteractionRange = 13
	attrMovementSpeed          = 26

	attrModCreativeBlockRange  = "minecraft:creative_mode_block_range"
	attrModCreativeEntityRange = "minecraft:creative_mode_entity_range"
)

// attrModifier is one attribute modifier triple: key, amount, op.
type attrModifier struct {
	key    string
	amount float64
	op     int32
}

// writeAttrEntry encodes one attribute entry: id, double base, and
// modifier triples.
func writeAttrEntry(b *protocol.Buffer, id int32, base float64, mods ...attrModifier) {
	b.VarInt(id)
	b.Double(base)
	b.VarInt(int32(len(mods)))
	for _, m := range mods {
		b.String(m.key)
		b.Double(m.amount)
		b.VarInt(m.op)
	}
}

// The two creative range modifiers.
func attrBlockMod() attrModifier {
	return attrModifier{attrModCreativeBlockRange, 0.5, 0}
}
func attrEntityMod() attrModifier {
	return attrModifier{attrModCreativeEntityRange, 2.0, 0}
}

// updateAttributesFull builds the fresh-join sync: all three entries
// with modifiers, no fillers.
func updateAttributesFull(eid int32) []byte {
	var b protocol.Buffer
	b.VarInt(eid)
	b.VarInt(3)
	writeAttrEntry(&b, attrBlockInteractionRange, 4.5, attrBlockMod())
	writeAttrEntry(&b, attrEntityInteractionRange, 3.0, attrEntityMod())
	writeAttrEntry(&b, attrMovementSpeed, float64(float32(0.1)))
	return b.Bytes()
}

// updateAttributesStub builds the rejoin reset: the three bare
// entries with no modifiers, which is the stub shape the real 26.2
// server sends (filler entries replayed from older captures carried
// ids past the 40-entry attribute registry and kick the real client).
func updateAttributesStub(eid int32) []byte {
	var b protocol.Buffer
	b.VarInt(eid)
	b.VarInt(3)
	writeAttrEntry(&b, attrBlockInteractionRange, 4.5)
	writeAttrEntry(&b, attrEntityInteractionRange, 3.0)
	writeAttrEntry(&b, attrMovementSpeed, float64(float32(0.1)))
	return b.Bytes()
}

// updateAttributesMods builds the rejoin follow-up: the two ranged
// modifiers, which overwrite the stub's bare and filler entries.
func updateAttributesMods(eid int32) []byte {
	var b protocol.Buffer
	b.VarInt(eid)
	b.VarInt(2)
	writeAttrEntry(&b, attrBlockInteractionRange, 4.5, attrBlockMod())
	writeAttrEntry(&b, attrEntityInteractionRange, 3.0, attrEntityMod())
	return b.Bytes()
}

// updateAttributesMode rebuilds the reach entries for a gamemode
// switch: creative carries the two range modifiers, every other mode
// carries the bare bases. Vanilla resends exactly these two entries
// (no speed) when creative is left or entered, and nothing at all for
// switches between the other three modes. Entry order shuffles per
// send on the real server; mc-go sends entity first and tests compare
// as sets.
func updateAttributesMode(eid int32, mode byte) []byte {
	var b protocol.Buffer
	b.VarInt(eid)
	b.VarInt(2)
	if mode == GameModeCreative {
		writeAttrEntry(&b, attrEntityInteractionRange, 3.0, attrEntityMod())
		writeAttrEntry(&b, attrBlockInteractionRange, 4.5, attrBlockMod())
	} else {
		writeAttrEntry(&b, attrEntityInteractionRange, 3.0)
		writeAttrEntry(&b, attrBlockInteractionRange, 4.5)
	}
	return b.Bytes()
}

// sendUpdateAttributes syncs the joiner's attributes after the opening
// chunk batch, where the real server sends them. A returning name gets
// the reset stub plus the modifier follow-up; a fresh name gets the
// full sync in one packet.
func (s *Server) sendUpdateAttributes(sess *Session) error {
	eid := sess.entityID.Load()
	if s.markJoined(sess.getName()) {
		if err := sess.write(sess.ids().PlayUpdateAttributes, updateAttributesStub(eid)); err != nil {
			return err
		}
		return sess.write(sess.ids().PlayUpdateAttributes, updateAttributesMods(eid))
	}
	return sess.write(sess.ids().PlayUpdateAttributes, updateAttributesFull(eid))
}

// markJoined records a play-stage join and reports whether the name
// joined before. A name restored from the data file counts as
// returning even on the boot's first sighting, the way vanilla's
// playerdata files do.
func (s *Server) markJoined(name string) bool {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	if s.seenJoin == nil {
		s.seenJoin = make(map[string]bool)
	}
	if s.seenJoin[name] {
		return true
	}
	_, known := s.savedPlayers[name]
	s.seenJoin[name] = true
	return known
}
