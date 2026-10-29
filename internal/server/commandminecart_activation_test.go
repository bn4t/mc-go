package server

import "testing"

func TestCommandMinecartActivationNativeRestart(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	s.runCommandAt("RCON", 4, "scoreboard", []string{"objectives", "add", "mc", "dummy"})
	s.runCommandAt("RCON", 4, "scoreboard", []string{"players", "set", "runs", "mc", "0"})
	s.runSetblock("P", []string{"1", "0", "1", "redstone_block"})
	s.runSetblock("P", []string{"1", "1", "1", "activator_rail[shape=north_south]"})
	e := s.entities.Spawn(&Entity{Type: mustEntityType("command_block_minecart"), Dim: dimOverworld, X: 1.5, Y: 1, Z: 1.5})
	save := entitySaveMap(e, s.gameTime(), s.entities).m
	save["Command"] = "execute if entity @s[type=minecraft:command_block_minecart] run scoreboard players add runs mc 1"
	save["TrackOutput"] = int8(0)
	s.applyEntitySave(e, save)
	s.StepTicks(3)
	if commandMinecartFields(e)["LastExecution"] != nil {
		t.Fatal("cart activated before its fourth entity tick")
	}
	s.StepTicks(1)
	if commandMinecartFields(e)["SuccessCount"] != int32(1) || commandMinecartFields(e)["LastExecution"] != int64(4) {
		t.Fatalf("fourth tick=%#v", commandMinecartFields(e))
	}
	s.StepTicks(4)
	if commandMinecartFields(e)["LastExecution"] != int64(8) {
		t.Fatal("cart did not repeat after four entity ticks")
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
	restarted.runCommandAt("RCON", 4, "scoreboard", []string{"objectives", "add", "mc", "dummy"})
	restarted.runCommandAt("RCON", 4, "scoreboard", []string{"players", "set", "runs", "mc", "0"})
	var cart *Entity
	for _, copy := range restarted.entities.All() {
		if copy.Type == mustEntityType("command_block_minecart") {
			cart, _ = restarted.entities.GetPtr(copy.ID)
		}
	}
	if cart == nil || commandMinecartFields(cart)["LastExecution"] != int64(8) {
		t.Fatal("executed cart state did not survive native load")
	}
	restarted.StepTicks(3)
	if commandMinecartFields(cart)["LastExecution"] != int64(8) {
		t.Fatal("runtime activation cooldown did not reset on native load")
	}
	restarted.StepTicks(1)
	if commandMinecartFields(cart)["SuccessCount"] != int32(1) || commandMinecartFields(cart)["LastExecution"] != int64(12) {
		t.Fatalf("restored activation=%#v", commandMinecartFields(cart))
	}
}
