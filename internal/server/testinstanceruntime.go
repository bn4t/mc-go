package server

import "mc-go/internal/world"

// The built-in function completes at test tick zero. Its setup interval is
// counted by world ticks, including the initial two runner scheduling ticks;
// frozen player/connection ticks never advance it.
type testInstanceRun struct {
	d          *dimension
	pos        world.Pos
	controller *genericBlockEntity
	function   string
	remaining  int32
}

func (s *Server) testInstanceStart(sess *Session, d *dimension, pos world.Pos, g *genericBlockEntity, data testInstanceData) {
	def := testInstanceDefinition(data.test)
	if def == nil {
		s.testInstanceFeedback(sess, "test_instance_block.error.no_test", pos)
		return
	}
	if !s.testInstancePlace(d, pos, data) {
		s.testInstanceFeedback(sess, "test_instance_block.error.no_test_structure", pos)
		return
	}
	delete(g.fields, "errors")
	s.sendSystemChatComponent(sess, translatable("test_instance_block.starting", data.test))
	// Runner preparation reconstructs the controller from its registered test
	// and template, rather than preserving the editor's size/error/entity flag.
	template, _ := def["structure"].(string)
	data.size = structureVector(s.structureTemplateGet(template)["size"])
	data.status = 0
	data.ignoreEntities = false
	data.errorMessage = nil
	g.fields["data"] = data.native()
	s.testInstancePlace(d, pos, data)
	barrier, _ := world.BlockState("barrier")
	s.testInstanceBoundary(d, pos, data, func(p world.Pos) {
		spec, ok := blockEntityOf(d.w.GetBlock(p))
		if !ok || spec.name != testInstanceBlockEntityID {
			s.setSilentBlock(d, p, barrier)
		}
	})
	// Starting another run retires the prior server-wide runner.
	function, _ := def["function"].(string)
	s.testRun = &testInstanceRun{d: d, pos: pos, controller: g, function: function, remaining: nbtNumberOr(def["setup_ticks"], 0) + 2}
	environment, _ := def["environment"].(string)
	s.sendOpsEcho(nil, translatable("commands.test.batch.starting", literal(environment), nestedCompound{value: int32(0)}))
}

func (s *Server) testInstanceTickStep() {
	run := s.testRun
	if run == nil {
		return
	}
	run.remaining--
	if run.remaining > 0 {
		return
	}

	// An editor can change the selected definition while a run is active. The
	// executing function stays fixed, while completion updates the controller
	// currently occupying the original position (including a replacement).
	if run.function == "minecraft:always_pass" {
		original, ok := testInstanceDataFromNative(run.controller.fields["data"])
		if ok {
			low, high, _ := testInstanceFootprint(run.pos, original)
			s.testInstanceDiscardEntities(run.d, low, high, 1)
			s.testInstanceRemoveBarriers(run.d, run.pos, original)
		}
		if current := s.testInstanceAt(run.d, run.pos); current != nil {
			if data, ok := testInstanceDataFromNative(current.fields["data"]); ok {
				data.status = 2
				data.errorMessage = nil
				current.fields["data"] = data.native()
				s.testInstanceDirty(run.d, run.pos)
			}
		}
	}
	s.testRun = nil
}
