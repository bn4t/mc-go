package server

import (
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"testing"
)

func TestNativeItemComponentsRetainNestedAndTrim(t *testing.T) {
	helmet := map[string]any{"id": "minecraft:leather_helmet", "count": int32(1), "components": map[string]any{
		"minecraft:damage": int32(2), "minecraft:repair_cost": int32(5), "minecraft:custom_name": "minecraft:123",
		"minecraft:trim": map[string]any{"material": "minecraft:iron", "pattern": "minecraft:spire"},
	}}
	box := itemFromNBT(map[string]any{"id": "minecraft:shulker_box", "count": int32(1), "components": map[string]any{
		"minecraft:custom_name": "Outer",
		"minecraft:container":   nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"slot": int32(0), "item": helmet}}},
	}})
	if box.Name != "Outer" || box.Contents == nil {
		t.Fatalf("outer stack %#v", box)
	}
	child := box.Contents[0]
	if child.Damage != 2 || child.RepairCost != 5 || child.Name != "minecraft:123" || child.Trim != "iron/spire" {
		t.Fatalf("native child %#v", child)
	}
	restored := itemFromNBT(itemNBT(box))
	if restored.Contents == nil || restored.Contents[0].Damage != 2 || restored.Contents[0].Trim != "iron/spire" || restored.Name != "Outer" {
		t.Fatalf("save round trip %#v", restored)
	}
}

func TestNativeContainerComponentsSaveRestart(t *testing.T) {
	dir := t.TempDir()
	saved := New("unused")
	t.Cleanup(saved.Stop)
	saved.WorldDir = dir
	pos := world.Pos{X: 2, Y: -60, Z: 2}
	saved.setSilentBlock(saved.dims[dimOverworld], pos, world.BlockStateMust("chest"))
	helmetID, _ := world.ItemID("leather_helmet")
	boxID, _ := world.ItemID("shulker_box")
	helmet := ItemStack{ID: helmetID, Count: 1, Damage: 2, RepairCost: 5, Name: "minecraft:123", Trim: "iron/spire"}
	contents := new([shulkerSlots]ItemStack)
	contents[0] = helmet
	chest := saved.chestAt(saved.dims[dimOverworld], pos)
	chest.slots[0] = helmet
	chest.slots[1] = ItemStack{ID: boxID, Count: 1, Name: "Outer", Contents: contents}
	if err := saved.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	loaded := New("unused")
	t.Cleanup(loaded.Stop)
	loaded.WorldDir = dir
	if err := loaded.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	restored := loaded.chestAt(loaded.dims[dimOverworld], pos)
	check := func(stack ItemStack) {
		if stack.ID != helmetID || stack.Count != 1 || stack.Damage != 2 || stack.RepairCost != 5 || stack.Name != "minecraft:123" || stack.Trim != "iron/spire" {
			t.Fatalf("restored helmet %#v", stack)
		}
	}
	check(restored.slots[0])
	box := restored.slots[1]
	if box.ID != boxID || box.Name != "Outer" || box.Contents == nil {
		t.Fatalf("restored box %#v", box)
	}
	check(box.Contents[0])
}
