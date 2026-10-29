package server

import (
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"testing"
)

func TestBundleProfilesRoundTrip(t *testing.T) {
	child := ItemStack{ID: world.ItemIDs["stone"], Count: 13, Name: "nested 😀"}
	inner := ItemStack{ID: world.ItemIDs["blue_bundle"], Count: 1}
	bundleSetItems(&inner, []ItemStack{child})
	outer := ItemStack{ID: world.ItemIDs["bundle"], Count: 1}
	bundleSetItems(&outer, []ItemStack{inner})
	for _, number := range []int32{775, 776} {
		profile, ok := javaProfileFor(number)
		if !ok {
			t.Fatalf("missing profile%d", number)
		}
		var b protocol.Buffer
		if err := profile.writeSlot(&b, outer); err != nil {
			t.Fatalf("profile%d encode: %v", number, err)
		}
		r := protocol.NewPacketReader(b.Bytes())
		got, err := profile.readSlot(r)
		if err != nil || got != outer || r.Remaining() != 0 {
			t.Fatalf("profile%d roundtrip: got %+v err %v", number, got, err)
		}
	}
}
