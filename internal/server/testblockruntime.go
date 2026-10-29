package server

import "mc-go/internal/world"

// Emission is tied to the block-state mode; edge handling uses the native BE
// mode. Loading either half does not silently reconcile the other half.
func testBlockSignal(d *dimension, pos world.Pos, state world.Block) int {
	mode, _ := world.StateProperty(state, "mode")
	if mode != "start" {
		return 0
	}
	if g := d.genericBE[pos]; g != nil && g.id == testBlockEntityID && nbtTruth(g.fields["powered"]) {
		return 15
	}
	return 0
}
func (s *Server) testBlockNeighborPower(d *dimension, pos world.Pos, powered bool) {
	g := s.testBlockAt(d, pos)
	if g == nil || g.fields["mode"] == "start" {
		return
	}
	before := nbtTruth(g.fields["powered"])
	if before == powered {
		return
	}
	g.fields["powered"] = nbtBool(powered)
	if powered {
		s.testBlockTrigger(d, pos, g)
	}
}
func (s *Server) testBlockTrigger(d *dimension, pos world.Pos, g *genericBlockEntity) {
	if g.fields["mode"] == "start" {
		g.fields["powered"] = int8(1)
		s.rsNeighborFanOut(d, pos)
	}
	g.testTriggered = true
}

func (s *Server) testInstanceBlocks(run *testInstanceRun, mode string) []world.Pos {
	data, ok := testInstanceDataFromNative(run.controller.fields["data"])
	if !ok {
		return nil
	}
	_, _, origin := testInstanceFootprint(run.pos, data)
	var positions []world.Pos
	for z := int32(0); z < data.size[2]; z++ {
		for y := int32(0); y < data.size[1]; y++ {
			for x := int32(0); x < data.size[0]; x++ {
				rel := structureTransform([3]int32{x, y, z}, 0, data.rotation)
				p := world.Pos{X: origin.X + int(rel[0]), Y: origin.Y + int(rel[1]), Z: origin.Z + int(rel[2])}
				state := run.d.w.GetBlock(p)
				name, _ := world.StateOwner(state)
				value, _ := world.StateProperty(state, "mode")
				if name == "test_block" && value == mode {
					positions = append(positions, p)
				}
			}
		}
	}
	return positions
}
func testInstanceTickError(message component, tick int32) any {
	return translatable("test.error.tick", message, nestedCompound{value: tick}).nested()
}
func testInstanceMissingBlock(mode string, tick int32) any {
	return testInstanceTickError(translatable("test_block.error.missing", translatable("test_block.mode."+mode)), tick)
}

func (s *Server) testInstanceBeginBlocks(run *testInstanceRun) {
	positions := s.testInstanceBlocks(run, "start")
	if len(positions) == 0 {
		run.failure = testInstanceMissingBlock("start", 0)
		return
	}
	if len(positions) != 1 {
		run.failure = testInstanceTickError(translatable("test_block.error.too_many", translatable("test_block.mode.start")), 0)
		return
	}
	if g := s.testBlockAt(run.d, positions[0]); g != nil {
		s.testBlockTrigger(run.d, positions[0], g)
	}
	s.testInstancePollBlocks(run)
}
func (s *Server) testInstancePollBlocks(run *testInstanceRun) {
	accepted := s.testInstanceBlocks(run, "accept")
	if len(accepted) == 0 {
		run.failure = testInstanceMissingBlock("accept", run.elapsed+1)
		return
	}
	for _, pos := range accepted {
		if g := s.testBlockAt(run.d, pos); g != nil && g.testTriggered {
			s.testInstanceSucceed(run)
			return
		}
	}
	for _, pos := range s.testInstanceBlocks(run, "fail") {
		if g := s.testBlockAt(run.d, pos); g != nil && g.testTriggered {
			message, _ := g.fields["message"].(string)
			run.failure = testInstanceTickError(literal(message), run.elapsed+1)
			return
		}
	}
	for _, pos := range s.testInstanceBlocks(run, "log") {
		if g := s.testBlockAt(run.d, pos); g != nil && g.testTriggered {
			g.testTriggered = false
		}
	}
}
