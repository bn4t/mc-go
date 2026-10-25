package server

import (
	"testing"

	"mc-go/internal/protocol"
)

func TestEquipmentEncoding(t *testing.T) {
	entries := []struct {
		slot  byte
		stack ItemStack
	}{
		{equipMainHand, ItemStack{ID: 1, Count: 1}},
		{equipHelmet, ItemStack{ID: 2, Count: 1}},
	}
	body := encodeEquipment(7, entries)
	r := protocol.NewPacketReader(body)
	if eid, err := r.VarInt(); err != nil || eid != 7 {
		t.Fatalf("eid = %d, err %v", eid, err)
	}
	slot, err := r.Byte()
	if err != nil || slot != equipMainHand|0x80 {
		t.Fatalf("first slot = %#x, err %v (want continuation)", slot, err)
	}
	if _, err := readSlot(r); err != nil {
		t.Fatal(err)
	}
	slot, err = r.Byte()
	if err != nil || slot != equipHelmet {
		t.Fatalf("second slot = %#x, err %v (want no continuation)", slot, err)
	}
	if _, err := readSlot(r); err != nil {
		t.Fatal(err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("trailing = %d", r.Remaining())
	}
}
