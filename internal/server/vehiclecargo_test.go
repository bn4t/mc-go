package server

import (
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func cargoUnitEntity(s *Server, name string) *Entity {
	e := s.entities.Spawn(&Entity{Type: mustEntityType(name), Dim: dimOverworld, X: 1.5, Y: 0, Z: 1.5})
	save := entitySaveMap(e, 0, s.entities).m
	save["Items"] = nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"Slot": int8(0), "id": "minecraft:diamond", "count": int32(3)}}}
	s.applyEntitySave(e, save)
	return e
}

func TestVehicleCargoSharedReloadAndNative(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	first, _ := enforceSession(t, s, "CargoFirst")
	second, _ := enforceSession(t, s, "CargoSecond")
	first.setPos(1.5, 0, 0, 0, 0, true)
	second.setPos(1.5, 0, 0, 0, 0, true)
	e := cargoUnitEntity(s, "chest_minecart")
	if !s.openVehicleCargo(first, e) || !s.openVehicleCargo(second, e) {
		t.Fatal("cargo menu did not open")
	}
	storage := e.vehicleCargo
	if first.window.container != storage || second.window.container != storage || first.window.containerSize() != 27 || first.window.kind != menuGeneric9x3 {
		t.Fatal("cargo viewers did not share the27-slot inventory")
	}
	if _, matches := first.window.blockMenuSlot(world.Pos{}, 0); matches {
		t.Fatal("vehicle cargo aliased a block menu at the zero position")
	}
	appleID, _ := world.ItemID("apple")
	apple := ItemStack{ID: appleID, Count: 2}
	first.setMenuStack(0, apple)
	if second.menuStack(0) != apple || second.window.commandUpdates == nil {
		t.Fatal("other viewer did not observe the shared slot mutation")
	}
	s.flushMenuCommandUpdates(second)
	save := entitySaveMap(e, 0, s.entities).m
	slots := make([]ItemStack, 27)
	itemsInto(save["Items"], slots)
	if slots[0] != apple {
		t.Fatal("native projection did not see a menu mutation")
	}
	save["Items"] = nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"Slot": int8(0), "id": "minecraft:diamond", "count": int32(5)}}}
	s.applyEntitySave(e, save)
	if e.vehicleCargo != storage || first.window.container != storage || second.menuStack(0).Count != 5 {
		t.Fatal("data reload replaced viewed cargo or failed to reload its slots")
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	reloaded := nativeSaveTestServer(t)
	reloaded.WorldDir = s.WorldDir
	if err := reloaded.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	for _, copy := range reloaded.entities.All() {
		if copy.Type == mustEntityType("chest_minecart") {
			cart, _ := reloaded.entities.GetPtr(copy.ID)
			if reloaded.vehicleCargoInventory(cart).slots[0].Count != 5 {
				t.Fatal("viewed cargo mutation lost on native restart")
			}
			return
		}
	}
	t.Fatal("cargo vehicle missing after native restart")
}

func TestVehicleCargoLootAndSpectator(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	player, _ := enforceSession(t, s, "CargoSpectator")
	player.setPos(1.5, 0, 0, 0, 0, true)
	e := cargoUnitEntity(s, "hopper_minecart")
	save := entitySaveMap(e, 0, s.entities).m
	save["LootTable"] = "minecraft:empty"
	save["LootTableSeed"] = int64(123)
	delete(save, "Items")
	s.applyEntitySave(e, save)
	player.setMode(GameModeSpectator)
	if s.openVehicleCargo(player, e) || e.vehicleCargo.lootTable != "minecraft:empty" || player.window != nil {
		t.Fatal("spectator generated armed cargo loot")
	}
	player.setMode(GameModeSurvival)
	if !s.openVehicleCargo(player, e) || player.window.containerSize() != 5 || player.window.kind != menuHopper {
		t.Fatal("hopper cargo menu shape differs")
	}
	projected := entitySaveMap(e, 0, s.entities).m
	if projected["LootTable"] != nil || projected["LootTableSeed"] != nil || len(projected["Items"].(nbt.List).Items) != 0 {
		t.Fatal("empty loot was not consumed/projected correctly")
	}
	diamondID, _ := world.ItemID("diamond")
	player.setMenuStack(0, ItemStack{ID: diamondID, Count: 3})
	player.setMode(GameModeSpectator)
	var packet protocol.Buffer
	packet.VarInt(player.window.id)
	packet.VarInt(player.windowState)
	packet.Short(0)
	packet.Byte(0)
	packet.VarInt(0)
	packet.VarInt(0)
	packet.VarInt(0)
	s.handleClickContainer(player, protocol.Packet{Data: packet.Bytes()})
	if player.menuStack(0).Count != 3 || !player.carriedBuffer().Empty() {
		t.Fatal("spectator removed a cargo stack")
	}
}

func TestVehicleCargoRangeAndRemoval(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	player, _ := enforceSession(t, s, "CargoRange")
	e := cargoUnitEntity(s, "chest_minecart")
	e.X, e.Y, e.Z = 6.5, -60, 5.5
	player.setMode(GameModeSurvival)
	player.setPos(6.5, -60, 2, 0, 0, true)
	s.openVehicleCargo(player, e)
	w := player.window
	for _, tc := range []struct {
		mode  byte
		z     float64
		valid bool
	}{{GameModeSurvival, -1.90, true}, {GameModeSurvival, -1.96, false}, {GameModeCreative, -3.91, true}, {GameModeCreative, -3.98, false}} {
		player.setMode(tc.mode)
		player.setPos(6.5, -60, tc.z, 0, 0, true)
		if got := s.vehicleCargoValid(player, w); got != tc.valid {
			t.Errorf("mode%d z%v validity%v want%v", tc.mode, tc.z, got, tc.valid)
		}
	}
	player.setPos(6.5, -60, 2, 0, 0, true)
	s.entities.Remove(e.ID)
	s.validateVehicleCargo(player)
	if player.window != nil {
		t.Fatal("removed cargo vehicle retained its menu")
	}
}
