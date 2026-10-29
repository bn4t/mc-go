package server

import (
	"fmt"
	"mc-go/internal/world"
)

// The built-in function completes at test tick zero. Its setup interval is
// counted by world ticks, including the initial two runner scheduling ticks;
// frozen player/connection ticks never advance it.
type testInstanceRun struct {
	d                 *dimension
	pos               world.Pos
	controller        *genericBlockEntity
	function          string
	remaining         int32
	kind              string
	maxTicks, elapsed int32
	started           bool
	failure           any
}

func (s *Server) testInstanceStart(sess *Session, d *dimension, pos world.Pos, g *genericBlockEntity, data testInstanceData) {
	def := s.testInstanceDefinition(data.test)
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
	kind, _ := def["type"].(string)
	s.testRun = &testInstanceRun{d: d, pos: pos, controller: g, kind: kind, function: function, maxTicks: nbtNumberOr(def["max_ticks"], 1), remaining: nbtNumberOr(def["setup_ticks"], 0) + 2}
	environment, _ := def["environment"].(string)
	s.sendOpsEcho(nil, translatable("commands.test.batch.starting", literal(environment), nestedCompound{value: int32(0)}))
}

func (s *Server) testInstanceTickStep() {
	run := s.testRun
	if run == nil {
		return
	}
	// Failures are published on the next world tick; a successful test can
	// finish in the same tick that consumes its accepting signal.
	if run.failure != nil {
		s.testInstanceComplete(run, run.failure)
		return
	}
	if !run.started {
		run.remaining--
		if run.remaining > 0 {
			return
		}
		run.started = true
		if data, ok := testInstanceDataFromNative(run.controller.fields["data"]); ok {
			data.status = 1
			data.errorMessage = nil
			run.controller.fields["data"] = data.native()
			s.testInstanceDirty(run.d, run.pos)
		}
		switch run.kind {
		case "minecraft:function":
			if run.function == "minecraft:always_pass" {
				s.testInstanceSucceed(run)
			} else {
				run.failure = "Trying to access missing test function: " + run.function
			}
		case "minecraft:block_based":
			s.testInstanceBeginBlocks(run)
		}
		return
	}
	run.elapsed++
	if run.elapsed > run.maxTicks {
		run.failure = fmt.Sprintf("Didn't succeed or fail within %d ticks", run.maxTicks)
		return
	}
	if run.kind == "minecraft:block_based" {
		s.testInstancePollBlocks(run)
	}
}

func (s *Server) testInstanceSucceed(run *testInstanceRun) {
	original, ok := testInstanceDataFromNative(run.controller.fields["data"])
	if ok {
		low, high, _ := testInstanceFootprint(run.pos, original)
		s.testInstanceDiscardEntities(run.d, low, high, 1)
		s.testInstanceRemoveBarriers(run.d, run.pos, original)
	}
	s.testInstanceComplete(run, nil)
}
func (s *Server) testInstanceComplete(run *testInstanceRun, failure any) {
	if current := s.testInstanceAt(run.d, run.pos); current != nil {
		if data, ok := testInstanceDataFromNative(current.fields["data"]); ok {
			data.status = 2
			data.errorMessage = failure
			current.fields["data"] = data.native()
			s.testInstanceDirty(run.d, run.pos)
		}
	}
	if s.testRun == run {
		s.testRun = nil
	}
}
