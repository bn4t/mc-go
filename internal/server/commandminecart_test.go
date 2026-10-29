package server

import "testing"

func TestCommandMinecartSyncedCallbacks(t *testing.T) {
	s, _ := randSim(t)
	e := s.entities.Spawn(&Entity{Type: mustEntityType("command_block_minecart"), Dim: dimOverworld})
	save := entitySaveMap(e, s.gameTime(), s.entities).m
	save["Command"] = "say seed"
	save["SuccessCount"] = int32(7)
	save["LastOutput"] = "old"
	s.applyEntitySave(e, save)
	fields := commandMinecartFields(e)
	if fields["SuccessCount"] != int32(0) || fields["LastOutput"] != "old" {
		t.Fatalf("load callbacks=%#v", fields)
	}
	fields["Command"] = "say quiet"
	fields["TrackOutput"] = int8(0)
	delete(fields, "LastOutput")
	commandMinecartUpdated(e)
	if got := entitySaveMap(e, s.gameTime(), s.entities).m; got["LastOutput"] != nil {
		t.Fatal("disabled tracking saved output")
	}
	fields["TrackOutput"] = int8(1)
	commandMinecartUpdated(e)
	if got := entitySaveMap(e, s.gameTime(), s.entities).m; got["LastOutput"] != "" {
		t.Fatalf("synced empty output was not retained: %#v", got["LastOutput"])
	}
	save = entitySaveMap(e, s.gameTime(), s.entities).m
	save["SuccessCount"] = int32(9)
	s.applyEntitySave(e, save)
	if fields = commandMinecartFields(e); fields["SuccessCount"] != int32(9) {
		t.Fatal("unchanged synced command reset count during load")
	}
	// A disabled edit can update the runtime base without updating cached data.
	fields["Command"] = "say disabled"
	if e.commandCart.command != "say quiet" {
		t.Fatal("runtime command aliased the cached metadata")
	}
}

func TestCommandMinecartNativePersistence(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	s.runSetblock("P", []string{"1", "0", "1", "minecraft:stone"})
	e := s.entities.Spawn(&Entity{Type: mustEntityType("command_block_minecart"), Dim: dimOverworld, X: 1.5, Y: 1, Z: 1.5})
	save := entitySaveMap(e, s.gameTime(), s.entities).m
	save["Command"] = "say saved"
	save["CustomName"] = "Keeper"
	save["TrackOutput"] = int8(0)
	save["UpdateLastExecution"] = int8(0)
	save["LastExecution"] = int64(123)
	save["LastOutput"] = "hidden"
	s.applyEntitySave(e, save)
	commandMinecartFields(e)["SuccessCount"] = int32(3)
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	var cart *Entity
	for _, copy := range restarted.entities.All() {
		if copy.Type == mustEntityType("command_block_minecart") {
			cart, _ = restarted.entities.GetPtr(copy.ID)
			break
		}
	}
	if cart == nil {
		t.Fatal("native command minecart missing")
	}
	got := entitySaveMap(cart, restarted.gameTime(), restarted.entities).m
	if got["Command"] != "say saved" || got["CustomName"] != "Keeper" || got["SuccessCount"] != int32(0) || got["TrackOutput"] != int8(0) {
		t.Fatalf("native command minecart fields=%#v", got)
	}
	if got["LastOutput"] != nil || got["LastExecution"] != nil {
		t.Fatal("disabled base fields survived native save")
	}
	commandMinecartFields(cart)["TrackOutput"] = int8(1)
	commandMinecartFields(cart)["UpdateLastExecution"] = int8(1)
	commandMinecartUpdated(cart)
	got = entitySaveMap(cart, restarted.gameTime(), restarted.entities).m
	if got["LastOutput"] != nil || got["LastExecution"] != nil {
		t.Fatal("hidden loaded fields were resurrected")
	}
}
