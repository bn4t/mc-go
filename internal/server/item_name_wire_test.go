package server

import (
	"bytes"
	"encoding/hex"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"strings"
	"testing"
)

func TestItemCustomNameModifiedUTF8(t *testing.T) {
	// NBT payloads measured from Java25 writeUTF and vanilla26.2's anvil.
	for _, tc := range []struct{ name, payload string }{
		{"A\x00B", "08000441c08042"},
		{"😀", "080006eda0bdedb880"},
	} {
		id, _ := world.ItemID("iron_pickaxe")
		it := ItemStack{ID: id, Count: 1, Name: tc.name}
		var b protocol.Buffer
		writeSlot(&b, it)
		r := protocol.NewPacketReader(b.Bytes())
		for _, want := range []int32{1, id, 1, 0, compCustomName} {
			got, err := r.VarInt()
			if err != nil || got != want {
				t.Fatalf("slot header got=%d err=%v want=%d", got, err, want)
			}
		}
		raw, err := r.Read(r.Remaining())
		if err != nil {
			t.Fatal(err)
		}
		want, _ := hex.DecodeString(tc.payload)
		if !bytes.Equal(raw, want) {
			t.Fatalf("%q payload=%x want=%x", tc.name, raw, want)
		}
		decoded, err := readSlot(protocol.NewPacketReader(b.Bytes()))
		if err != nil || decoded.Name != tc.name {
			t.Fatalf("roundtrip %q: %+v err=%v", tc.name, decoded, err)
		}
	}
}

func TestItemCustomNameRejectsInvalidPayload(t *testing.T) {
	// A non-string custom name must fail before traversing an untrusted tree.
	r := protocol.NewPacketReader([]byte{nbt.TagCompound, 0, 0, 0})
	var it ItemStack
	if err := readSlotComponent(r, &it, compCustomName); err == nil || r.Remaining() != 3 {
		t.Fatalf("nonstring traversed: remaining=%d err=%v", r.Remaining(), err)
	}
	id, _ := world.ItemID("iron_pickaxe")
	for _, name := range []string{strings.Repeat("\x00", 32768), strings.Repeat("😀", 10923), "\xff"} {
		for _, version := range []int32{775, 776} {
			p, _ := javaProfileFor(version)
			var b protocol.Buffer
			if err := p.writeSlot(&b, ItemStack{ID: id, Count: 1, Name: name}); err == nil || len(b.Bytes()) != 0 {
				t.Fatalf("profile%d accepted invalid name: wrote=%d err=%v", version, len(b.Bytes()), err)
			}
			var inside [shulkerSlots]ItemStack
			inside[0] = ItemStack{ID: id, Count: 1, Name: name}
			if err := p.writeSlot(&b, ItemStack{ID: world.ItemIDs["shulker_box"], Count: 1, Contents: &inside}); err == nil || len(b.Bytes()) != 0 {
				t.Fatalf("profile%d accepted nested invalid name: wrote=%d err=%v", version, len(b.Bytes()), err)
			}
		}
	}
}
