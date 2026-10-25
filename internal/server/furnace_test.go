package server

import (
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestFurnaceSmelts(t *testing.T) {
	s := New("unused")
	pos := world.Pos{X: 4, Y: 4, Z: 4}
	furnace, _ := world.BlockState("furnace")
	s.setBlockAndBroadcast("x", pos, furnace)
	f := s.furnaceAt(s.dims[dimOverworld], pos)
	f.slots[furnaceInput] = ItemStack{ID: world.ItemIDs["sand"], Count: 1}
	f.slots[furnaceFuel] = ItemStack{ID: world.ItemIDs["coal"], Count: 1}

	for i := 0; i < furnaceCookTicks+2; i++ {
		s.furnaceTickStep()
	}
	s.dims[dimOverworld].furnaces.mu.Lock()
	defer s.dims[dimOverworld].furnaces.mu.Unlock()
	if got := f.slots[furnaceOutput]; got != (ItemStack{ID: world.ItemIDs["glass"], Count: 1}) {
		t.Fatalf("output = %+v, want glass", got)
	}
	if !f.slots[furnaceInput].Empty() {
		t.Fatalf("input = %+v, want empty", f.slots[furnaceInput])
	}
	if !f.slots[furnaceFuel].Empty() {
		t.Fatalf("fuel = %+v, want consumed", f.slots[furnaceFuel])
	}
	if f.burn <= 0 {
		t.Fatal("furnace stopped burning after one smelt")
	}
}

func TestFurnaceOpenPackets(t *testing.T) {
	s := New("unused")
	c, pr, _ := joinPlay(t, s, "Smelter")
	defer c.Close()
	drainJoinBurst(t, pr)
	drainToQuiet(t, pr)

	sess := s.findSessionByName("Smelter")
	pos := world.Pos{X: 8, Y: -61, Z: 0}
	furnace, _ := world.BlockState("furnace")
	s.setBlockAndBroadcast("Smelter", pos, furnace)
	drainToQuiet(t, pr)

	s.openFurnace(sess, pos)

	os, err := waitPacket(t, pr, IDPlayOpenScreen)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(os.Data)
	if _, err := r.VarInt(); err != nil { // window
		t.Fatal(err)
	}
	if typ, err := r.VarInt(); err != nil || typ != menuFurnace {
		t.Fatalf("menu type = %d, err %v (want %d)", typ, err, menuFurnace)
	}

	cc, err := waitPacket(t, pr, IDPlayContainerContent)
	if err != nil {
		t.Fatal(err)
	}
	cr := protocol.NewPacketReader(cc.Data)
	_, _ = cr.VarInt() // window
	_, _ = cr.VarInt() // state
	n, err := cr.VarInt()
	if err != nil || n != furnaceSlots+36 {
		t.Fatalf("furnace slots = %d, err %v (want %d)", n, err, furnaceSlots+36)
	}

	// All four progress properties arrive.
	for i := 0; i < 4; i++ {
		p, err := waitPacket(t, pr, IDPlayContainerProperty)
		if err != nil {
			t.Fatal(err)
		}
		pr := protocol.NewPacketReader(p.Data)
		if _, err := pr.VarInt(); err != nil {
			t.Fatal(err)
		}
		prop, err := pr.Short()
		if err != nil || prop != int16(i) {
			t.Fatalf("property %d = %d, err %v", i, prop, err)
		}
	}
}
