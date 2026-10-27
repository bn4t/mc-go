package server

import (
	"reflect"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func bundleTestItem(t *testing.T, name string, count int32) ItemStack {
	t.Helper()
	id, ok := world.ItemID(name)
	if !ok {
		t.Fatalf("unknown item %s", name)
	}
	return ItemStack{ID: id, Count: count}
}

func TestBundleWeightsInSixtyFourths(t *testing.T) {
	empty := bundleTestItem(t, "bundle", 1)
	if got := bundleItemWeight(empty); got != bundleNestedWeight {
		t.Fatalf("empty nested bundle weighs %d, want 1/16", got)
	}
	pearls := bundleTestItem(t, "ender_pearl", 3)
	dirt := bundleTestItem(t, "dirt", 5)
	sword := bundleTestItem(t, "iron_sword", 1)
	if got := bundleWeight([]ItemStack{pearls, dirt, sword}); got != 3*4+5+64 {
		t.Fatalf("content weight %d", got)
	}
	hive := bundleTestItem(t, "beehive", 1)
	hive.Bees = 2
	if bundleItemWeight(hive) != bundleCapacity {
		t.Fatal("an occupied hive fills a bundle")
	}
	inner := bundleTestItem(t, "red_bundle", 1)
	inner.Bundle = &BundleContents{Items: []ItemStack{dirt}}
	if got := bundleItemWeight(inner); got != 5+bundleNestedWeight {
		t.Fatalf("nested weight %d", got)
	}
}

func TestBundleInsertMergeOrderAndRemoval(t *testing.T) {
	m := newBundleMut(&BundleContents{})
	dirt := bundleTestItem(t, "dirt", 40)
	if n := m.tryInsert(&dirt); n != 40 || !dirt.Empty() {
		t.Fatalf("inserted %d left %+v", n, dirt)
	}
	pearls := bundleTestItem(t, "ender_pearl", 16)
	// 24/64 free fits six pearls at 4/64 each.
	if n := m.tryInsert(&pearls); n != 6 || pearls.Count != 10 {
		t.Fatalf("pearls inserted %d left %d", n, pearls.Count)
	}
	more := bundleTestItem(t, "dirt", 10)
	if n := m.tryInsert(&more); n != 0 || more.Count != 10 {
		t.Fatalf("full bundle took %d", n)
	}
	// Free one pearl's worth by removing it, then merge dirt: the
	// grown dirt entry moves back to the front.
	m.toggle(1)
	got, ok := m.removeOne()
	if !ok || got.Count != 40 || m.sel != -1 {
		t.Fatalf("selected removal %+v sel=%d", got, m.sel)
	}
	if n := m.tryInsert(&more); n != 10 || m.items[0].Count != 10 || len(m.items) != 2 {
		t.Fatalf("re-add %d items=%+v", n, m.items)
	}
	again := bundleTestItem(t, "dirt", 3)
	if n := m.tryInsert(&again); n != 3 || m.items[0].Count != 13 || m.items[1].ID != pearls.ID {
		t.Fatalf("merge order %+v", m.items)
	}
	// Unstackable items never merge and shulker boxes never enter.
	box := bundleTestItem(t, "shulker_box", 1)
	if m.tryInsert(&box) != 0 || box.Empty() {
		t.Fatal("a shulker box entered a bundle")
	}
	m.toggle(5)
	if m.sel != -1 {
		t.Fatal("out-of-range selection must clear")
	}
	m.toggle(1)
	m.toggle(1)
	if m.sel != -1 {
		t.Fatal("re-selecting the same entry must clear")
	}
	var it ItemStack = bundleTestItem(t, "bundle", 1)
	m.store(&it)
	if it.Bundle == nil || len(it.Bundle.Items) != 2 {
		t.Fatal("store lost contents")
	}
	m.items = nil
	m.store(&it)
	if it.Bundle != nil {
		t.Fatal("an emptied bundle must drop back to its default")
	}
}

func TestBundleContentsWireRoundTripBothProfiles(t *testing.T) {
	inner := bundleTestItem(t, "blue_bundle", 1)
	inner.Bundle = &BundleContents{Items: []ItemStack{bundleTestItem(t, "stone", 2)}}
	sword := bundleTestItem(t, "diamond_sword", 1)
	sword.Damage = 7
	sword.Name = "Edge"
	outer := bundleTestItem(t, "bundle", 1)
	outer.Bundle = &BundleContents{Items: []ItemStack{inner, sword, bundleTestItem(t, "ender_pearl", 3)}}
	for _, number := range []int32{775, 776} {
		p, _ := javaProfileFor(number)
		var b protocol.Buffer
		if err := p.writeSlot(&b, outer); err != nil {
			t.Fatalf("%d encode: %v", number, err)
		}
		r := protocol.NewPacketReader(b.Bytes())
		got, err := p.readSlot(r)
		if err != nil || r.Remaining() != 0 {
			t.Fatalf("%d decode: %v remaining=%d", number, err, r.Remaining())
		}
		if !reflect.DeepEqual(got, outer) {
			t.Fatalf("%d roundtrip\n got %+v\nwant %+v", number, got, outer)
		}
	}
	// The selection is server-side only: it never reaches the wire.
	sel := outer
	sel.Bundle = &BundleContents{Items: outer.Bundle.Items, Selected: 2}
	var a, b protocol.Buffer
	writeSlot(&a, outer)
	writeSlot(&b, sel)
	if string(a.Bytes()) != string(b.Bytes()) {
		t.Fatal("selection leaked onto the wire")
	}
	// An explicit empty list on a plain bundle decodes to the default.
	var e protocol.Buffer
	e.VarInt(1)
	e.VarInt(outer.ID)
	e.VarInt(1)
	e.VarInt(0)
	e.VarInt(compBundleContents)
	e.VarInt(0)
	if got, err := readSlot(protocol.NewPacketReader(e.Bytes())); err != nil || got.Bundle != nil {
		t.Fatalf("empty list decode %+v %v", got, err)
	}
}

func TestBundleContentsNBTAndCommandParse(t *testing.T) {
	st, _, ok := parseItemPatch(`bundle_contents=[{id:"minecraft:dirt",count:3},"minecraft:stick",{id:"minecraft:iron_sword",components:{"minecraft:damage":5}}]`, 2)
	if !ok || st.Bundle == nil || len(st.Bundle.Items) != 3 {
		t.Fatalf("parse %+v", st.Bundle)
	}
	items := st.Bundle.Items
	if items[0].Count != 3 || items[1].Count != 1 || items[2].Damage != 5 {
		t.Fatalf("parsed entries %+v", items)
	}
	st.ID = bundleTestItem(t, "bundle", 1).ID
	st.Count = 1
	back := itemFromNBT(map[string]any{
		"id":         "minecraft:bundle",
		"count":      int32(1),
		"components": itemComponentsNBT(st),
	})
	if !itemStacksEqual(back, st) {
		t.Fatalf("nbt roundtrip\n got %+v\nwant %+v", back.Bundle, st.Bundle)
	}
	if _, _, ok := parseItemPatch(`bundle_contents=[{id:"minecraft:dirt",count:0}]`, 2); ok {
		t.Fatal("count 0 must be rejected")
	}
}
