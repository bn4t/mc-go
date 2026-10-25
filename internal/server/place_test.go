package server

import (
	"testing"
	"time"

	"mc-go/internal/event"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestFaceOffsets(t *testing.T) {
	want := map[int32][3]int32{
		0: {0, -1, 0}, 1: {0, 1, 0}, 2: {0, 0, -1},
		3: {0, 0, 1}, 4: {-1, 0, 0}, 5: {1, 0, 0},
	}
	for f, d := range want {
		dx, dy, dz, ok := faceOffset(f)
		if !ok || dx != d[0] || dy != d[1] || dz != d[2] {
			t.Fatalf("face %d = %d,%d,%d,%v", f, dx, dy, dz, ok)
		}
	}
	if _, _, _, ok := faceOffset(6); ok {
		t.Fatal("face 6 accepted")
	}
	if _, _, _, ok := faceOffset(-1); ok {
		t.Fatal("face -1 accepted")
	}
}

func TestItemBlockMap(t *testing.T) {
	for id, blk := range map[int32]world.Block{
		ItemStone: world.Stone, ItemDirt: world.Dirt, ItemGrass: world.Grass,
	} {
		if got, ok := itemBlock(id); !ok || got != blk {
			t.Fatalf("item %d = %v,%v", id, got, ok)
		}
	}
	if _, ok := itemBlock(world.ItemIDs["stick"]); ok {
		t.Fatal("unknown item mapped")
	}
}

// placeSession builds a play-stage session with a drained queue.
func placeSession(t *testing.T, s *Server, name string, x, y, z float64) *Session {
	t.Helper()
	sess := &Session{
		server: s,
		sendCh: make(chan sendPkt, 64),
		done:   make(chan struct{}),
		ver:    LatestVersion(),
	}
	sess.teleportNext.Store(1)
	sess.setIdentity(name, offlineUUID(name))
	sess.state.Store(StatePlay)
	// A real client reports its terrain loaded right after joining; the
	// gate would otherwise drop the uses this helper's callers send.
	sess.loaded.Store(true)
	// Production seeds the per-player mode in initSurvival; the helper
	// mirrors it so tests exercise the same source of truth.
	sess.setMode(s.GameMode)
	sess.setPos(x, y, z, 0, 0, true)
	s.track(sess)
	// Stream the spawn window like the join burst does: block-update
	// fan-out only reaches sessions tracking the column, in production
	// as in tests.
	_ = s.sendWindow(sess, 0, 0)
	for len(sess.sendCh) > 0 {
		materializeOutboundPacket(t, sess, <-sess.sendCh)
	}
	t.Cleanup(func() { s.untrack(sess) })
	return sess
}

func useItemPkt(hand, x, y, z, face int32, seq *int32) protocol.Packet {
	var b protocol.Buffer
	b.VarInt(hand)
	b.Position(x, y, z)
	b.VarInt(face)
	b.Float(0.5)
	b.Float(0.5)
	b.Float(0.5)
	b.Bool(true)
	if seq != nil {
		b.VarInt(*seq)
	}
	return protocol.Packet{ID: IDPlayUseItemOnSB, Data: b.Bytes()}
}

func drainAcks(t *testing.T, sess *Session) (updates []protocol.Packet, acks []int32) {
	for {
		select {
		case pkt := <-sess.sendCh:
			pkt = materializeOutboundPacket(t, sess, pkt)
			switch pkt.id {
			case IDPlayBlockUpdateCB:
				updates = append(updates, protocol.Packet{ID: pkt.id, Data: pkt.data})
			case IDPlayBlockChangedAck:
				v, err := protocol.NewPacketReader(pkt.data).VarInt()
				if err == nil {
					acks = append(acks, v)
				}
			}
		default:
			return updates, acks
		}
	}
}

func TestPlaceStone(t *testing.T) {
	s := New("unused")
	ch, unsub := s.Bus().Subscribe(event.TopicBlockChange, 8)
	defer unsub()
	sess := placeSession(t, s, "Builder", 8.5, -60, 8.5)
	sess.setSlot(slotHotbarStart, int32(ItemStone), 1)
	sess.held.Store(0)

	seq := int32(7)
	s.handleUseItem(sess, useItemPkt(0, 8, -61, 8, 1, &seq))

	if got := s.world.GetBlock(world.Pos{X: 8, Y: -60, Z: 8}); got != world.Stone {
		t.Fatalf("target = %v, want stone", got)
	}
	updates, acks := drainAcks(t, sess)
	if len(acks) != 1 || acks[0] != 7 {
		t.Fatalf("acks = %v", acks)
	}
	// The wire is the write's block update, then the vanilla
	// client-prediction revert pair: BU(clicked) + BU(face, showing
	// the placed stone).
	if len(updates) != 3 {
		t.Fatalf("updates = %d, want 3", len(updates))
	}
	r := protocol.NewPacketReader(updates[0].Data)
	ux, uy, uz, err := r.Position()
	if err != nil || ux != 8 || uy != -60 || uz != 8 {
		t.Fatalf("update pos = %d,%d,%d err %v", ux, uy, uz, err)
	}
	if st, err := r.VarInt(); err != nil || st != StateStone {
		t.Fatalf("update state = %d err %v", st, err)
	}
	r = protocol.NewPacketReader(updates[1].Data)
	if ux, uy, uz, err := r.Position(); err != nil || ux != 8 || uy != -61 || uz != 8 {
		t.Fatalf("revert pos = %d,%d,%d err %v, want the clicked block", ux, uy, uz, err)
	}
	select {
	case e := <-ch:
		bc := e.Data.(BlockChange)
		if bc.Player != "Builder" || bc.X != 8 || bc.Y != -60 || bc.Z != 8 || bc.Block != world.Stone {
			t.Fatalf("event = %+v", bc)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no BlockChange event")
	}
}

func TestPlaceDenials(t *testing.T) {
	seq := int32(3)

	cases := []struct {
		name    string
		setup   func(s *Server, sess *Session)
		pkt     protocol.Packet
		updates int
	}{
		{"occupied", func(s *Server, sess *Session) {
			sess.setSlot(slotHotbarStart, int32(ItemStone), 1)
			s.world.SetBlock(world.Pos{X: 8, Y: -60, Z: 8}, world.Stone)
		}, useItemPkt(0, 8, -61, 8, 1, &seq), 2},
		{"unknown item", func(s *Server, sess *Session) {
			sess.setSlot(slotHotbarStart, 999, 1)
		}, useItemPkt(0, 8, -61, 8, 1, &seq), 2},
		{"empty hand", func(s *Server, sess *Session) {}, useItemPkt(0, 8, -61, 8, 1, &seq), 2},
		{"bad face", func(s *Server, sess *Session) {
			sess.setSlot(slotHotbarStart, int32(ItemStone), 1)
		}, useItemPkt(0, 8, 64, 8, 9, &seq), 0},
		{"click air", func(s *Server, sess *Session) {
			sess.setSlot(slotHotbarStart, int32(ItemStone), 1)
		}, useItemPkt(0, 8, 80, 8, 1, &seq), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Fresh world per case so setups never leak into each other.
			s := New("unused")
			ch, unsub := s.Bus().Subscribe(event.TopicBlockChange, 8)
			defer unsub()
			sess := placeSession(t, s, "Builder", 8.5, -60, 8.5)
			sess.held.Store(0)
			c.setup(s, sess)
			s.handleUseItem(sess, c.pkt)
			updates, acks := drainAcks(t, sess)
			// Every use_item_on that reaches dispatch ends with the
			// client-prediction revert pair, even a denied one; clicks
			// that never reach a block (bad face, air) emit none.
			if len(updates) != c.updates {
				t.Fatalf("denied place fanned %d updates, want %d", len(updates), c.updates)
			}
			if len(acks) != 1 || acks[0] != 3 {
				t.Fatalf("acks = %v, want [3]", acks)
			}
			select {
			case e := <-ch:
				t.Fatalf("denied place published %+v", e.Data)
			default:
			}
		})
	}

	// Out of reach denies even with a valid held block.
	s := New("unused")
	far := placeSession(t, s, "Far", 1000.5, 65, 1000.5)
	far.setSlot(slotHotbarStart, int32(ItemStone), 1)
	s.handleUseItem(far, useItemPkt(0, 8, -61, 8, 1, &seq))
	if updates, _ := drainAcks(t, far); len(updates) != 0 {
		t.Fatal("far place fanned updates")
	}
	if got := s.world.GetBlock(world.Pos{X: 8, Y: -60, Z: 8}); got != world.Air {
		t.Fatalf("far place wrote %v", got)
	}
}

func TestPlaceWithoutSeq(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Builder", 8.5, -60, 8.5)
	sess.setSlot(slotHotbarStart, int32(ItemDirt), 1)
	s.handleUseItem(sess, useItemPkt(0, 8, -61, 8, 1, nil))
	if got := s.world.GetBlock(world.Pos{X: 8, Y: -60, Z: 8}); got != world.Dirt {
		t.Fatalf("target = %v, want dirt", got)
	}
	updates, acks := drainAcks(t, sess)
	if len(updates) != 3 || len(acks) != 0 {
		t.Fatalf("updates=%d acks=%v", len(updates), acks)
	}
}

func TestHeldAndCreativeSlot(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Builder", 8.5, -60, 8.5)

	var b protocol.Buffer
	b.Short(3)
	s.handleHeld(sess, protocol.Packet{ID: IDPlayHeldChangeSB, Data: b.Bytes()})
	if sess.held.Load() != 3 {
		t.Fatalf("held = %d", sess.held.Load())
	}
	b.Reset()
	b.Short(9) // outside the hotbar: ignored
	s.handleHeld(sess, protocol.Packet{ID: IDPlayHeldChangeSB, Data: b.Bytes()})
	if sess.held.Load() != 3 {
		t.Fatalf("held = %d after bad slot", sess.held.Load())
	}

	// Hotbar menu slot36 carries a complete empty component patch.
	b.Reset()
	b.Short(slotHotbarStart)
	b.VarInt(1)
	b.VarInt(int32(ItemGrass))
	b.VarInt(0)
	b.VarInt(0)
	s.handleCreativeSlot(sess, protocol.Packet{ID: IDPlayCreativeSlotSB, Data: b.Bytes()})
	sess.held.Store(0)
	if id, ok := sess.heldItem(); !ok || id != int32(ItemGrass) {
		t.Fatalf("slot = %d,%v", id, ok)
	}
	// count 0 clears.
	b.Reset()
	b.Short(slotHotbarStart)
	b.VarInt(0)
	s.handleCreativeSlot(sess, protocol.Packet{ID: IDPlayCreativeSlotSB, Data: b.Bytes()})
	if _, ok := sess.heldItem(); ok {
		t.Fatal("cleared slot still reads")
	}
}

// TestPlaceFlowOverWire runs creative slot + held select + placement as
// real packets through a full play session.
func TestPlaceFlowOverWire(t *testing.T) {
	s := New("unused")
	pr, pw := joinPlayPipe(t, s, "Builder")

	// Stand next to the target in the flat world (spawn is at y=-60);
	// reach is enforced from the eye position.
	sendPosRot(t, pw, 7.5, -60, 7.5, 0, 0, true)

	var b protocol.Buffer
	b.Short(slotHotbarStart)
	b.VarInt(1)
	b.VarInt(int32(ItemStone))
	b.VarInt(0)
	b.VarInt(0)
	if err := pw.WritePacket(IDPlayCreativeSlotSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	b.Reset()
	b.Short(0)
	if err := pw.WritePacket(IDPlayHeldChangeSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	b.Reset()
	b.VarInt(0)
	b.Position(8, -61, 8)
	b.VarInt(1)
	b.Float(0.5)
	b.Float(0.5)
	b.Float(0.5)
	b.Bool(true)
	b.VarInt(11)
	if err := pw.WritePacket(IDPlayUseItemOnSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}

	sawUpdate, sawAck := false, false
	for i := 0; i < 40 && !(sawUpdate && sawAck); i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("place stream: %v", err)
		}
		switch p.ID {
		case IDPlayBlockUpdateCB:
			r := protocol.NewPacketReader(p.Data)
			x, y, z, err := r.Position()
			if err != nil {
				t.Fatalf("update pos: %v", err)
			}
			st, err := r.VarInt()
			if err != nil {
				t.Fatalf("update state: %v", err)
			}
			if x == 8 && y == -60 && z == 8 && st == StateStone {
				sawUpdate = true
			}
		case IDPlayBlockChangedAck:
			if seq, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil && seq == 11 {
				sawAck = true
			}
		}
	}
	if !sawUpdate || !sawAck {
		t.Fatalf("update=%v ack=%v", sawUpdate, sawAck)
	}
	if got := s.world.GetBlock(world.Pos{X: 8, Y: -60, Z: 8}); got != world.Stone {
		t.Fatalf("world = %v", got)
	}
}
