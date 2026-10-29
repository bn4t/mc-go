package server

import (
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

func oneBlockStructureForTest(state world.Block, tag map[string]any) map[string]any {
	name, _ := world.StateOwner(state)
	palette := map[string]any{"Name": "minecraft:" + name}
	props := map[string]any{}
	for k, v := range world.BlockProperties(state) {
		props[k] = v
	}
	if len(props) > 0 {
		palette["Properties"] = props
	}
	block := map[string]any{"state": int32(0), "pos": structureIntList([3]int32{})}
	if tag != nil {
		block["nbt"] = tag
	}
	return map[string]any{"size": structureIntList([3]int32{1, 1, 1}), "palette": nbt.List{Elem: nbt.TagCompound, Items: []any{palette}}, "blocks": nbt.List{Elem: nbt.TagCompound, Items: []any{block}}}
}
func TestStructurePlacementCallbackAndInventoryPolicy(t *testing.T) {
	s := simulationTestServer(t)
	d := s.dims[dimOverworld]
	controller := world.Pos{X: 4, Y: -60, Z: 4}
	target := world.Pos{X: 4, Y: -59, Z: 4}
	s.simulationAction(func() {
		load, _ := world.BlockStateOf("structure_block", map[string]string{"mode": "load"})
		s.setSilentBlock(d, controller, load)
		g := s.structureBlockAt(d, controller)
		redstone, _ := world.BlockState("redstone_block")
		s.setSilentBlock(d, world.Pos{X: 5, Y: -59, Z: 4}, redstone)
		tnt, _ := world.BlockState("tnt")
		g.fields["strict"] = int8(1)
		s.structureTemplatePlace(d, controller, g, oneBlockStructureForTest(tnt, nil))
		if d.w.GetBlock(target) != tnt {
			t.Fatal("strict placement ran TNT onPlace")
		}
		s.setSilentBlockWithPolicy(d, target, world.Air, true, true)
		g.fields["strict"] = int8(0)
		s.structureTemplatePlace(d, controller, g, oneBlockStructureForTest(tnt, nil))
		if d.w.GetBlock(target) != world.Air {
			t.Fatal("ordinary placement skipped TNT onPlace")
		}
		chest, _ := world.BlockState("chest")
		stone, _ := world.BlockState("stone")
		diamond, _ := world.ItemID("diamond")
		s.setSilentBlock(d, target, chest)
		retired := d.chests.chests[target]
		retired.slots[0] = ItemStack{ID: diamond, Count: 3}
		s.setSilentBlockWithPolicy(d, target, stone, true, true)
		if !retired.slots[0].Empty() || d.chests.chests[target] != nil {
			t.Fatal("strict removal retained stale inventory")
		}
		for _, e := range s.entities.All() {
			if name, _ := EntityTypeName(e.Type); name == "item" {
				t.Fatal("strict removal dropped inventory")
			}
		}
		s.setSilentBlock(d, target, chest)
		d.chests.chests[target].slots[0] = ItemStack{ID: diamond, Count: 3}
		s.setSilentBlock(d, target, stone)
		dropped := false
		for _, e := range s.entities.All() {
			if name, _ := EntityTypeName(e.Type); name == "item" {
				dropped = true
				s.entities.Remove(e.ID)
			}
		}
		if !dropped {
			t.Fatal("ordinary removal no longer drops inventory")
		}
		s.setSilentBlock(d, target, chest)
		retired = d.chests.chests[target]
		retired.slots[0] = ItemStack{ID: diamond, Count: 3}
		tag := map[string]any{"id": "minecraft:chest", "Items": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"Slot": int8(0), "id": "minecraft:iron_ingot", "count": int32(1)}}}}
		s.structureTemplatePlace(d, controller, g, oneBlockStructureForTest(chest, tag))
		if !retired.slots[0].Empty() {
			t.Fatal("NBT barrier retained stale inventory")
		}
		iron, _ := world.ItemID("iron_ingot")
		if got := d.chests.chests[target].slots[0]; got.ID != iron || got.Count != 1 {
			t.Fatalf("new inventory%+v", got)
		}
		for _, e := range s.entities.All() {
			if name, _ := EntityTypeName(e.Type); name == "item" {
				t.Fatal("NBT barrier spilled old inventory")
			}
		}
	})
}
func TestStructurePlacementStrictShapesAndWater(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "strict"}[strict], func(t *testing.T) {
			s := simulationTestServer(t)
			d := s.dims[dimOverworld]
			controller := world.Pos{X: 4, Y: -60, Z: 4}
			target := world.Pos{X: 4, Y: -59, Z: 4}
			outside := world.Pos{X: 3, Y: -59, Z: 4}
			s.simulationAction(func() {
				load, _ := world.BlockStateOf("structure_block", map[string]string{"mode": "load"})
				s.setSilentBlock(d, controller, load)
				g := s.structureBlockAt(d, controller)
				g.fields["strict"] = nbtBool(strict)
				fence, _ := world.BlockState("oak_fence")
				stone, _ := world.BlockState("stone")
				s.setSilentBlock(d, outside, fence)
				s.setSilentBlock(d, world.Pos{X: 5, Y: -59, Z: 4}, stone)
				s.structureTemplatePlace(d, controller, g, oneBlockStructureForTest(fence, nil))
				want := "true"
				if strict {
					want = "false"
				}
				for _, test := range []struct {
					p   world.Pos
					key string
				}{{target, "east"}, {target, "west"}, {outside, "east"}} {
					if got, _ := world.StateProperty(d.w.GetBlock(test.p), test.key); got != want {
						t.Fatalf("%v%s=%s want%s", test.p, test.key, got, want)
					}
				}
				water, _ := world.BlockState("water")
				s.setSilentBlock(d, target, water)
				slab, _ := world.BlockState("oak_slab")
				s.structureTemplatePlace(d, controller, g, oneBlockStructureForTest(slab, nil))
				if wet, _ := world.StateProperty(d.w.GetBlock(target), "waterlogged"); wet != "true" {
					t.Fatal("template did not retain source water")
				}
			})
		})
	}
}

func TestStructurePlacementPreservesNamesDuringNativeUpdates(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "strict"}[strict], func(t *testing.T) {
			s := simulationTestServer(t)
			d := s.dims[dimOverworld]
			controller := world.Pos{X: 4, Y: -60, Z: 4}
			target := world.Pos{X: 4, Y: -59, Z: 4}
			outside := world.Pos{X: 3, Y: -59, Z: 4}
			s.simulationAction(func() {
				load, _ := world.BlockStateOf("structure_block", map[string]string{"mode": "load"})
				s.setSilentBlock(d, controller, load)
				g := s.structureBlockAt(d, controller)
				g.fields["strict"] = nbtBool(strict)
				chest, _ := world.BlockState("chest")
				water, _ := world.BlockState("water")
				tag := map[string]any{"id": "minecraft:chest", "CustomName": "Incoming"}
				s.setSilentBlock(d, target, water)
				s.structureTemplatePlace(d, controller, g, oneBlockStructureForTest(chest, tag))
				if d.beNames[target] != "Incoming" {
					t.Fatal("waterlogging lost native name")
				}
				s.setSilentBlock(d, target, world.Air)
				s.setSilentBlock(d, outside, chest)
				d.beNames[outside] = "Neighbor"
				s.structureTemplatePlace(d, controller, g, oneBlockStructureForTest(chest, tag))
				if d.beNames[target] != "Incoming" || d.beNames[outside] != "Neighbor" {
					t.Fatalf("shape updates lost names target%q neighbor%q", d.beNames[target], d.beNames[outside])
				}
			})
		})
	}
}
