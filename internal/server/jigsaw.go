package server

import (
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"strings"
)

const jigsawEntityID = "minecraft:jigsaw"

func jigsawDefaults() map[string]any {
	return map[string]any{"name": "minecraft:empty", "target": "minecraft:empty", "pool": "minecraft:empty", "final_state": "minecraft:air", "joint": "rollable", "selection_priority": int32(0), "placement_priority": int32(0)}
}

// Identifier allows an empty path, while the structure editor's empty name
// means no selected template. Add its default namespace before validation.
func jigsawIdentifier(raw string) (string, bool) {
	if !strings.ContainsRune(raw, ':') {
		raw = "minecraft:" + raw
	}
	value := structureName(raw)
	return value, value != ""
}

func jigsawDefaultJoint(state world.Block) string {
	orientation, _ := world.StateProperty(state, "orientation")
	front, _, _ := strings.Cut(orientation, "_")
	if front == "north" || front == "south" || front == "east" || front == "west" {
		return "aligned"
	}
	return "rollable"
}

// Native loading resets absent/invalid fields and discards unrecognized data.
// Its missing-joint fallback differs from both construction and packet decode.
func jigsawSavedFields(state world.Block, in map[string]any) map[string]any {
	out := jigsawDefaults()
	out["joint"] = jigsawDefaultJoint(state)
	for _, key := range []string{"name", "target", "pool"} {
		if value, ok := in[key].(string); ok {
			if value, ok := jigsawIdentifier(value); ok {
				out[key] = value
			}
		}
	}
	if value, ok := in["final_state"].(string); ok {
		out["final_state"] = value
	}
	if value, ok := in["joint"].(string); ok && (value == "aligned" || value == "rollable") {
		out["joint"] = value
	}
	out["selection_priority"] = jigsawPriority(in["selection_priority"])
	out["placement_priority"] = jigsawPriority(in["placement_priority"])
	if value, ok := in["components"]; ok {
		out["components"] = cloneNBTValue(value)
	}
	return out
}

func jigsawPriority(value any) int32 {
	switch n := value.(type) {
	case float32:
		return jigsawFloatPriority(float64(n))
	case float64:
		return jigsawFloatPriority(n)
	default:
		if n, ok := nbtInt32(value); ok {
			return n
		}
		return 0
	}
}
func jigsawFloatPriority(value float64) int32 {
	value = math.Floor(value)
	if math.IsNaN(value) {
		return 0
	}
	if value >= math.MaxInt32 {
		return math.MaxInt32
	}
	if value <= math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}

func (s *Server) jigsawAt(d *dimension, pos world.Pos) *genericBlockEntity {
	if d == nil {
		return nil
	}
	spec, ok := blockEntityOf(d.w.GetBlock(pos))
	if !ok || spec.name != jigsawEntityID {
		return nil
	}
	if g := d.genericBE[pos]; g != nil && g.id == jigsawEntityID {
		return g
	}
	g := &genericBlockEntity{id: jigsawEntityID, fields: jigsawDefaults()}
	if d.genericBE == nil {
		d.genericBE = map[world.Pos]*genericBlockEntity{}
	}
	d.genericBE[pos] = g
	return g
}

type jigsawEdit struct {
	pos                                   world.Pos
	name, target, pool, finalState, joint string
	selection, placement                  int32
}

func readJigsawEdit(data []byte) (edit jigsawEdit, ok bool) {
	r := protocol.NewPacketReader(data)
	x, y, z, err := r.Position()
	if err != nil {
		return
	}
	edit.pos = world.Pos{X: int(x), Y: int(y), Z: int(z)}
	for _, field := range []*string{&edit.name, &edit.target, &edit.pool} {
		raw, e := r.String()
		if e != nil {
			return
		}
		value, valid := jigsawIdentifier(raw)
		if !valid {
			return
		}
		*field = value
	}
	if edit.finalState, err = r.String(); err != nil {
		return
	}
	if edit.joint, err = r.String(); err != nil {
		return
	}
	if edit.joint != "aligned" && edit.joint != "rollable" {
		edit.joint = "aligned"
	}
	if edit.selection, err = r.VarInt(); err != nil {
		return
	}
	if edit.placement, err = r.VarInt(); err != nil {
		return
	}
	return edit, true
}

func (s *Server) handleSetJigsawBlock(sess *Session, p protocol.Packet) {
	edit, ok := readJigsawEdit(p.Data)
	if !ok || sess.mode() != GameModeCreative || sess.opLevel() < 2 {
		return
	}
	d := sess.dimension()
	g := s.jigsawAt(d, edit.pos)
	if g == nil {
		return
	}
	g.fields["name"], g.fields["target"], g.fields["pool"] = edit.name, edit.target, edit.pool
	g.fields["final_state"], g.fields["joint"] = edit.finalState, edit.joint
	g.fields["selection_priority"], g.fields["placement_priority"] = edit.selection, edit.placement
	// setChanged/sendBlockUpdated occurs even for a semantically unchanged edit.
	s.broadcastBlockState(d, edit.pos, d.w.GetBlock(edit.pos))
	s.queueBlockEntityDirty(d, edit.pos)
}
