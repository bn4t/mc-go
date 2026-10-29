package server

import (
	"testing"

	"mc-go/internal/world"
)

func TestCommandBlockNativePersistence(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	pos := world.Pos{X: 1, Y: 0, Z: 1}
	s.runSetblock("P", []string{"1", "0", "1", `chain_command_block[facing=up]{Command:"say saved",SuccessCount:7,TrackOutput:0b,UpdateLastExecution:0b,LastExecution:123L,LastOutput:"hidden",CustomName:"Keeper",auto:1b}`})
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	fields, ok := restarted.blockEntitySaveMap(restarted.dims[dimOverworld], pos)
	if !ok || fields["Command"] != "say saved" || fields["SuccessCount"] != int32(7) || fields["auto"] != int8(1) || fields["CustomName"] != "Keeper" {
		t.Fatalf("command state did not survive native save: %#v", fields)
	}
	if _, ok := fields["LastOutput"]; ok {
		t.Fatal("disabled output tracking persisted LastOutput")
	}
	if _, ok := fields["LastExecution"]; ok {
		t.Fatal("disabled execution tracking persisted LastExecution")
	}
	state := restarted.dims[dimOverworld].w.GetBlock(pos)
	if facing, _ := world.StateProperty(state, "facing"); facing != "up" || !isBlockNamed(state, "chain_command_block") {
		t.Fatal("command block state did not survive native save")
	}
}

func TestCommandBlockExecutionNativePersistence(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	s.runCommandAt("RCON", 4, "scoreboard", []string{"objectives", "add", "cb", "dummy"})
	s.runSetblock("P", []string{"1", "0", "1", `repeating_command_block{Command:"scoreboard players add runs cb 1",auto:1b}`})
	s.StepTicks(3)
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	fields, ok := restarted.blockEntitySaveMap(restarted.dims[dimOverworld], world.Pos{X: 1, Y: 0, Z: 1})
	if !ok || fields["SuccessCount"] != int32(1) || fields["LastExecution"] != int64(3) || fields["auto"] != int8(1) {
		t.Fatalf("executed command state did not survive native save: %#v", fields)
	}
	if _, ok := fields["LastOutput"]; !ok {
		t.Fatal("executed command feedback did not survive native save")
	}
}
