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
// three entries. A returning name gets a reset stub - always carrying
// the bare speed entry plus the bare range entries and/or degenerate
// filler entries - followed by a second packet with the two ranged
// modifiers, which overwrite the bare entries. Entry order shuffles per
// send on the real server, so tests compare canonically.
//
// The fillers are 10-byte degenerate entries (ids 84 and 132, outside
// the 40-entry attribute registry) with no modifier count, replayed
// byte-for-byte: d4 00 08 plus seven zero bytes, and 84 01 20 plus
// seven zero bytes. Their shape never varied across captures, and the
// client demonstrably tolerates them - every vanilla rejoin carries
// them - so replaying the same bytes carries the same tolerance. Which
// stub mix a rejoin draws (bare ranges, one filler, or two) showed no
// pattern across captures, so mc-go sends one fixed mix and the tests
// pin the invariant (speed always present, modifiers always follow)
// rather than the exact mix.
const (
	attrBlockInteractionRange  = 8
	attrEntityInteractionRange = 13
	attrMovementSpeed          = 26

	attrModCreativeBlockRange  = "minecraft:creative_mode_block_range"
	attrModCreativeEntityRange = "minecraft:creative_mode_entity_range"
)

var (
	attrFillerBlock  = []byte{0xd4, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	attrFillerEntity = []byte{0x84, 0x01, 0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
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

// updateAttributesStub builds the rejoin reset: bare speed plus both
// filler entries.
func updateAttributesStub(eid int32) []byte {
	var b protocol.Buffer
	b.VarInt(eid)
	b.VarInt(3)
	writeAttrEntry(&b, attrMovementSpeed, float64(float32(0.1)))
	b.Raw(attrFillerEntity)
	b.Raw(attrFillerBlock)
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
