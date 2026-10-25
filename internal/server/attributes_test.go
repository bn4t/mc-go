package server

import (
	"testing"

	"mc-go/internal/protocol"
)

// attrEntry is one decoded Update Attributes entry.
type attrEntry struct {
	id   int32
	base float64
	mods []attrModifier
}

// readAttrEntry decodes one well-formed entry: id, double base, and
// modifier triples.
func readAttrEntry(t *testing.T, r *protocol.PacketReader) attrEntry {
	t.Helper()
	var e attrEntry
	var err error
	if e.id, err = r.VarInt(); err != nil {
		t.Fatalf("entry id: %v", err)
	}
	if e.base, err = r.Double(); err != nil {
		t.Fatalf("entry base: %v", err)
	}
	m, err := r.VarInt()
	if err != nil {
		t.Fatalf("entry mods: %v", err)
	}
	for i := int32(0); i < m; i++ {
		var mod attrModifier
		if mod.key, err = r.String(); err != nil {
			t.Fatalf("mod key: %v", err)
		}
		if mod.amount, err = r.Double(); err != nil {
			t.Fatalf("mod amount: %v", err)
		}
		if mod.op, err = r.VarInt(); err != nil {
			t.Fatalf("mod op: %v", err)
		}
		e.mods = append(e.mods, mod)
	}
	return e
}

// readAttrHead decodes the entity id and entry count.
func readAttrHead(t *testing.T, body []byte) (*protocol.PacketReader, int32, int32) {
	t.Helper()
	r := protocol.NewPacketReader(body)
	eid, err := r.VarInt()
	if err != nil {
		t.Fatalf("attributes eid: %v", err)
	}
	n, err := r.VarInt()
	if err != nil {
		t.Fatalf("attributes count: %v", err)
	}
	return r, eid, n
}

func TestUpdateAttributesFull(t *testing.T) {
	body := updateAttributesFull(1)
	if len(body) != 123 {
		t.Fatalf("full length %d, want 123", len(body))
	}
	r, eid, n := readAttrHead(t, body)
	if eid != 1 || n != 3 {
		t.Fatalf("head (%d,%d), want (1,3)", eid, n)
	}
	got := map[int32]attrEntry{}
	for i := int32(0); i < n; i++ {
		e := readAttrEntry(t, r)
		got[e.id] = e
	}
	if r.Remaining() != 0 {
		t.Fatalf("full trailing bytes: %d", r.Remaining())
	}
	block, ok := got[attrBlockInteractionRange]
	if !ok || block.base != 4.5 || len(block.mods) != 1 ||
		block.mods[0] != (attrModifier{attrModCreativeBlockRange, 0.5, 0}) {
		t.Fatalf("block range entry: %+v", block)
	}
	entity, ok := got[attrEntityInteractionRange]
	if !ok || entity.base != 3.0 || len(entity.mods) != 1 ||
		entity.mods[0] != (attrModifier{attrModCreativeEntityRange, 2.0, 0}) {
		t.Fatalf("entity range entry: %+v", entity)
	}
	speed, ok := got[attrMovementSpeed]
	if !ok || speed.base != float64(float32(0.1)) || len(speed.mods) != 0 {
		t.Fatalf("speed entry: %+v", speed)
	}
}

func TestUpdateAttributesRejoin(t *testing.T) {
	stub := updateAttributesStub(7)
	if len(stub) != 32 {
		t.Fatalf("stub length %d, want 32", len(stub))
	}
	r, eid, n := readAttrHead(t, stub)
	if eid != 7 || n != 3 {
		t.Fatalf("stub head (%d,%d), want (7,3)", eid, n)
	}
	// The real 26.2 stub is the three bare entries, no fillers.
	wantIDs := []int32{attrBlockInteractionRange, attrEntityInteractionRange, attrMovementSpeed}
	for i, want := range wantIDs {
		e := readAttrEntry(t, r)
		if e.id != want || len(e.mods) != 0 {
			t.Fatalf("stub entry %d: %+v", i, e)
		}
		if e.id < 0 || e.id >= 40 {
			t.Fatalf("stub id %d out of the 40-entry attribute registry", e.id)
		}
	}
	if r.Remaining() != 0 {
		t.Fatalf("stub trailing bytes: %d", r.Remaining())
	}
	mods := updateAttributesMods(7)
	if len(mods) != 113 {
		t.Fatalf("mods length %d, want 113", len(mods))
	}
	mr, meid, mn := readAttrHead(t, mods)
	if meid != 7 || mn != 2 {
		t.Fatalf("mods head (%d,%d), want (7,2)", meid, mn)
	}
	for i := int32(0); i < mn; i++ {
		e := readAttrEntry(t, mr)
		if len(e.mods) != 1 {
			t.Fatalf("mods entry without modifier: %+v", e)
		}
	}
	if mr.Remaining() != 0 {
		t.Fatalf("mods trailing bytes: %d", mr.Remaining())
	}
}

func TestUpdateTimeBodies(t *testing.T) {
	s := New("127.0.0.1:0")
	join := s.updateTimeBody()
	r := protocol.NewPacketReader(join)
	age, _ := r.Long()
	n, _ := r.VarInt()
	if n != 2 {
		t.Fatalf("join clocks %d, want 2", n)
	}
	id1, _ := r.VarInt()
	t1, _ := r.VarLong()
	r.Float()
	r.Float()
	id2, _ := r.VarInt()
	t2, _ := r.VarLong()
	r.Float()
	r.Float()
	if id1 != 0 || t1 != s.dayTime() {
		t.Fatalf("day clock (%d,%d), want (0,%d)", id1, t1, s.dayTime())
	}
	if id2 != 1 || t2 != age {
		t.Fatalf("age clock (%d,%d), want (1,%d)", id2, t2, age)
	}
	if r.Remaining() != 0 {
		t.Fatalf("join trailing bytes: %d", r.Remaining())
	}
	tick := s.updateTimeTickBody()
	if len(tick) != 9 {
		t.Fatalf("tick length %d, want 9", len(tick))
	}
	set := s.updateTimeSetBody(6000)
	if len(set) != 20 {
		t.Fatalf("set length %d, want 20", len(set))
	}
	rs := protocol.NewPacketReader(set)
	rs.Long()
	if n, _ := rs.VarInt(); n != 1 {
		t.Fatalf("set clocks %d, want 1", n)
	}
	if id, _ := rs.VarInt(); id != 0 {
		t.Fatalf("set clock id %d, want 0", id)
	}
	if tm, _ := rs.VarLong(); tm != 6000 {
		t.Fatalf("set clock time %d, want 6000", tm)
	}
}
