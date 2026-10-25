package server

import (
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestHopperTransfersChestToChest(t *testing.T) {
	s := New("unused")
	base := world.Pos{X: 4, Y: 4, Z: 4}
	above := world.Pos{X: 4, Y: 5, Z: 4}
	below := world.Pos{X: 4, Y: 3, Z: 4}
	chest, _ := world.BlockState("chest")
	hopper, _ := world.BlockState("hopper")
	s.setBlockAndBroadcast("x", above, chest)
	s.setBlockAndBroadcast("x", base, hopper)
	s.setBlockAndBroadcast("x", below, chest)

	s.chestAt(s.dims[dimOverworld], above).slots[0] = ItemStack{ID: world.ItemIDs["stone"], Count: 3}

	for i := 0; i < 20; i++ {
		s.hopperTickStep()
	}
	h := s.hopperAt(s.dims[dimOverworld], base)
	if !h.slots[0].Empty() && h.slots[0].ID == world.ItemIDs["stone"] {
		// It may already have moved on to the lower chest.
	}
	lower := s.chestAt(s.dims[dimOverworld], below)
	total := int32(0)
	s.dims[dimOverworld].chests.mu.Lock()
	for i := range lower.slots {
		if lower.slots[i].ID == world.ItemIDs["stone"] {
			total += lower.slots[i].Count
		}
	}
	s.dims[dimOverworld].chests.mu.Unlock()
	if total == 0 {
		t.Fatalf("stone never reached the lower chest; hopper=%+v", h.slots)
	}
}

func TestHopperOpenPackets(t *testing.T) {
	s := New("unused")
	c, pr, _ := joinPlay(t, s, "Hopper")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)
	sess := s.findSessionByName("Hopper")
	pos := world.Pos{X: 8, Y: -61, Z: 0}
	hopper, _ := world.BlockState("hopper")
	s.setBlockAndBroadcast("Hopper", pos, hopper)
	drainToQuiet(t, pr)

	s.openHopper(sess, pos)
	os, err := waitPacket(t, pr, IDPlayOpenScreen)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(os.Data)
	if _, err := r.VarInt(); err != nil {
		t.Fatal(err)
	}
	if typ, err := r.VarInt(); err != nil || typ != menuHopper {
		t.Fatalf("menu type = %d, err %v (want %d)", typ, err, menuHopper)
	}
	cc, err := waitPacket(t, pr, IDPlayContainerContent)
	if err != nil {
		t.Fatal(err)
	}
	cr := protocol.NewPacketReader(cc.Data)
	_, _ = cr.VarInt()
	_, _ = cr.VarInt()
	if n, err := cr.VarInt(); err != nil || n != hopperMenuSlots {
		t.Fatalf("hopper slots = %d, err %v (want %d)", n, err, hopperMenuSlots)
	}
}

func TestHopperClickTransfer(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Hopper", 0.5, 0.5, 0.5)
	pos := world.Pos{X: 4, Y: 4, Z: 4}
	hopper, _ := world.BlockState("hopper")
	s.setBlockAndBroadcast("Hopper", pos, hopper)
	sess.setStack(invSlotForHotbar(0), ItemStack{ID: world.ItemIDs["stone"], Count: 2})
	s.openHopper(sess, pos)
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()

	// Pick hotbar (menu 5) then place into hopper slot 0.
	s.handleHopperClick(sess, w, w.hopper, 32, 0, 0)
	s.handleHopperClick(sess, w, w.hopper, 0, 0, 0)
	h := s.hopperAt(s.dims[dimOverworld], pos)
	if h.slots[0] != (ItemStack{ID: world.ItemIDs["stone"], Count: 2}) {
		t.Fatalf("hopper slot = %+v", h.slots[0])
	}
}
