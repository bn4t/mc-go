package server

import (
	"bytes"
	"strings"
	"sync"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// signBlockEntityType is minecraft:sign in the block_entity_type
// registry (from the official report).
const signBlockEntityType = 7

// signText is the editable text of one sign block entity.
type signText struct {
	Front [4]string
	Back  [4]string
}

// signStore keeps block-entity state by position. Signs are the first
// block entity; chests and the rest extend this map with typed state.
type signStore struct {
	mu    sync.Mutex
	signs map[world.Pos]*signText
}

// isSignBlock reports whether a block state is a standing or wall sign.
func isSignBlock(b world.Block) bool {
	name, ok := world.BlockName(b)
	if !ok {
		return false
	}
	return strings.HasSuffix(name, "_sign") && !strings.HasSuffix(name, "_hanging_sign")
}

// signAt returns a copy of the sign text at pos in dimension d.
func (s *Server) signAt(d *dimension, pos world.Pos) (signText, bool) {
	d.signs.mu.Lock()
	defer d.signs.mu.Unlock()
	st, ok := d.signs.signs[pos]
	if !ok {
		return signText{}, false
	}
	return *st, true
}

// ensureSign creates an empty sign block entity when absent.
func (s *Server) ensureSign(d *dimension, pos world.Pos) {
	d.signs.mu.Lock()
	if d.signs.signs == nil {
		d.signs.signs = make(map[world.Pos]*signText)
	}
	if _, ok := d.signs.signs[pos]; !ok {
		d.signs.signs[pos] = &signText{}
	}
	d.signs.mu.Unlock()
}

// clearSign removes a block entity when its block is replaced.
func (s *Server) clearSign(d *dimension, pos world.Pos) {
	d.signs.mu.Lock()
	delete(d.signs.signs, pos)
	d.signs.mu.Unlock()
}

// signNBT builds the sign block entity's NBT payload.
func signNBT(st signText) []byte {
	text := func(lines [4]string) map[string]any {
		msgs := nbt.List{Elem: nbt.TagString, Items: make([]any, 0, 4)}
		for _, l := range lines {
			msgs.Items = append(msgs.Items, l)
		}
		return map[string]any{
			"messages":         msgs,
			"color":            "black",
			"has_glowing_text": int8(0),
		}
	}
	var buf bytes.Buffer
	_ = nbt.WriteUnnamed(&buf, map[string]any{
		"front_text": text(st.Front),
		"back_text":  text(st.Back),
		"is_waxed":   int8(0),
	})
	return buf.Bytes()
}

// broadcastBlockEntityData sends one Block Entity Data packet to every
// session tracking the chunk in dimension d.
func (s *Server) broadcastBlockEntityData(d *dimension, pos world.Pos, beType int32, payload []byte) {
	var b protocol.Buffer
	b.Position(int32(pos.X), int32(pos.Y), int32(pos.Z))
	b.VarInt(beType)
	b.Raw(payload)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dimName() != d.name || !sess.hasChunk(pos.X>>4, pos.Z>>4) {
			return true
		}
		_ = sess.write(sess.ids().PlayBlockEntityData, body)
		return true
	})
}

// sendOpenSignEditor opens the sign editor for one session.
func (s *Server) sendOpenSignEditor(sess *Session, pos world.Pos, front bool) {
	var b protocol.Buffer
	b.Position(int32(pos.X), int32(pos.Y), int32(pos.Z))
	b.Bool(front)
	_ = sess.write(sess.ids().PlayOpenSignEditor, b.Bytes())
}

// handleUpdateSign stores edited sign text and fans the block entity
// data out to tracking sessions.
func (s *Server) handleUpdateSign(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	x, y, z, err := r.Position()
	if err != nil {
		return
	}
	front, err := r.Bool()
	if err != nil {
		return
	}
	var lines [4]string
	for i := range lines {
		s, err := r.String()
		if err != nil {
			return
		}
		if len(s) > 384 {
			s = s[:384]
		}
		lines[i] = s
	}
	pos := world.Pos{X: int(x), Y: int(y), Z: int(z)}
	d := sess.dimension()
	if !isSignBlock(d.w.GetBlock(pos)) {
		return
	}
	s.ensureSign(d, pos)
	d.signs.mu.Lock()
	st := d.signs.signs[pos]
	if front {
		st.Front = lines
	} else {
		st.Back = lines
	}
	snapshot := *st
	d.signs.mu.Unlock()
	s.broadcastBlockEntityData(d, pos, signBlockEntityType, signNBT(snapshot))
}
