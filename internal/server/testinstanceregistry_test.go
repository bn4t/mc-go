package server

import (
	"bytes"
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"reflect"
	"testing"
)

func TestTestInstanceRegisteredCatalogAndDescription(t *testing.T) {
	def := testInstanceDefinition("minecraft:always_pass")
	if def["structure"] != "minecraft:empty" || def["function"] != "minecraft:always_pass" || def["environment"] != "minecraft:default" || def["setup_ticks"] != int32(1) || def["max_ticks"] != int32(1) {
		t.Fatal(def)
	}
	v, err := nbt.ReadUnnamed(bytes.NewReader(testInstanceDescription(def).content()))
	if err != nil {
		t.Fatal(err)
	}
	row := func(key string, value any) map[string]any {
		return map[string]any{"translate": "test_instance.description." + key, "with": nbt.List{Elem: nbt.TagCompound, Items: []any{value}}, "extra": nbt.List{Elem: nbt.TagString, Items: []any{"\n"}}}
	}
	batch := row("batch", map[string]any{"color": "blue", "text": "minecraft:default"})
	info := row("structure", map[string]any{"color": "blue", "text": "minecraft:empty"})
	info["extra"] = nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"": "\n"}, batch}}
	want := row("type", map[string]any{"color": "blue", "translate": "test_instance.type.function"})
	want["extra"] = nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"": "\n"}, row("function", map[string]any{"color": "blue", "text": "minecraft:always_pass"}), info}}
	if !reflect.DeepEqual(v, want) {
		t.Fatalf("description\ngot %#v\nwant %#v", v, want)
	}
}
func TestTestInstanceRegisteredSaveAndRotatedReset(t *testing.T) {
	s := simulationTestServer(t)
	s.WorldDir = t.TempDir()
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 4, Y: 70, Z: 4}
	s.simulationAction(func() {
		data := testInstanceData{test: "minecraft:always_pass", size: [3]int32{2, 1, 3}, ignoreEntities: true}
		gold, _ := world.BlockState("gold_block")
		stone, _ := world.BlockState("stone")
		diamond, _ := world.BlockState("diamond_block")
		d.w.SetBlock(world.Pos{X: 4, Y: 71, Z: 5}, gold)
		d.w.SetBlock(world.Pos{X: 5, Y: 71, Z: 7}, stone)
		if name := s.testInstanceSave(d, pos, data); name != "minecraft:empty" {
			t.Fatal(name)
		}
		if s.structureTemplateGet("minecraft:always_pass") != nil {
			t.Fatal("saved under test instead of structure name")
		}
		corners := [][2]world.Pos{{{X: 4, Y: 71, Z: 5}, {X: 5, Y: 71, Z: 7}}, {{X: 6, Y: 71, Z: 5}, {X: 4, Y: 71, Z: 6}}, {{X: 5, Y: 71, Z: 7}, {X: 4, Y: 71, Z: 5}}, {{X: 4, Y: 71, Z: 6}, {X: 6, Y: 71, Z: 5}}}
		for rotation, points := range corners {
			for x := 4; x <= 6; x++ {
				for z := 5; z <= 7; z++ {
					d.w.SetBlock(world.Pos{X: x, Y: 71, Z: z}, diamond)
				}
			}
			data.rotation = int32(rotation)
			if !s.testInstancePlace(d, pos, data) {
				t.Fatal("missing registered template")
			}
			if d.w.GetBlock(points[0]) != gold || d.w.GetBlock(points[1]) != stone {
				t.Fatalf("rotation%d contents", rotation)
			}
			holes := []world.Pos{{X: 4, Y: 71, Z: 6}, {X: 5, Y: 71, Z: 5}, {X: 5, Y: 71, Z: 6}, {X: 5, Y: 71, Z: 6}}
			if d.w.GetBlock(holes[rotation]) != world.Air {
				t.Fatalf("rotation%d omitted cell not cleared", rotation)
			}
		}
	})
}

func TestTestInstanceDiscardRespectsPresenceCapability(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	d := s.dims[dimOverworld]
	neutral := s.entities.Spawn(&Entity{Type: mustEntityType("armor_stand"), Dim: d.name, X: 4.5, Y: 71, Z: 5.5, presenceOnly: true})
	ordinary := s.entities.Spawn(&Entity{Type: mustEntityType("armor_stand"), Dim: d.name, X: 4.5, Y: 71, Z: 5.5})
	player := s.entities.Spawn(&Entity{Type: mustEntityType("player"), Dim: d.name, X: 4.5, Y: 71, Z: 5.5})
	s.simulationAction(func() {
		s.testInstanceDiscardEntities(d, world.Pos{X: 4, Y: 71, Z: 5}, world.Pos{X: 5, Y: 72, Z: 7}, 0)
	})
	if _, ok := s.entities.Get(neutral.ID); !ok {
		t.Fatal("nonplayer presence-only incarnation discarded")
	}
	if _, ok := s.entities.Get(player.ID); !ok {
		t.Fatal("Java player discarded")
	}
	if _, ok := s.entities.Get(ordinary.ID); ok {
		t.Fatal("ordinary nonplayer not discarded")
	}
}

func TestTestInstanceCaptureRespectsPresenceCapability(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	d := s.dims[dimOverworld]
	s.entities.Spawn(&Entity{Type: mustEntityType("armor_stand"), Dim: d.name, X: 4.5, Y: 71, Z: 5.5, presenceOnly: true})
	s.entities.Spawn(&Entity{Type: mustEntityType("armor_stand"), Dim: d.name, X: 4.5, Y: 71, Z: 5.5})
	s.entities.Spawn(&Entity{Type: mustEntityType("player"), Dim: d.name, X: 4.5, Y: 71, Z: 5.5})
	s.simulationAction(func() {
		native := s.structureTemplateCapture(d, world.Pos{X: 4, Y: 71, Z: 5}, [3]int32{2, 2, 3}, true, "")
		entities, _ := native["entities"].(nbt.List)
		if len(entities.Items) != 1 {
			t.Fatalf("capture count%d want1 ordinary entity", len(entities.Items))
		}
		entry, _ := entities.Items[0].(map[string]any)
		record, _ := entry["nbt"].(map[string]any)
		if record["id"] != "minecraft:armor_stand" {
			t.Fatal("ordinary control missing", record)
		}
	})
}
