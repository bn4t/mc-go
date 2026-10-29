package server

import (
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"testing"
)

func TestBundleContentsCapacityAndSelection(t *testing.T) {
	bundle := ItemStack{ID: world.ItemIDs["bundle"], Count: 1}
	stone := ItemStack{ID: world.ItemIDs["stone"], Count: 60}
	if got := bundleInsert(&bundle, &stone); got != 60 || !stone.Empty() {
		t.Fatalf("insert=%d remaining=%v", got, stone)
	}
	eggs := ItemStack{ID: world.ItemIDs["egg"], Count: 2}
	if got := bundleInsert(&bundle, &eggs); got != 1 || eggs.Count != 1 {
		t.Fatalf("16-stack capacity insert=%d remaining=%v", got, eggs)
	}
	bundleToggle(&bundle, 1)
	if got := bundleRemove(&bundle); got.ID != world.ItemIDs["stone"] || got.Count != 60 {
		t.Fatalf("selected extraction=%v", got)
	}
	bundleToggle(&bundle, 0)
	bundleToggle(&bundle, 0)
	if bundle.BundleSelected != 0 {
		t.Fatal("selection did not toggle off")
	}
	nested := ItemStack{ID: world.ItemIDs["bundle"], Count: 1}
	if got := bundleInsert(&nested, &bundle); got != 1 || !bundle.Empty() {
		t.Fatalf("nested insert %d", got)
	}
	source := ItemStack{ID: world.ItemIDs["stone"], Count: 64}
	if got := bundleInsert(&nested, &source); got != 56 {
		t.Fatalf("nested overhead insert=%d", got)
	}
	shulker := ItemStack{ID: world.ItemIDs["shulker_box"], Count: 1}
	if bundleInsert(&nested, &shulker) != 0 {
		t.Fatal("accepted shulker")
	}
}
func TestBundleNestedCodecAndNBT(t *testing.T) {
	bundle := ItemStack{ID: world.ItemIDs["blue_bundle"], Count: 1}
	child := ItemStack{ID: world.ItemIDs["stone"], Count: 5, Name: "Kept"}
	bundleSetItems(&bundle, []ItemStack{child})
	bundleToggle(&bundle, 0)
	var b protocol.Buffer
	writeSlot(&b, bundle)
	got, err := readCanonicalSlot(protocol.NewPacketReader(b.Bytes()), 0)
	if err != nil || got.Bundle != bundle.Bundle || got.BundleSelected != 0 {
		t.Fatalf("wire %v %v", got, err)
	}
	back := itemFromNBT(itemNBT(bundle))
	if back.Bundle != bundle.Bundle || back.BundleSelected != 0 {
		t.Fatalf("NBT %v", back)
	}
}

func TestBundleUseCadenceAndRelease(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "BundleUser", 2.5, 70, 3.5)
	bundle := ItemStack{ID: world.ItemIDs["bundle"], Count: 1}
	bundleSetItems(&bundle, []ItemStack{{ID: world.ItemIDs["stone"], Count: 4}, {ID: world.ItemIDs["dirt"], Count: 3}, {ID: world.ItemIDs["sand"], Count: 2}})
	sess.setStack(slotOffhand, bundle)
	s.startBundleUse(sess, 1, bundle)
	if enderEyeUseFlags(sess) != 3 {
		t.Fatal("offhand active-use flag absent")
	}
	s.bundleUseTick(sess)
	if len(bundleItems(sess.stackAt(slotOffhand))) != 2 {
		t.Fatal("no initial ejection")
	}
	for i := 0; i < 11; i++ {
		s.bundleUseTick(sess)
	}
	if len(bundleItems(sess.stackAt(slotOffhand))) != 2 {
		t.Fatal("ejected inside initial delay")
	}
	s.bundleUseTick(sess)
	if len(bundleItems(sess.stackAt(slotOffhand))) != 1 {
		t.Fatal("no first repeated ejection")
	}
	s.stopBundleUse(sess)
	s.bundleUseTick(sess)
	if len(bundleItems(sess.stackAt(slotOffhand))) != 1 || enderEyeUseFlags(sess) != 2 {
		t.Fatal("release continued or retained active flag")
	}
	for _, e := range s.entities.All() {
		if e.Item.Empty() {
			continue
		}
		if e.X != 2.5 || e.Z != 3.5 || e.PickupDelay != 40 || e.Thrower != sess.entityID.Load() {
			t.Fatalf("wrong drop placement/owner: %+v", e)
		}
	}
}
