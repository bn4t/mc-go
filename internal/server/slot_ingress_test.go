package server

import (
	"bytes"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// The trusted payload comes from the normal profile encoder; this helper only
// adds the independently documented untrusted per-component byte-array envelope.
func untrustedSlotForTest(t *testing.T, p *javaProfile, it ItemStack) []byte {
	t.Helper()
	var trusted protocol.Buffer
	if err := p.writeSlot(&trusted, it); err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(trusted.Bytes())
	var out protocol.Buffer
	count, _ := r.VarInt()
	out.VarInt(count)
	if count == 0 {
		return out.Bytes()
	}
	id, _ := r.VarInt()
	add, _ := r.VarInt()
	remove, _ := r.VarInt()
	out.VarInt(id)
	out.VarInt(add)
	out.VarInt(remove)
	dialect, err := slotDialectFor(p)
	if err != nil {
		t.Fatal(err)
	}
	projector := slotProjector{source: dialect, target: dialect}
	for i := int32(0); i < add; i++ {
		typ, _ := r.VarInt()
		var payload protocol.Buffer
		if err = projector.component(r, &payload, dialect.builtin[slotComponentDomain].to776[typ], 0); err != nil {
			t.Fatal(err)
		}
		out.VarInt(typ)
		out.VarInt(int32(payload.Len()))
		out.Raw(payload.Bytes())
	}
	for i := int32(0); i < remove; i++ {
		typ, _ := r.VarInt()
		out.VarInt(typ)
	}
	if r.Remaining() != 0 {
		t.Fatal("test envelope did not consume source slot")
	}
	return out.Bytes()
}
func slotTestSession(p *javaProfile) *Session {
	s := &Session{profile: p, ver: p.version, sendCh: make(chan sendPkt, 16), done: make(chan struct{})}
	s.state.Store(StatePlay)
	s.setMode(GameModeCreative)
	return s
}

func TestCreativeSlotProfilesPreserveComponentsAndRejectBeforeMutation(t *testing.T) {
	server := New("unused")
	defer server.Stop()
	for _, number := range []int32{775, 776} {
		profile, _ := javaProfileFor(number)
		sess := slotTestSession(profile)
		sess.server = server
		want := ItemStack{ID: world.ItemIDs["iron_pickaxe"], Count: 1, Damage: 7, Name: "Canonical"}
		payload := untrustedSlotForTest(t, profile, want)
		packet := func(data []byte) protocol.Packet {
			var b protocol.Buffer
			b.Short(36)
			b.Raw(data)
			return protocol.Packet{ID: IDPlayCreativeSlotSB, Data: b.Bytes()}
		}
		server.handleCreativeSlot(sess, packet(payload))
		if got := sess.stackAt(36); got != want {
			t.Fatalf("protocol%d creative decoded %+v want%+v", number, got, want)
		}
		sess.setMode(GameModeSurvival)
		replacement := untrustedSlotForTest(t, profile, ItemStack{ID: world.ItemIDs["stone"], Count: 12})
		server.handleCreativeSlot(sess, packet(replacement))
		if got := sess.stackAt(36); got != want {
			t.Fatal("noncreative source mutated inventory")
		}
		sess.setMode(GameModeCreative)
		server.handleCreativeSlot(sess, packet(payload[:len(payload)-1]))
		if got := sess.stackAt(36); got != want {
			t.Fatal("truncated payload partially mutated inventory")
		}
		// A bad nested template must reject the whole creative action too.
		var nested protocol.Buffer
		nested.VarInt(1)
		nested.Bool(true)
		nested.VarInt(1 << 20)
		nested.VarInt(1)
		nested.VarInt(0)
		nested.VarInt(0)
		boxID, _ := profile.projectBuiltinRegistryID("minecraft:item", world.ItemIDs["shulker_box"])
		containerType, _ := profile.projectBuiltinRegistryID("minecraft:data_component_type", compContainer)
		var nestedSlot protocol.Buffer
		nestedSlot.VarInt(1)
		nestedSlot.VarInt(boxID)
		nestedSlot.VarInt(1)
		nestedSlot.VarInt(0)
		nestedSlot.VarInt(containerType)
		nestedSlot.VarInt(int32(nested.Len()))
		nestedSlot.Raw(nested.Bytes())
		server.handleCreativeSlot(sess, packet(nestedSlot.Bytes()))
		if got := sess.stackAt(36); got != want {
			t.Fatal("unsupported nested item partially mutated inventory")
		}
		// Full-body exactness is checked independently of component boundaries.
		server.handleCreativeSlot(sess, packet(append(append([]byte(nil), payload...), 0)))
		if got := sess.stackAt(36); got != want {
			t.Fatal("trailing data mutated inventory")
		}
		var resultSlot protocol.Buffer
		resultSlot.Short(0)
		resultSlot.Raw(replacement)
		server.handleCreativeSlot(sess, protocol.Packet{Data: resultSlot.Bytes()})
		if !sess.stackAt(0).Empty() {
			t.Fatal("creative result slot0 mutated")
		}
	}
}

func TestHashedCursorProfilesInverseMapAndBoundedCounts(t *testing.T) {
	for _, number := range []int32{775, 776} {
		p, _ := javaProfileFor(number)
		wire, err := p.projectBuiltinRegistryID("minecraft:item", world.ItemIDs["diamond"])
		if err != nil {
			t.Fatal(err)
		}
		var body protocol.Buffer
		body.Bool(true)
		body.VarInt(wire)
		body.VarInt(3)
		body.VarInt(1)
		body.VarInt(compDamage)
		body.Int(0x01020304)
		body.VarInt(0)
		r := protocol.NewPacketReader(body.Bytes())
		stack, ok := p.readHashedSlot(r)
		if !ok || stack.ID != world.ItemIDs["diamond"] || stack.Count != 3 || r.Remaining() != 0 {
			t.Fatalf("protocol%d cursor shape/inverse failed", number)
		}
		bad := protocol.Buffer{}
		bad.Bool(true)
		bad.VarInt(wire)
		bad.VarInt(3)
		bad.VarInt(1 << 30)
		if _, ok := p.readHashedSlot(protocol.NewPacketReader(bad.Bytes())); ok {
			t.Fatal("unbounded cursor hashes accepted")
		}
	}
}

func TestSlotRejectsUnboundedAndUnmodeledPayloads(t *testing.T) {
	p, _ := javaProfileFor(776)
	item, _ := p.projectBuiltinRegistryID("minecraft:item", world.ItemIDs["iron_pickaxe"])
	for _, build := range []func(*protocol.Buffer){
		func(b *protocol.Buffer) { b.VarInt(-1) },
		func(b *protocol.Buffer) { b.VarInt(1); b.VarInt(item); b.VarInt(-1); b.VarInt(0) },
		func(b *protocol.Buffer) {
			b.VarInt(1)
			b.VarInt(item)
			b.VarInt(1)
			b.VarInt(0)
			b.VarInt(compDamage)
			b.VarInt(1 << 30)
		},
		func(b *protocol.Buffer) {
			b.VarInt(1)
			b.VarInt(item)
			b.VarInt(1)
			b.VarInt(0)
			b.VarInt(compCustomName)
			b.VarInt(5)
			b.Byte(7)
			b.Int(1 << 30)
		},
		func(b *protocol.Buffer) {
			b.VarInt(1)
			b.VarInt(item)
			b.VarInt(1)
			b.VarInt(0)
			b.VarInt(compCustomName)
			var data protocol.Buffer
			_ = nbt.WriteUnnamed(&data, map[string]any{"text": "rich"})
			b.VarInt(int32(data.Len()))
			b.Raw(data.Bytes())
		},
	} {
		var raw protocol.Buffer
		build(&raw)
		if _, err := p.readUntrustedSlot(protocol.NewPacketReader(raw.Bytes())); err == nil {
			t.Fatalf("malformed/unmodeled input accepted: %x", raw.Bytes())
		}
	}
	var contents [shulkerSlots]ItemStack
	cyclic := ItemStack{ID: world.ItemIDs["shulker_box"], Count: 1, Contents: &contents}
	contents[0] = cyclic
	var b protocol.Buffer
	if err := p.writeSlot(&b, cyclic); err == nil {
		t.Fatal("cyclic contents accepted")
	}
}

func TestSlotFailureClosesBeforePartialPacketAdmission(t *testing.T) {
	p, _ := javaProfileFor(775)
	d, err := slotDialectFor(p)
	if err != nil {
		t.Fatal(err)
	}
	var missing int32 = -1
	for id, wire := range d.builtin[slotItemDomain].from776 {
		if wire < 0 {
			missing = int32(id)
			break
		}
	}
	if missing < 0 {
		t.Fatal("fixture has no unsupported775item")
	}
	var inside [shulkerSlots]ItemStack
	inside[0] = ItemStack{ID: missing, Count: 1}
	sess := slotTestSession(p)
	var b protocol.Buffer
	b.VarInt(0)
	b.VarInt(0)
	b.Short(36)
	sess.writeSlot(&b, ItemStack{ID: world.ItemIDs["shulker_box"], Count: 1, Contents: &inside})
	if !sess.closing.Load() {
		t.Fatal("strict missing slot did not retire viewer")
	}
	if err := sess.write(IDPlayContainerSlot, b.Bytes()); err == nil || len(sess.sendCh) != 0 {
		t.Fatal("partial slot packet was admitted after codec failure")
	}
	currentProfile, _ := javaProfileFor(776)
	current := slotTestSession(currentProfile)
	var good protocol.Buffer
	good.VarInt(0)
	good.VarInt(0)
	good.Short(36)
	current.writeSlot(&good, ItemStack{ID: world.ItemIDs["shulker_box"], Count: 1, Contents: &inside})
	if current.closing.Load() || current.write(IDPlayContainerSlot, good.Bytes()) != nil || len(current.sendCh) != 1 {
		t.Fatal("unsupported old viewer affected current viewer")
	}
	if inside[0] != (ItemStack{ID: missing, Count: 1}) {
		t.Fatal("projection rewrote canonical nested content")
	}

}

func TestModeledPresenceComponentsRoundTrip(t *testing.T) {
	stacks := []ItemStack{
		{ID: world.ItemIDs["white_banner"], Count: 1, Ominous: true},
		{ID: world.ItemIDs["shield"], Count: 1, BaseColor: 1},
		{ID: world.ItemIDs["ominous_bottle"], Count: 1, BottleAmpSet: true, BottleAmp: 0},
		{ID: world.ItemIDs["filled_map"], Count: 1, MapID: 1, MapPost: 1},
		{ID: world.ItemIDs["white_dye"], Count: 1, Dye: 1},
		{ID: world.ItemIDs["leather_helmet"], Count: 1, DyedColor: 0x112233},
		{ID: world.ItemIDs["goat_horn"], Count: 1, InstrumentRemoved: true},
	}
	for _, number := range []int32{775, 776} {
		p, _ := javaProfileFor(number)
		for _, want := range stacks {
			var b protocol.Buffer
			if err := p.writeSlot(&b, want); err != nil {
				t.Fatal(err)
			}
			r := protocol.NewPacketReader(b.Bytes())
			got, err := p.readSlot(r)
			if err != nil || got != want || r.Remaining() != 0 {
				t.Fatalf("protocol%d roundtrip %+v -> %+v err=%v raw=%x", number, want, got, err, b.Bytes())
			}
		}
	}
	// Projection adds no allocation over the existing four VarInt writes.
	// Buffer.VarInt currently allocates through its io.Writer implementation.
	p, _ := javaProfileFor(775)
	it := ItemStack{ID: world.ItemIDs["stone"], Count: 2}
	var canonical, projected protocol.Buffer
	baseline := testing.AllocsPerRun(100, func() { canonical.Reset(); writeSlot(&canonical, it) })
	actual := testing.AllocsPerRun(100, func() { projected.Reset(); _ = p.writeSlot(&projected, it) })
	if actual > baseline {
		t.Fatalf("plain projection adds allocations: canonical=%v projected=%v", baseline, actual)
	}
}

func TestEquipmentSessionProjectionContainsFailureAndPreservesSource(t *testing.T) {
	p775, _ := javaProfileFor(775)
	p776, _ := javaProfileFor(776)
	d, _ := slotDialectFor(p775)
	var missing int32 = -1
	for id, wire := range d.builtin[slotItemDomain].from776 {
		if wire < 0 {
			missing = int32(id)
			break
		}
	}
	var raw protocol.Buffer
	raw.VarInt(7)
	raw.Byte(0)
	writeSlot(&raw, ItemStack{ID: missing, Count: 1})
	original := append([]byte(nil), raw.Bytes()...)
	old, current := slotTestSession(p775), slotTestSession(p776)
	if err := old.write(old.ids().PlaySetEquipment, raw.Bytes()); err == nil || !old.closing.Load() || len(old.sendCh) != 0 {
		t.Fatal("unsupported equipment was enqueued or old viewer stayed active")
	}
	if err := current.write(current.ids().PlaySetEquipment, raw.Bytes()); err != nil || current.closing.Load() || len(current.sendCh) != 1 {
		t.Fatal("old equipment failure affected current viewer")
	}
	if !bytes.Equal(original, raw.Bytes()) {
		t.Fatal("projection modified source bytes")
	}
}
