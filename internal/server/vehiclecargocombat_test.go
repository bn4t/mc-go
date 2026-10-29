package server

import (
	"mc-go/internal/world"
	"strconv"
	"testing"
)

func TestVehicleCargoCombatDropRules(t *testing.T) {
	for _, creative := range []bool{true, false} {
		for _, drops := range []bool{true, false} {
			s := nativeSaveTestServer(t)
			sess, _ := enforceSession(t, s, "CargoCombat")
			sess.setMode(GameModeSurvival)
			if creative {
				sess.setMode(GameModeCreative)
			}
			s.gameRules["minecraft:entity_drops"] = strconv.FormatBool(drops)
			e := cargoUnitEntity(s, "chest_minecart")
			s.vehicleHurt(sess, e, 10)
			if _, ok := s.entities.GetPtr(e.ID); ok {
				t.Fatal("vehicle survived")
			}
			diamond, _ := world.ItemID("diamond")
			cart, _ := world.ItemID("chest_minecart")
			contents, vehicles := int32(0), int32(0)
			for _, item := range s.entities.All() {
				if item.Type != mustEntityType("item") {
					continue
				}
				if item.Item.ID == diamond {
					contents += item.Item.Count
				}
				if item.Item.ID == cart {
					vehicles += item.Item.Count
				}
			}
			wantVehicle := int32(0)
			if !creative && drops {
				wantVehicle = 1
			}
			if contents != 3 || vehicles != wantVehicle {
				t.Fatalf("creative%v drops%v contents%d vehicles%d", creative, drops, contents, vehicles)
			}
		}
	}
}
