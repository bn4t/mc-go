package server

import (
	"fmt"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func menuProjectionSession(t *testing.T, s *Server, number int32, name string) *Session {
	t.Helper()
	sess, _ := enforceSession(t, s, name)
	profile, ok := javaProfileFor(number)
	if !ok {
		t.Fatal("missing profile")
	}
	sess.profile = profile
	// This codec fixture bypasses transport, not profile admission. These
	// packet numbers are independently identical in both official reports.
	sess.ver = Version{Name: profile.version.Name, Protocol: number, IDs: LatestVersion().IDs}
	return sess
}

func TestJavaChestContentProjectsItemsAndCursor(t *testing.T) {
	for _, tc := range []struct {
		protocol int32
		wireItem int32
	}{{775, 333}, {776, 360}} {
		t.Run(fmt.Sprint(tc.protocol), func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			sess := menuProjectionSession(t, s, tc.protocol, "ChestCodec")
			canonical := world.ItemIDs["crafting_table"]
			if canonical != 360 {
				t.Fatalf("canonical crafting table=%d", canonical)
			}
			chest := &chestData{}
			chest.slots[0] = ItemStack{ID: canonical, Count: 3}
			sess.window = &openWindow{id: 7, chest: chest, dim: s.dims[dimOverworld]}
			sess.windowState = 9
			sess.carried = ItemStack{ID: canonical, Count: 1}
			s.sendChestContent(sess)
			if len(sess.sendCh) != 1 {
				t.Fatalf("content packets=%d", len(sess.sendCh))
			}
			packet := materializeOutboundPacket(t, sess, <-sess.sendCh)
			if packet.id != sess.ids().PlayContainerContent {
				t.Fatalf("content ID=%d", packet.id)
			}
			r := protocol.NewPacketReader(packet.data)
			window, _ := r.VarInt()
			state, _ := r.VarInt()
			count, _ := r.VarInt()
			if window != 7 || state != 9 || count != 63 {
				t.Fatalf("content header=%d/%d/%d", window, state, count)
			}
			checkPlain := func(wantCount int32) {
				t.Helper()
				n, e := r.VarInt()
				id, ie := r.VarInt()
				add, ae := r.VarInt()
				remove, re := r.VarInt()
				if e != nil || ie != nil || ae != nil || re != nil || n != wantCount || id != tc.wireItem || add != 0 || remove != 0 {
					t.Fatalf("plain slot=%d/%d/%d/%d errors=%v/%v/%v/%v", n, id, add, remove, e, ie, ae, re)
				}
			}
			checkPlain(3)
			for i := 1; i < 63; i++ {
				if n, e := r.VarInt(); e != nil || n != 0 {
					t.Fatalf("empty slot%d=%d,%v", i, n, e)
				}
			}
			checkPlain(1)
			if r.Remaining() != 0 {
				t.Fatalf("content tail=%d", r.Remaining())
			}
			if chest.slots[0].ID != canonical || sess.carried.ID != canonical {
				t.Fatal("projection changed canonical inventory")
			}
		})
	}
}

func TestJavaMenuMissingItemCannotEmitPartialPacket(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	old := menuProjectionSession(t, s, 775, "OldMenu")
	current := menuProjectionSession(t, s, 776, "CurrentMenu")
	domain := builtinDomain(775, "minecraft:item")
	missing := int32(-1)
	for id, wire := range domain.from776 {
		if wire < 0 {
			missing = int32(id)
			break
		}
	}
	if missing < 0 {
		t.Fatal("fixture has no newer item")
	}
	chest := &chestData{}
	// First slot encodes successfully. The second fails after the packet header
	// and first slot exist in its local buffer; none may reach the writer queue.
	chest.slots[0] = ItemStack{ID: world.ItemIDs["crafting_table"], Count: 3}
	chest.slots[1] = ItemStack{ID: missing, Count: 1}
	for _, sess := range []*Session{old, current} {
		sess.window = &openWindow{id: 7, chest: chest, dim: s.dims[dimOverworld]}
	}
	s.sendChestContent(old)
	select {
	case <-old.done:
	default:
		t.Fatal("unrepresentable menu did not close viewer")
	}
	if len(old.sendCh) != 0 {
		t.Fatal("partial menu payload reached queue")
	}
	s.sendChestContent(current)
	select {
	case <-current.done:
		t.Fatal("other viewer closed")
	default:
	}
	if len(current.sendCh) != 1 {
		t.Fatalf("compatible viewer content=%d", len(current.sendCh))
	}
	packet := materializeOutboundPacket(t, current, <-current.sendCh)
	r := protocol.NewPacketReader(packet.data)
	_, _ = r.VarInt()
	_, _ = r.VarInt()
	_, _ = r.VarInt()
	_, err := current.profile.readSlot(r)
	if err != nil {
		t.Fatal(err)
	}
	item, err := current.profile.readSlot(r)
	if err != nil || item.ID != missing {
		t.Fatalf("compatible missing-item slot=%+v,%v", item, err)
	}
	if chest.slots[1].ID != missing {
		t.Fatal("strict rejection replaced authoritative item")
	}
}

