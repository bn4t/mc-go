package server

import (
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func (s *Server) testInstanceAt(d *dimension, pos world.Pos) *genericBlockEntity {
	if d == nil {
		return nil
	}
	spec, ok := blockEntityOf(d.w.GetBlock(pos))
	if !ok || spec.name != testInstanceBlockEntityID {
		return nil
	}
	if g := d.genericBE[pos]; g != nil && g.id == testInstanceBlockEntityID {
		return g
	}
	g := &genericBlockEntity{id: testInstanceBlockEntityID, fields: map[string]any{"data": (testInstanceData{}).native()}}
	if d.genericBE == nil {
		d.genericBE = map[world.Pos]*genericBlockEntity{}
	}
	d.genericBE[pos] = g
	return g
}
func (s *Server) testInstanceDirty(d *dimension, pos world.Pos) {
	s.broadcastBlockState(d, pos, d.w.GetBlock(pos))
	s.queueBlockEntityDirty(d, pos)
}
func (s *Server) testInstanceFeedback(sess *Session, key string, pos world.Pos) {
	c := translatable(key, int32(pos.X), int32(pos.Y), int32(pos.Z))
	c.color = "red"
	s.sendSystemChatComponent(sess, c)
}

func (s *Server) handleTestInstanceBlockAction(sess *Session, p protocol.Packet) {
	edit, ok := readTestInstanceEdit(p.Data)
	if !ok || sess.mode() != GameModeCreative || sess.opLevel() < 2 {
		return
	}
	d := sess.dimension()
	g := s.testInstanceAt(d, edit.pos)
	if g == nil {
		return
	}
	if edit.action == 0 || edit.action == 1 {
		// The default registry has no selected test definition. Registry-backed
		// descriptions and template setup are a separate runtime workflow.
		description := translatable("test_instance.description.no_test")
		description.color = "red"
		var b protocol.Buffer
		b.Raw(description.content())
		b.Bool(false)
		_ = sess.write(sess.ids().PlayTestInstanceStatus, b.Bytes())
		return
	}
	data := edit.data
	g.fields["data"] = data.native()
	defer s.testInstanceDirty(d, edit.pos)
	switch edit.action {
	case 2: // SET only changes the controller record.
	case 3:
		s.testInstanceRemoveBarriers(d, edit.pos, data)
		delete(g.fields, "errors")
		data.status = 0
		data.errorMessage = nil
		g.fields["data"] = data.native()
	case 4, 5:
		name := s.testInstanceSave(d, edit.pos, data)
		if name == "" {
			s.testInstanceFeedback(sess, "test_instance_block.error.unable_to_save", edit.pos)
			return
		}
		if edit.action == 5 {
			c := literal("Test structure exporting is disabled")
			c.color = "red"
			s.sendSystemChatComponent(sess, c)
		}
	case 6:
		s.testInstanceFeedback(sess, "test_instance_block.error.no_test", edit.pos)
	}
}

// Without a registry definition, SAVE still uses the selected resource key.
// AIR is omitted from game-test captures, so loading preserves those cells.
func (s *Server) testInstanceSave(d *dimension, pos world.Pos, data testInstanceData) string {
	if data.test == "" {
		return ""
	}
	if data.size[0] <= 0 || data.size[1] <= 0 || data.size[2] <= 0 {
		return data.test
	}
	origin := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z + 1}
	native := s.structureTemplateCapture(d, origin, data.size, !data.ignoreEntities, "")
	palette, _ := native["palette"].(nbt.List)
	blocks, _ := native["blocks"].(nbt.List)
	filtered := nbt.List{Elem: nbt.TagCompound}
	states := nbt.List{Elem: nbt.TagCompound}
	indices := map[int32]int32{}
	for _, value := range blocks.Items {
		block, ok := value.(map[string]any)
		if !ok {
			continue
		}
		old := nbtNumberOr(block["state"], -1)
		if old < 0 || int(old) >= len(palette.Items) {
			continue
		}
		state, _ := palette.Items[old].(map[string]any)
		if state["Name"] == "minecraft:air" {
			continue
		}
		index, found := indices[old]
		if !found {
			index = int32(len(states.Items))
			indices[old] = index
			states.Items = append(states.Items, state)
		}
		block["state"] = index
		filtered.Items = append(filtered.Items, block)
	}
	native["palette"], native["blocks"] = states, filtered
	s.structureTemplateStore(data.test, native, true)
	return data.test
}

// The default controller has no extra registry padding or sky-access rule.
// Only the outer six faces lose barriers; interior blocks are untouched.
func (s *Server) testInstanceRemoveBarriers(d *dimension, pos world.Pos, data testInstanceData) {
	size := data.size
	if data.rotation == 1 || data.rotation == 3 {
		size[0], size[2] = size[2], size[0]
	}
	start := [3]int32{int32(pos.X), int32(pos.Y) + 1, int32(pos.Z) + 1}
	var low, high [3]int32
	for axis := range start {
		end := start[axis] + size[axis] - 1
		low[axis] = min(start[axis], end) - 1
		high[axis] = max(start[axis], end) + 1
	}
	barrier, _ := world.BlockState("barrier")
	visit := func(x, y, z int64) {
		p := world.Pos{X: int(x), Y: int(y), Z: int(z)}
		if d.w.GetBlock(p) == barrier {
			s.setSilentBlock(d, p, world.Air)
		}
	}
	// Visit just the faces, retaining x-fast coordinate order. Clipping the
	// vertical read range skips only cells the existing build-height guard
	// could never change; the controller dimensions are not arbitrarily capped.
	lowY := max(int64(low[1]), int64(d.minY))
	highY := min(int64(high[1]), int64(d.minY+d.height-1))
	for z := int64(low[2]); z <= int64(high[2]); z++ {
		for y := lowY; y <= highY; y++ {
			if z == int64(low[2]) || z == int64(high[2]) || y == int64(low[1]) || y == int64(high[1]) {
				for x := int64(low[0]); x <= int64(high[0]); x++ {
					visit(x, y, z)
				}
			} else {
				visit(int64(low[0]), y, z)
				if high[0] != low[0] {
					visit(int64(high[0]), y, z)
				}
			}
		}
	}
}
