package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestChestStorageFamilyNativePersistence(t *testing.T) {
	for _, name := range []string{"chest", "trapped_chest", "copper_chest", "waxed_weathered_copper_chest"} {
		t.Run(name, func(t *testing.T) {
			s := nativeSaveTestServer(t)
			s.SetFrozen(true)
			pos := world.Pos{X: 1, Y: 0, Z: 1}
			s.runSetblock("P", []string{"1", "0", "1", name + `{CustomName:"Cargo",Items:[{Slot:0b,id:"minecraft:diamond",count:7}]}`})
			assertStorage := func(core *Server, phase string) {
				d := core.dims[dimOverworld]
				c := core.peekChest(d, pos)
				if c == nil || c.slots[0].ID != world.ItemIDs["diamond"] || c.slots[0].Count != 7 || d.beNames[pos] != "Cargo" {
					t.Fatalf("%s %s stored=%v name=%q", name, phase, c != nil, d.beNames[pos])
				}
				fields, ok := core.blockEntitySaveMap(d, pos)
				if !ok || fields["CustomName"] != "Cargo" {
					t.Fatalf("saved fields=%#v", fields)
				}
			}
			assertStorage(s, "before save")
			if err := s.SaveWorld(); err != nil {
				t.Fatal(err)
			}
			restored := nativeSaveTestServer(t)
			restored.WorldDir = s.WorldDir
			if err := restored.LoadWorld(); err != nil {
				t.Fatal(err)
			}
			assertStorage(restored, "after reload")
		})
	}
}