func TestJavaMerchantCostsAndSellSlotUseTargetItems(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	sess := menuProjectionSession(t, s, 775, "TradeCodec")
	table := ItemStack{ID: world.ItemIDs["crafting_table"], Count: 2}
	e := &Entity{Offers: []merchantOffer{{buy: table, buyB: table, sell: table, maxUses: 12}}}
	s.sendMerchantOffers(sess, &openWindow{id: 4}, e)
	if len(sess.sendCh) != 1 {
		t.Fatalf("offer packets=%d", len(sess.sendCh))
	}
	packet := materializeOutboundPacket(t, sess, <-sess.sendCh)
	r := protocol.NewPacketReader(packet.data)
	_, _ = r.VarInt()
	count, _ := r.VarInt()
	if count != 1 {
		t.Fatalf("offers=%d", count)
	}
	cost := func() {
		t.Helper()
		id, _ := r.VarInt()
		n, _ := r.VarInt()
		predicate, _ := r.VarInt()
		if id != 333 || n != 2 || predicate != 0 {
			t.Fatalf("cost=%d/%d/%d", id, n, predicate)
		}
	}
	cost()
	item, err := sess.profile.readSlot(r)
	if err != nil || item != table {
		t.Fatalf("sell slot=%+v,%v", item, err)
	}
	present, err := r.Bool()
	if err != nil || !present {
		t.Fatalf("second cost present=%v,%v", present, err)
	}
	cost()
}

func TestJava775ChestClickRejectsTrailingPredictionBeforeMutation(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	sess := menuProjectionSession(t, s, 775, "Click775")
	table := ItemStack{ID: world.ItemIDs["crafting_table"], Count: 3}
	chest := &chestData{}
	chest.slots[0] = table
	sess.window = &openWindow{id: 7, chest: chest, dim: s.dims[dimOverworld]}
	sess.windowState = 9
	var b protocol.Buffer
	b.VarInt(7)
	b.VarInt(9)
	b.Short(0)
	b.Byte(0)
	b.VarInt(0)
	b.VarInt(1) // changed prediction: slot0, optional hashed stack
	b.Short(0)
	b.Bool(true)
	b.VarInt(333)
	b.VarInt(3)
	b.VarInt(0)
	b.VarInt(0)
	b.Bool(false) // optional hashed carried stack
	valid := append([]byte(nil), b.Bytes()...)
	malformed := append(append([]byte(nil), valid...), 0x7f)
	s.handleChestClickPacket(sess, protocol.Packet{ID: IDPlayClickContainerSB, Data: malformed})
	if chest.slots[0] != table || !sess.carried.Empty() {
		t.Fatal("trailing prediction mutated canonical inventory")
	}
	if len(sess.sendCh) != 0 {
		t.Fatal("invalid prediction dispatched a click")
	}
	s.handleChestClickPacket(sess, protocol.Packet{ID: IDPlayClickContainerSB, Data: valid})
	if !chest.slots[0].Empty() || sess.carried != table {
		t.Fatalf("valid775 click did not preserve canonical stack: chest=%+v carried=%+v", chest.slots[0], sess.carried)
	}
}
