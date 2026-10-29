package server

import (
	"reflect"
	"strconv"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

func TestVehicleNativeCargoAndState(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	enforceSession(t, s, "P")
	cases := []struct {
		name string
		tag  string
		want map[string]any
	}{
		{"oak_chest_boat", `{NoGravity:1b,CustomName:"Cargo",Items:[{Slot:0b,id:"minecraft:diamond",count:3},{Slot:26b,id:"minecraft:apple",count:2},{Slot:27b,id:"minecraft:stone",count:9}]}`, map[string]any{"CustomName": "Cargo", "NoGravity": int8(1)}},
		{"hopper_minecart", `{NoGravity:1b,Enabled:0b,Items:[{Slot:4b,id:"minecraft:apple",count:2},{Slot:5b,id:"minecraft:stone",count:3}]}`, map[string]any{"Enabled": int8(0)}},
		{"furnace_minecart", `{NoGravity:1b,Fuel:123s,PushX:0.25d,PushZ:-0.5d}`, map[string]any{"Fuel": int16(123), "PushX": float64(.25), "PushZ": float64(-.5)}},
		{"tnt_minecart", `{NoGravity:1b,fuse:40}`, map[string]any{"fuse": int32(40)}},
		{"minecart", `{NoGravity:1b,HasTicked:1b,FlippedRotation:1b}`, map[string]any{"HasTicked": int8(1), "FlippedRotation": int8(1)}},
	}
	for i, c := range cases {
		s.runSummon("P", []string{c.name, "1", "0", strconv.Itoa(i + 1), c.tag})
	}
	before := map[string]map[string]any{}
	for _, e := range s.entities.All() {
		name, _ := EntityTypeName(e.Type)
		before[name] = entitySaveMap(&e, 0, s.entities).m
	}
	for _, c := range cases {
		for k, v := range c.want {
			if !reflect.DeepEqual(before[c.name][k], v) {
				t.Fatalf("%s %s=%#v want %#v", c.name, k, before[c.name][k], v)
			}
		}
	}
	for name, want := range map[string]int{"oak_chest_boat": 2, "hopper_minecart": 1} {
		if got := len(before[name]["Items"].(nbt.List).Items); got != want {
			t.Fatalf("%s cargo count=%d", name, got)
		}
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	restarted.SetFrozen(true)
	seen := 0
	for _, e := range restarted.entities.All() {
		name, _ := EntityTypeName(e.Type)
		old, ok := before[name]
		if !ok {
			continue
		}
		seen++
		after := entitySaveMap(&e, 0, restarted.entities).m
		for k, v := range old {
			if !reflect.DeepEqual(after[k], v) {
				t.Errorf("%s after reload %s=%#v want %#v", name, k, after[k], v)
			}
		}
	}
	if seen != len(cases) {
		t.Fatalf("restored%d vehicles want%d", seen, len(cases))
	}
	restarted.StepTicks(1)
	for _, e := range restarted.entities.All() {
		if name, _ := EntityTypeName(e.Type); name == "minecart" && entitySaveMap(&e, 0, restarted.entities).m["HasTicked"] != int8(0) {
			t.Fatal("loaded HasTicked flag did not clear at the next minecart tick")
		}
	}
}

func TestVehicleNativePassengerTree(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	rider := map[string]any{"id": "minecraft:pig", "UUID": []int32{1, 2, 3, 4}, "Pos": nbt.List{Elem: nbt.TagDouble, Items: []any{1.5, 0.0, 1.5}}, "NoAI": int8(1), "Tags": nbt.List{Elem: nbt.TagString, Items: []any{"rider"}}}
	boat := map[string]any{"id": "minecraft:oak_boat", "UUID": []int32{5, 6, 7, 8}, "Pos": nbt.List{Elem: nbt.TagDouble, Items: []any{1.5, 0.0, 1.5}}, "NoGravity": int8(1), "Passengers": nbt.List{Elem: nbt.TagCompound, Items: []any{rider}}}
	dir := world.DimensionDir(s.WorldDir, dimOverworld)
	if err := world.SaveEntityRegions(dir, map[[2]int32][]map[string]any{{0, 0}: {boat}}); err != nil {
		t.Fatal(err)
	}
	reloaded := nativeSaveTestServer(t)
	reloaded.WorldDir = s.WorldDir
	if err := reloaded.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	reloaded.SetFrozen(true)
	var vehicle, passenger *Entity
	for _, copy := range reloaded.entities.All() {
		e, _ := reloaded.entities.GetPtr(copy.ID)
		name, _ := EntityTypeName(e.Type)
		if name == "oak_boat" {
			vehicle = e
		} else if name == "pig" {
			passenger = e
		}
	}
	if vehicle == nil || passenger == nil || passenger.Riding != vehicle.ID || len(vehicle.Passengers) != 1 || vehicle.Passengers[0] != passenger.ID || passenger.UUID != uuidFromInts([]int32{1, 2, 3, 4}) || !passenger.NoAI {
		t.Fatal("native passenger hierarchy or identity was lost")
	}
	if err := reloaded.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	list, err := world.LoadEntityRegions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("root count=%d want1", len(list))
	}
	nested, ok := list[0]["Passengers"].(nbt.List)
	if !ok || len(nested.Items) != 1 {
		t.Fatalf("passenger list=%#v", list[0]["Passengers"])
	}

	// Removing the final rider must not leave an input Passengers compound
	// available for a later native reload to resurrect.
	vehicle.Passengers = nil
	reloaded.entities.Remove(passenger.ID)
	if err := reloaded.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	list, err = world.LoadEntityRegions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0]["Passengers"] != nil {
		t.Fatalf("removed rider resurrected in native save: %#v", list)
	}
}

