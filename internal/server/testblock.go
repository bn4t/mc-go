package server

import (
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

const testBlockEntityID = "minecraft:test_block"

var testBlockModes = [...]string{"start", "log", "fail", "accept"}

func validTestBlockMode(mode string) bool {
	for _, value := range testBlockModes {
		if mode == value {
			return true
		}
	}
	return false
}

// Native missing/invalid mode uses FAIL, while construction takes the mode
// property of the actual block. Loading does not rewrite that property.
func testBlockSavedFields(in map[string]any) map[string]any {
	out := map[string]any{"mode": "fail", "message": "", "powered": int8(0)}
	if mode, ok := in["mode"].(string); ok && validTestBlockMode(mode) {
		out["mode"] = mode
	}
	if message, ok := in["message"].(string); ok {
		out["message"] = message
	}
	out["powered"] = nbtBool(nbtTruth(in["powered"]))
	if components, ok := in["components"]; ok {
		out["components"] = cloneNBTValue(components)
	}
	return out
}
func (s *Server) testBlockAt(d *dimension, pos world.Pos) *genericBlockEntity {
	if d == nil {
		return nil
	}
	state := d.w.GetBlock(pos)
	spec, ok := blockEntityOf(state)
	if !ok || spec.name != testBlockEntityID {
		return nil
	}
	if current := d.genericBE[pos]; current != nil && current.id == testBlockEntityID {
		return current
	}
	mode, _ := world.StateProperty(state, "mode")
	if !validTestBlockMode(mode) {
		mode = "fail"
	}
	g := &genericBlockEntity{id: testBlockEntityID, fields: map[string]any{"mode": mode, "message": "", "powered": int8(0)}}
	if d.genericBE == nil {
		d.genericBE = map[world.Pos]*genericBlockEntity{}
	}
	d.genericBE[pos] = g
	return g
}

type testBlockEdit struct {
	pos           world.Pos
	mode, message string
}

func readTestBlockEdit(data []byte) (edit testBlockEdit, ok bool) {
	r := protocol.NewPacketReader(data)
	x, y, z, err := r.Position()
	if err != nil {
		return
	}
	mode, err := r.VarInt()
	if err != nil {
		return
	}
	if mode < 0 || mode >= int32(len(testBlockModes)) {
		mode = 0
	}
	message, err := r.String()
	if err != nil {
		return
	}
	return testBlockEdit{pos: world.Pos{X: int(x), Y: int(y), Z: int(z)}, mode: testBlockModes[mode], message: message}, true
}
func (s *Server) handleSetTestBlock(sess *Session, p protocol.Packet) {
	edit, ok := readTestBlockEdit(p.Data)
	if !ok || sess.mode() != GameModeCreative || sess.opLevel() < 2 {
		return
	}
	d := sess.dimension()
	g := s.testBlockAt(d, edit.pos)
	if g == nil {
		return
	}
	g.fields["mode"], g.fields["message"] = edit.mode, edit.message
	state, _ := world.WithProperty(d.w.GetBlock(edit.pos), "mode", edit.mode)
	// TestBlockEntity.setMode uses flags2: its property update does not
	// perform neighbour physics. The listener still marks an unchanged edit.
	d.w.SetBlock(edit.pos, state)
	s.broadcastBlockState(d, edit.pos, state)
	s.queueBlockEntityDirty(d, edit.pos)
}
