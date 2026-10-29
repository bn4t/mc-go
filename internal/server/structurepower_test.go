package server

import (
	"os"
	"path/filepath"
	"testing"

	"mc-go/internal/world"
)

func TestStructurePowerEdgesAndRepositoryActions(t *testing.T) {
	s := simulationTestServer(t)
	s.WorldDir = t.TempDir()
	d := s.dims[dimOverworld]
	controller := world.Pos{X: 4, Y: -60, Z: 4}
	target := world.Pos{X: 4, Y: -59, Z: 4}
	signal := world.Pos{X: 5, Y: -60, Z: 4}
	s.simulationAction(func() {
		structure, _ := world.BlockStateOf("structure_block", map[string]string{"mode": "save"})
		stone, _ := world.BlockState("stone")
		dirt, _ := world.BlockState("dirt")
		redstone, _ := world.BlockState("redstone_block")
		s.setSilentBlock(d, controller, structure)
		s.setSilentBlock(d, target, stone)
		g := s.structureBlockAt(d, controller)
		g.fields["name"] = "test:power"
		g.fields["sizeX"], g.fields["sizeY"], g.fields["sizeZ"] = int32(1), int32(1), int32(1)
		setMode := func(mode string) { g.fields["mode"] = mode; s.structureBlockMode(d, controller, mode) }
		power := func(on bool) {
			state := world.Air
			if on {
				state = redstone
			}
			s.setBlockAndBroadcastInOwned(d, "", signal, state, true)
		}
		power(true)
		saved := s.structureTemplates["test:power"]
		if saved == nil || !nbtTruth(g.fields["powered"]) {
			t.Fatal("SAVE rising edge did not capture/latch")
		}
		if _, err := os.Stat(filepath.Join(s.WorldDir, "generated/test/structure/power.nbt")); !os.IsNotExist(err) {
			t.Fatalf("SAVE rising edge exported file: %v", err)
		}
		s.setSilentBlock(d, target, dirt)
		if structureVector(saved["size"]) != [3]int32{1, 1, 1} {
			t.Fatal("saved size changed")
		}
		power(false)
		if nbtTruth(g.fields["powered"]) {
			t.Fatal("falling edge retained latch")
		}
		setMode("LOAD")
		g.fields["sizeX"] = int32(0)
		power(true)
		if d.w.GetBlock(target) != stone || g.fields["sizeX"] != int32(1) {
			t.Fatal("LOAD did not immediately restore despite editor size mismatch")
		}
		s.setSilentBlock(d, target, dirt)
		s.rsNeighborChanged(d, controller)
		if d.w.GetBlock(target) != dirt {
			t.Fatal("steady high retriggered LOAD")
		}
		power(false)
		power(true)
		if d.w.GetBlock(target) != stone {
			t.Fatal("fresh edge did not retrigger LOAD")
		}
		power(false)
		setMode("DATA")
		s.setSilentBlock(d, target, dirt)
		power(true)
		if d.w.GetBlock(target) != dirt || !nbtTruth(g.fields["powered"]) {
			t.Fatal("DATA action or latch incorrect")
		}
		power(false)
		setMode("CORNER")
		power(true)
		if s.structureTemplates["test:power"] != nil {
			t.Fatal("CORNER retained cached template")
		}
		power(false)
		setMode("LOAD")
		power(true)
		if d.w.GetBlock(target) != dirt || !nbtTruth(g.fields["powered"]) {
			t.Fatal("missing LOAD changed blocks or failed to latch")
		}
	})
}
