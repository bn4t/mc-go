package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestTestBlockActivationSurvivesNativeMergeButNotSave(t *testing.T) {
	s := New("unused")
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 4, Y: 71, Z: 5}
	state, _ := world.BlockStateOf("test_block", map[string]string{"mode": "accept"})
	d.w.SetBlock(pos, state)
	g := s.testBlockAt(d, pos)
	s.testBlockNeighborPower(d, pos, true)
	if !g.testTriggered || !nbtTruth(g.fields["powered"]) {
		t.Fatal("rising edge did not activate")
	}
	native, ok := s.blockEntitySaveMap(d, pos)
	if !ok {
		t.Fatal("native record missing")
	}
	if _, ok := native["triggered"]; ok {
		t.Fatal("transient flag persisted")
	}
	native["message"] = "edited"
	s.restoreBlockEntity(d, native)
	if s.testBlockAt(d, pos) != g || !g.testTriggered || g.fields["message"] != "edited" {
		t.Fatal("native merge discarded live activation")
	}
	s.testBlockNeighborPower(d, pos, false)
	if !g.testTriggered || nbtTruth(g.fields["powered"]) {
		t.Fatal("falling edge erased unconsumed activation")
	}
	restored := New("unused")
	restored.dims[dimOverworld].w.SetBlock(pos, state)
	restored.restoreBlockEntity(restored.dims[dimOverworld], native)
	if restored.testBlockAt(restored.dims[dimOverworld], pos).testTriggered {
		t.Fatal("native save restored transient activation")
	}
}

func TestTestBlockStartPowersAdjacentReceiver(t *testing.T) {
	s := New("unused")
	d := s.dims[dimOverworld]
	start, accept := world.Pos{X: 4, Y: 71, Z: 5}, world.Pos{X: 5, Y: 71, Z: 5}
	for p, mode := range map[world.Pos]string{start: "start", accept: "accept"} {
		st, _ := world.BlockStateOf("test_block", map[string]string{"mode": mode})
		d.w.SetBlock(p, st)
		g := s.testBlockAt(d, p)
		g.fields["mode"] = mode
	}
	s.testBlockTrigger(d, start, s.testBlockAt(d, start))
	if sig := rsGetSignal(d, start, rsWest); sig != 15 {
		t.Fatalf("start signal %d", sig)
	}
	if g := s.testBlockAt(d, accept); !g.testTriggered || !nbtTruth(g.fields["powered"]) {
		t.Fatalf("receiver %#v, triggered %v", g.fields, g.testTriggered)
	}
}

func TestTestBlockRunnerAdjacentAccept(t *testing.T) {
	s := New("unused")
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 4, Y: 70, Z: 4}
	controllerState, _ := world.BlockState("test_instance_block")
	d.w.SetBlock(pos, controllerState)
	g := s.testInstanceAt(d, pos)
	data := testInstanceData{size: [3]int32{2, 1, 2}}
	g.fields["data"] = data.native()
	for p, mode := range map[world.Pos]string{{X: 4, Y: 71, Z: 5}: "start", {X: 5, Y: 71, Z: 5}: "accept"} {
		st, _ := world.BlockStateOf("test_block", map[string]string{"mode": mode})
		d.w.SetBlock(p, st)
		s.testBlockAt(d, p)
	}
	run := &testInstanceRun{d: d, pos: pos, controller: g}
	s.testRun = run
	s.testInstanceBeginBlocks(run)
	if s.testRun != nil {
		t.Fatalf("runner not finished; starts %v accepts %v failure %#v", s.testInstanceBlocks(run, "start"), s.testInstanceBlocks(run, "accept"), run.failure)
	}
}
