package server

import (
	"encoding/json"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"testing"
)

func TestZeroDyedColorStackIdentityAndJSON(t *testing.T) {
	zero := ItemStack{ID: world.ItemIDs["stone"], Count: 1, DyedColorZero: true}
	plain := zero
	plain.DyedColorZero = false
	if zero.Stackable(plain) || plain.Stackable(zero) || !zero.Stackable(zero) {
		t.Fatal("explicit zero and absence identity collapsed")
	}
	raw, err := json.Marshal(zero)
	if err != nil {
		t.Fatal(err)
	}
	var got ItemStack
	if err = json.Unmarshal(raw, &got); err != nil || got != zero {
		t.Fatalf("JSON%#v %v", got, err)
	}
}

func TestZeroDyedColorCreativeProfiles(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	for _, number := range []int32{775, 776} {
		p, _ := javaProfileFor(number)
		sess := slotTestSession(p)
		sess.server = s
		want := ItemStack{ID: world.ItemIDs["leather_helmet"], Count: 1, DyedColorZero: true}
		var body protocol.Buffer
		body.Short(36)
		body.Raw(untrustedSlotForTest(t, p, want))
		s.handleCreativeSlot(sess, protocol.Packet{ID: IDPlayCreativeSlotSB, Data: body.Bytes()})
		if got := sess.stackAt(36); got != want {
			t.Fatalf("protocol%d creative%#v want%#v", number, got, want)
		}
	}
}

func TestZeroDyedColorNativeDisk(t *testing.T) {
	dir := t.TempDir()
	s := New("unused")
	t.Cleanup(s.Stop)
	s.WorldDir = dir
	pos := world.Pos{X: 3, Y: -60, Z: 3}
	s.setSilentBlock(s.dims[dimOverworld], pos, world.BlockStateMust("chest"))
	id := world.ItemIDs["leather_helmet"]
	wants := []ItemStack{{ID: id, Count: 1, DyedColorZero: true}, {ID: id, Count: 1, DyedColor: 1193046}, {ID: id, Count: 1}}
	chest := s.chestAt(s.dims[dimOverworld], pos)
	for i, want := range wants {
		chest.slots[i] = want
		native := itemNBT(want)
		if got := itemFromNBT(native); got != want {
			t.Fatalf("native item%d %#v want%#v", i, got, want)
		}
	}
	nested := new([shulkerSlots]ItemStack)
	nested[0] = wants[0]
	chest.slots[3] = ItemStack{ID: world.ItemIDs["shulker_box"], Count: 1, Contents: nested}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	r := New("unused")
	t.Cleanup(r.Stop)
	r.WorldDir = dir
	if err := r.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	restored := r.chestAt(r.dims[dimOverworld], pos)
	for i, want := range wants {
		if got := restored.slots[i]; got != want {
			t.Fatalf("disk%d %#v want%#v", i, got, want)
		}
	}
	if restored.slots[3].Contents == nil || restored.slots[3].Contents[0] != wants[0] {
		t.Fatal("nested zero dye lost")
	}
}

// Golden output from native public DyedItemColor.applyDyes; see
// work/native-zero-dye-public-api.measure.txt and ZeroDyePublicProbe.java.
func TestZeroDyedColorNativeMixGoldens(t *testing.T) {
	bases := []struct {
		rgb     int32
		present bool
	}{{0, false}, {0, true}, {255, true}, {1193046, true}}
	dyes := [][]int32{{14}, {0}, {15}, {14, 11}}
	goldens := [][]int32{{11546150, 16383998, 1908001, 11359128}, {5773075, 8159103, 921104, 7551078}, {8462807, 8159231, 921232, 6631880}, {8602195, 8755626, 1517627, 8081552}}
	for i, base := range bases {
		for j, ords := range dyes {
			if got := applyDyeMix(base.rgb, base.present, ords); got != goldens[i][j] {
				t.Errorf("base%d present%v dyes%v mix%d want%d", base.rgb, base.present, ords, got, goldens[i][j])
			}
		}
	}
}
