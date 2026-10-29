package server

import (
	"math"
	"strings"
	"unicode/utf16"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

var structureModes = [...]string{"SAVE", "LOAD", "CORNER", "DATA"}
var structureMirrors = [...]string{"NONE", "LEFT_RIGHT", "FRONT_BACK"}
var structureRotations = [...]string{"NONE", "CLOCKWISE_90", "CLOCKWISE_180", "COUNTERCLOCKWISE_90"}

// StructureBlockEntity's native defaults; constructor mode comes from state,
// while native loading without mode uses DATA. Evidence: pinned26.2 javap.
func structureBlockDefaults(mode string) map[string]any {
	return map[string]any{"name": "", "author": "", "metadata": "", "posX": int32(0), "posY": int32(1), "posZ": int32(0), "sizeX": int32(0), "sizeY": int32(0), "sizeZ": int32(0), "rotation": "NONE", "mirror": "NONE", "mode": mode, "ignoreEntities": int8(1), "strict": int8(0), "powered": int8(0), "showair": int8(0), "showboundingbox": int8(1), "integrity": float32(1), "seed": int64(0)}
}
func structureName(raw string) string {
	if raw == "" {
		return ""
	}
	ns, path, found := strings.Cut(raw, ":")
	if !found {
		ns, path = "minecraft", ns
	}
	if ns == "" {
		ns = "minecraft"
	}
	if ns == ".." {
		return ""
	}
	for _, c := range ns {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("_.-", c)) {
			return ""
		}
	}
	for _, c := range path {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("/._-", c)) {
			return ""
		}
	}
	return ns + ":" + path
}
func structureBlockSavedFields(in map[string]any) map[string]any {
	out := structureBlockDefaults("DATA")
	for k, def := range out {
		v, ok := in[k]
		if !ok {
			continue
		}
		switch def.(type) {
		case string:
			if v, ok := v.(string); ok {
				out[k] = v
			}
		case int8:
			out[k] = nbtBool(nbtTruth(v))
		case int32:
			if n, ok := nbtNumber(v); ok {
				out[k] = int32(n)
			}
		case int64:
			out[k] = commandLong(v, 0)
		case float32:
			if n, ok := nbtFloat(v); ok {
				out[k] = float32(n)
			}
		}
	}
	out["name"] = structureName(out["name"].(string))
	for _, k := range []string{"posX", "posY", "posZ"} {
		out[k] = max(int32(-48), min(int32(48), out[k].(int32)))
	}
	for _, k := range []string{"sizeX", "sizeY", "sizeZ"} {
		out[k] = max(int32(0), min(int32(48), out[k].(int32)))
	}
	for k, values := range map[string][]string{"mode": structureModes[:], "mirror": structureMirrors[:], "rotation": structureRotations[:]} {
		value := out[k].(string)
		valid := false
		for _, v := range values {
			valid = valid || value == v
		}
		if !valid {
			out[k] = structureBlockDefaults("DATA")[k]
		}
	}
	if c, ok := in["components"]; ok {
		out["components"] = cloneNBTValue(c)
	}
	return out
}
func (s *Server) structureBlockAt(d *dimension, pos world.Pos) *genericBlockEntity {
	name, _ := world.StateOwner(d.w.GetBlock(pos))
	if name != "structure_block" {
		return nil
	}
	if g, _, ok := genericBlockEntityAt(d, pos); ok {
		return g
	}
	mode, _ := world.StateProperty(d.w.GetBlock(pos), "mode")
	g := &genericBlockEntity{id: "minecraft:structure_block", fields: structureBlockDefaults(strings.ToUpper(mode))}
	if d.genericBE == nil {
		d.genericBE = map[world.Pos]*genericBlockEntity{}
	}
	d.genericBE[pos] = g
	return g
}
func (s *Server) structureBlockMode(d *dimension, pos world.Pos, mode string) {
	if state, ok := world.BlockStateOf("structure_block", map[string]string{"mode": strings.ToLower(mode)}); ok && state != d.w.GetBlock(pos) {
		s.setSilentBlock(d, pos, state)
	}
}

type structureBlockEdit struct {
	pos                            world.Pos
	action, mode, mirror, rotation int32
	name, data                     string
	offset, size                   [3]int32
	integrity                      float32
	seed                           int64
	flags                          byte
}