func TestVehicleNativeUnsupportedParentKeepsChildRoot(t *testing.T) {
	for _, kind := range []string{"ender_dragon", "command_block_minecart", "spawner_minecart", "area_effect_cloud", "unknown", "unlisted", "pig"} {
		t.Run(kind, func(t *testing.T) {
			s := nativeSaveTestServer(t)
			s.SetFrozen(true)
			id := int32(-1)
			if kind != "unknown" {
				name := kind
				if kind == "unlisted" {
					name = "oak_boat"
				}
				id = mustEntityType(name)
			}
			parent := s.entities.Spawn(&Entity{Type: id, Dim: dimOverworld, X: 1, Y: 0, Z: 1, Living: kind == "ender_dragon" || kind == "pig" || kind == "unknown"})
			child, ok := s.summonMobRaw(s.dims[dimOverworld], "pig", 1, 0, 1, true, false)
			if !ok {
				t.Fatal("pig")
			}
			child.Riding = parent.ID
			if kind != "unlisted" {
				parent.Passengers = []int32{child.ID}
			}
			groups := s.captureEntityRegionsOwned(s.dims[dimOverworld])
			found := false
			for _, list := range groups {
				for _, save := range list {
					if reflect.DeepEqual(save["UUID"], uuidInts(child.UUID)) {
						found = true
					}
				}
			}
			// A normal living parent owns its one nested rider; unsupported or
			// special branches and inconsistent membership preserve a child root.
			if found == (kind == "pig") {
				t.Fatalf("child root=%v for parent%s", found, kind)
			}
		})
	}
}

func TestVehicleNativeInvalidIdentifiers(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	for _, name := range []string{"test:bogus_boat", "foo_minecart"} {
		if ordinaryVehicle(name) {
			t.Fatalf("unknown identifier %q accepted", name)
		}
		if e := s.restoreVehicleNative(s.dims[dimOverworld], name, nil, [3]float64{}, [16]byte{}); e != nil {
			t.Fatal("invalid native vehicle constructed")
		}
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	pos := nbt.List{Elem: nbt.TagDouble, Items: []any{1.5, 0.0, 1.5}}
	roots := []map[string]any{
		{"id": "test:bogus_boat", "Pos": pos},
		{"id": "minecraft:foo_minecart", "Pos": pos},
		{"id": "minecraft:oak_boat", "Pos": pos, "Passengers": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"id": "test:bogus_boat", "Pos": pos}, map[string]any{"id": "minecraft:foo_minecart", "Pos": pos}, map[string]any{"id": "minecraft:pig", "Pos": pos, "NoAI": int8(1)}}}},
	}
	if err := world.SaveEntityRegions(world.DimensionDir(s.WorldDir, dimOverworld), map[[2]int32][]map[string]any{{0, 0}: roots}); err != nil {
		t.Fatal(err)
	}
	reload := nativeSaveTestServer(t)
	reload.WorldDir = s.WorldDir
	if err := reload.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, e := range reload.entities.All() {
		name, _ := EntityTypeName(e.Type)
		counts[name]++
	}
	if counts["oak_boat"] != 1 || counts["pig"] != 1 || len(counts) != 2 {
		t.Fatalf("invalid roots/passengers affected valid siblings: %#v", counts)
	}
}

func TestVehicleNativeDeepPassengerTree(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	pos := nbt.List{Elem: nbt.TagDouble, Items: []any{1.5, 0.0, 1.5}}
	root := map[string]any{"id": "minecraft:oak_boat", "Pos": pos, "NoGravity": int8(1)}
	current := root
	const descendants = 80
	for i := 0; i < descendants; i++ {
		child := map[string]any{"id": "minecraft:pig", "Pos": pos, "NoAI": int8(1)}
		current["Passengers"] = nbt.List{Elem: nbt.TagCompound, Items: []any{child}}
		current = child
	}
	s.restoreVehicleNative(s.dims[dimOverworld], "oak_boat", root, [3]float64{1.5, 0, 1.5}, [16]byte{})
	if got := len(s.entities.All()); got != descendants+1 {
		t.Fatalf("initial tree count=%d want%d", got, descendants+1)
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	if got := len(restarted.entities.All()); got != descendants+1 {
		t.Fatalf("deep native tree lost children: %d want%d", got, descendants+1)
	}
	for _, e := range restarted.entities.All() {
		if e.Riding != 0 {
			parent, ok := restarted.entities.GetPtr(e.Riding)
			if !ok || !nativePassengerListed(parent, e.ID) {
				t.Fatal("deep rider lost parent ordering/membership")
			}
		}
	}
}

func TestVehicleNativeSpecialChildStaysWithItsOwnLoader(t *testing.T) {
	s := nativeSaveTestServer(t)
	child := map[string]any{"id": "minecraft:ender_dragon", "Pos": nbt.List{Elem: nbt.TagDouble, Items: []any{1.5, 0.0, 1.5}}}
	root := map[string]any{"Passengers": nbt.List{Elem: nbt.TagCompound, Items: []any{child}}}
	s.restoreVehicleNative(s.dims[dimOverworld], "oak_boat", root, [3]float64{1.5, 0, 1.5}, [16]byte{})
	if got := len(s.entities.All()); got != 1 {
		t.Fatalf("special child was duplicated through generic loader: count%d", got)
	}
}
