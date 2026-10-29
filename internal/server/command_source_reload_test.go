package server

import (
	"testing"

	"mc-go/internal/world"
)

func TestCommandBlockOwnReloadLiveFields(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	pos := world.Pos{X: 1, Y: 0, Z: 1}
	s.runSetblock("P", []string{"1", "0", "1", `command_block{Command:"data merge block ~ ~ ~ {SuccessCount:999,TrackOutput:0b}"}`})
	original := s.commandBlockAt(s.dims[dimOverworld], pos)
	s.runSetblock("P", []string{"0", "0", "1", "redstone_block"})
	s.StepTicks(1)
	g := s.commandBlockAt(s.dims[dimOverworld], pos)
	if g != original {
		t.Fatal("data reload replaced the executing command block")
	}
	if g.fields["SuccessCount"] != int32(1000) || g.fields["LastExecution"] != int64(1) || g.fields["LastOutput"] == nil {
		t.Fatalf("live reload fields=%#v", g.fields)
	}
	saved, _ := s.blockEntitySaveMap(s.dims[dimOverworld], pos)
	if saved["LastOutput"] != nil {
		t.Fatal("disabled tracking exposed hidden runtime feedback")
	}
}

func TestCommandMinecartOwnReloadLiveFields(t *testing.T) {
	for _, initial := range []bool{false, true} {
		s := nativeSaveTestServer(t)
		s.SetFrozen(true)
		s.runSetblock("P", []string{"1", "0", "1", "redstone_block"})
		s.runSetblock("P", []string{"1", "1", "1", "activator_rail[shape=north_south]"})
		e := s.entities.Spawn(&Entity{Type: mustEntityType("command_block_minecart"), Dim: dimOverworld, X: 1.5, Y: 1, Z: 1.5})
		save := entitySaveMap(e, s.gameTime(), s.entities).m
		track := 0
		if !initial {
			track = 1
		}
		save["Command"] = "data merge entity @s {SuccessCount:999,TrackOutput:" + []string{"0b", "1b"}[track] + "}"
		save["TrackOutput"] = nbtBool(initial)
		s.applyEntitySave(e, save)
		s.StepTicks(4)
		fields := commandMinecartFields(e)
		if fields["SuccessCount"] != int32(1000) || fields["LastExecution"] != int64(4) {
			t.Fatalf("initial tracking=%v live fields=%#v", initial, fields)
		}
		if (fields["LastOutput"] != nil) != initial {
			t.Fatalf("feedback eligibility changed with reloaded tracking: initial=%v fields=%#v", initial, fields)
		}
	}
}