func readStructureBlockEdit(data []byte) (edit structureBlockEdit, ok bool) {
	r := protocol.NewPacketReader(data)
	x, y, z, e := r.Position()
	if e != nil {
		return
	}
	edit.pos = world.Pos{X: int(x), Y: int(y), Z: int(z)}
	if edit.action, e = r.VarInt(); e != nil || edit.action < 0 || edit.action > 3 {
		return
	}
	if edit.mode, e = r.VarInt(); e != nil || edit.mode < 0 || edit.mode > 3 {
		return
	}
	if edit.name, e = r.String(); e != nil {
		return
	}
	for i := range edit.offset {
		b, e := r.Byte()
		if e != nil {
			return edit, false
		}
		edit.offset[i] = max(int32(-48), min(int32(48), int32(int8(b))))
	}
	for i := range edit.size {
		b, e := r.Byte()
		if e != nil {
			return edit, false
		}
		edit.size[i] = max(int32(0), min(int32(48), int32(int8(b))))
	}
	if edit.mirror, e = r.VarInt(); e != nil || edit.mirror < 0 || edit.mirror > 2 {
		return
	}
	if edit.rotation, e = r.VarInt(); e != nil || edit.rotation < 0 || edit.rotation > 3 {
		return
	}
	if edit.data, e = r.String(); e != nil || len(utf16.Encode([]rune(edit.data))) > 128 {
		return
	}
	if edit.integrity, e = r.Float(); e != nil {
		return
	}
	if !math.IsNaN(float64(edit.integrity)) {
		edit.integrity = max(float32(0), min(float32(1), edit.integrity))
	}
	if edit.seed, e = r.VarLong(); e != nil {
		return
	}
	if edit.flags, e = r.Byte(); e != nil {
		return
	}
	return edit, true
}
func (s *Server) handleSetStructureBlock(sess *Session, p protocol.Packet) {
	edit, ok := readStructureBlockEdit(p.Data)
	if !ok || sess.mode() != GameModeCreative || sess.opLevel() < 2 {
		return
	}
	d := sess.dimension()
	g := s.structureBlockAt(d, edit.pos)
	if g == nil {
		return
	}
	f := g.fields
	f["name"] = structureName(edit.name)
	f["mode"] = structureModes[edit.mode]
	f["metadata"] = edit.data
	f["rotation"] = structureRotations[edit.rotation]
	f["mirror"] = structureMirrors[edit.mirror]
	f["integrity"] = edit.integrity
	f["seed"] = edit.seed
	for i, k := range []string{"posX", "posY", "posZ"} {
		f[k] = edit.offset[i]
	}
	for i, k := range []string{"sizeX", "sizeY", "sizeZ"} {
		f[k] = edit.size[i]
	}
	for k, mask := range map[string]byte{"ignoreEntities": 1, "showair": 2, "showboundingbox": 4, "strict": 8} {
		f[k] = nbtBool(edit.flags&mask != 0)
	}
	s.structureBlockMode(d, edit.pos, f["mode"].(string))
	name := f["name"].(string)
	switch {
	case name == "":
		s.sendSystemChatComponent(sess, translatable("structure_block.invalid_structure_name", edit.name))
	case edit.action == 3:
		if s.structureBlockScan(d, edit.pos, g) {
			s.sendSystemChatComponent(sess, translatable("structure_block.size_success", name))
		} else {
			s.sendSystemChatComponent(sess, translatable("structure_block.size_failure"))
		}
	}
	s.broadcastBlockState(d, edit.pos, d.w.GetBlock(edit.pos))
	spec, _ := blockEntitySpecForType("structure_block")
	s.broadcastBlockEntityData(d, edit.pos, spec.id, blockEntityUpdateTag("structure_block", f))
}

// Matching corner markers determine the open interior. A single marker uses
// the controller as its opposite corner; two or more use only the markers.
func (s *Server) structureBlockScan(d *dimension, pos world.Pos, g *genericBlockEntity) bool {
	if g.fields["mode"] != "SAVE" {
		return false
	}
	var corners []world.Pos
	for p, c := range d.genericBE {
		if c.id != "minecraft:structure_block" || c.fields["mode"] != "CORNER" || c.fields["name"] != g.fields["name"] || p.X < pos.X-80 || p.X > pos.X+80 || p.Z < pos.Z-80 || p.Z > pos.Z+80 {
			continue
		}
		if name, _ := world.StateOwner(d.w.GetBlock(p)); name == "structure_block" {
			corners = append(corners, p)
		}
	}
	if len(corners) == 0 {
		return false
	}
	if len(corners) == 1 {
		corners = append(corners, pos)
	}
	low, high := corners[0], corners[0]
	for _, p := range corners[1:] {
		low = world.Pos{X: min(low.X, p.X), Y: min(low.Y, p.Y), Z: min(low.Z, p.Z)}
		high = world.Pos{X: max(high.X, p.X), Y: max(high.Y, p.Y), Z: max(high.Z, p.Z)}
	}
	if high.X-low.X <= 1 || high.Y-low.Y <= 1 || high.Z-low.Z <= 1 {
		return false
	}
	g.fields["posX"], g.fields["posY"], g.fields["posZ"] = int32(low.X-pos.X+1), int32(low.Y-pos.Y+1), int32(low.Z-pos.Z+1)
	g.fields["sizeX"], g.fields["sizeY"], g.fields["sizeZ"] = int32(high.X-low.X-1), int32(high.Y-low.Y-1), int32(high.Z-low.Z-1)
	return true
}
