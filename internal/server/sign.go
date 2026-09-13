package server

import (
	"bytes"
	"math"
	"strconv"
	"strings"
	"sync"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// signText is one sign block entity's state: the four lines of each side,
// and how each side is drawn.
//
// A sign's styling is part of the block entity and not of the block: the
// real server keeps the dye colour's name and whether the text glows per
// side, and whether the whole sign is waxed, all three of which travel to
// the client in the chunk packet and are written to the chunk file. A
// sign with no colour is "black", which is what a fresh sign is.
type signText struct {
	Front [4]string
	Back  [4]string
	// frontColor and backColor name the dye colour of each side.
	frontColor string
	backColor  string
	// frontGlowing and backGlowing are the glowing-ink state of each
	// side.
	frontGlowing bool
	backGlowing  bool
	// waxed is the honeycomb state of the whole sign, which stops its
	// text being edited.
	waxed bool
}

// signColor is the colour name a side is drawn in, which the real server
// always writes: a side that has never been dyed is black rather than
// absent.
func signColor(name string) string {
	if name == "" {
		return "black"
	}
	return name
}

// signStore keeps block-entity state by position. Signs are the first
// block entity; chests and the rest extend this map with typed state.
//
// editors is which player, if any, has each sign's editor open. The real
// server shows a sign's editor to one player at a time and answers
// another player's use of that sign with nothing at all - not even the
// block entity data a dye would bring - so a use has to consult this
// before it does anything.
type signStore struct {
	mu      sync.Mutex
	signs   map[world.Pos]*signText
	editors map[world.Pos]*Session
}

// signSpecOf reports whether a block is a sign - hanging or standing -
// and which block entity type it carries, which is 7 for a sign and 8
// for a hanging sign.
func signSpecOf(b world.Block) (blockEntitySpec, bool) {
	spec, ok := blockEntityOf(b)
	if !ok || (spec.name != "minecraft:sign" && spec.name != "minecraft:hanging_sign") {
		return blockEntitySpec{}, false
	}
	return spec, true
}

// isSignBlock reports whether a block state is a standing or wall sign.
func isSignBlock(b world.Block) bool {
	name, ok := world.StateOwner(b)
	if !ok {
		return false
	}
	return strings.HasSuffix(name, "_sign") && !strings.HasSuffix(name, "_hanging_sign")
}

// isSignLikeBlock reports whether a block is a sign of any kind, hanging
// signs included: the real server's hanging sign is a sign with a
// different block and block entity type, and every interaction below
// treats it exactly like a standing one.
func isSignLikeBlock(b world.Block) bool {
	name, ok := world.StateOwner(b)
	return ok && strings.HasSuffix(name, "_sign")
}

// signFrontDir is the direction a sign's front faces, as a unit vector
// in the xz plane with x pointing east and z pointing south, which is
// the frame the measurements in signSideFor are in.
//
// A wall sign says it outright: the front faces its facing. A standing
// sign has only a rotation, and the mapping from that number to a
// direction was measured on 26.2 rather than assumed - a player south of
// a sign of rotation 0 sees its front, one west of rotation 4, one north
// of rotation 8 and one east of rotation 12, so the front turns a
// sixteenth of a turn south to west to north to east for every step of
// rotation.
func signFrontDir(b world.Block) (fx, fz float64, ok bool) {
	if f, ok := world.StateProperty(b, "facing"); ok {
		switch f {
		case "north":
			return 0, -1, true
		case "south":
			return 0, 1, true
		case "west":
			return -1, 0, true
		case "east":
			return 1, 0, true
		}
		return 0, 0, false
	}
	r, ok := world.StateProperty(b, "rotation")
	if !ok {
		return 0, 0, false
	}
	n, err := strconv.Atoi(r)
	if err != nil {
		return 0, 0, false
	}
	angle := float64(n) * math.Pi / 8
	return -math.Sin(angle), math.Cos(angle), true
}

// signSideFor is which of a sign's two sides a player at (px, pz) is on:
// the side the front of the sign faces, or the one behind it. The whole
// measured table agrees with it - a player is on the front when the
// vector from the sign's centre to the player has a positive dot product
// with the direction the front faces, for the sixteen standing rotations
// and the four wall facings alike.
//
// A player exactly on the line between the two sides - square on to a
// sign's edge - is a case the real server settles with floating-point
// noise, and the readings there disagree between geometries; the sign is
// treated as front from there, which is what most of them say.
func signSideFor(px, pz float64, pos world.Pos, b world.Block) (front bool, ok bool) {
	fx, fz, ok := signFrontDir(b)
	if !ok {
		return true, false
	}
	vx := px - (float64(pos.X) + 0.5)
	vz := pz - (float64(pos.Z) + 0.5)
	return vx*fx+vz*fz >= 0, true
}

// signSideStyled is one side's lines, colour and glow, as one value.
type signSideStyled struct {
	lines   [4]string
	color   string
	glowing bool
}

func signSideOf(st signText, front bool) signSideStyled {
	if front {
		return signSideStyled{st.Front, st.frontColor, st.frontGlowing}
	}
	return signSideStyled{st.Back, st.backColor, st.backGlowing}
}

// setSignSide writes one side's lines, colour and glow back into st.
func setSignSide(st *signText, front bool, s signSideStyled) {
	if front {
		st.Front, st.frontColor, st.frontGlowing = s.lines, s.color, s.glowing
		return
	}
	st.Back, st.backColor, st.backGlowing = s.lines, s.color, s.glowing
}

// signSideHasText reports whether a side holds anything the client can
// see, which is what a dye and a glow ink sac need before they apply:
// measured on 26.2, both take on a side that has text and neither does
// on one that has none, where the use opens the editor instead.
func signSideHasText(s signSideStyled) bool {
	for _, l := range s.lines {
		if l != "" {
			return true
		}
	}
	return false
}

// dyeColor is the colour a dye item paints with, if the item is a dye.
func dyeColor(item string) (string, bool) {
	if !strings.HasSuffix(item, "_dye") {
		return "", false
	}
	return strings.TrimSuffix(item, "_dye"), true
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
	delete(d.signs.editors, pos)
	d.signs.mu.Unlock()
}

// signEditReach is the squared distance the player holding a sign's
// editor may reach before the sign is free for someone else. Measured on
// 26.2 by walking the holder away from the sign it had open: 9 blocks
// out another player is still answered with nothing, 10 blocks out it is
// given the editor. The distance is taken from the player's feet to the
// centre of the sign's block, which is what the readings fit.
const signEditReach = 100.0

// signEditorHolder returns the session whose editor is open on this
// sign, or nil when nobody's is. A holder that has left the game, or
// walked more than signEditReach away, has let go.
func (s *Server) signEditorHolder(d *dimension, pos world.Pos) *Session {
	d.signs.mu.Lock()
	holder := d.signs.editors[pos]
	d.signs.mu.Unlock()
	if holder == nil || holder.state.Load() != StatePlay {
		return nil
	}
	hx, hy, hz, _, _, _ := holder.Pos()
	dx := hx - (float64(pos.X) + 0.5)
	dy := hy - (float64(pos.Y) + 0.5)
	dz := hz - (float64(pos.Z) + 0.5)
	if dx*dx+dy*dy+dz*dz > signEditReach {
		return nil
	}
	return holder
}

// signEditorHeldByOther reports whether somebody else's editor is open
// on this sign.
func (s *Server) signEditorHeldByOther(d *dimension, pos world.Pos, sess *Session) bool {
	holder := s.signEditorHolder(d, pos)
	return holder != nil && holder != sess
}

// claimSignEditor records one session as the player editing a sign. A
// player who already holds the sign keeps it: measured on 26.2, the
// holder can ask its own sign for the editor again and again.
func (s *Server) claimSignEditor(d *dimension, pos world.Pos, sess *Session) {
	d.signs.mu.Lock()
	if d.signs.editors == nil {
		d.signs.editors = make(map[world.Pos]*Session)
	}
	d.signs.editors[pos] = sess
	d.signs.mu.Unlock()
}

// releaseSignEditor lets go of one sign, which the real server does when
// the holder writes it: measured, the next player to use a sign the
// holder has just finished writing is given the editor straight away.
func (s *Server) releaseSignEditor(d *dimension, pos world.Pos, sess *Session) {
	d.signs.mu.Lock()
	if d.signs.editors[pos] == sess {
		delete(d.signs.editors, pos)
	}
	d.signs.mu.Unlock()
}

// releaseSignEdits lets go of every sign one session was editing, which
// is what a player leaving the game does. Measured: a sign whose holder
// has disconnected is free at once.
func (s *Server) releaseSignEdits(sess *Session) {
	for _, d := range s.dims {
		d.signs.mu.Lock()
		for pos, holder := range d.signs.editors {
			if holder == sess {
				delete(d.signs.editors, pos)
			}
		}
		d.signs.mu.Unlock()
	}
}

// signNBT builds the sign block entity's NBT payload, which is the data
// a client is given both in the chunk packet and in a block entity data
// update: the two sides and the waxed flag, and nothing else - no id, no
// position and no saved-only fields, which is what the real server sends.
func signNBT(st signText) []byte {
	var buf bytes.Buffer
	_ = nbt.WriteUnnamed(&buf, map[string]any{
		"front_text": signTextNBT(st.Front, st.frontColor, st.frontGlowing),
		"back_text":  signTextNBT(st.Back, st.backColor, st.backGlowing),
		"is_waxed":   boolByte(st.waxed),
	})
	return buf.Bytes()
}

// boolByte is the byte the real server writes for a boolean field.
func boolByte(v bool) int8 {
	if v {
		return 1
	}
	return 0
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

// useSign handles a use of a sign block: one of the four items that can
// change a sign, or the editor opening on the side the player is on.
//
// A dye paints the side the player is on, a glow ink sac makes that
// side's text glow, an ink sac takes the glow away again, and a honeycomb
// waxes the whole sign. The dye and the glow take only on a side that has
// text and the ink sac only on a side that is glowing; anything else -
// including any use of a waxed sign, by hand or by item - falls through
// to the editor, which is how a sign is written on in the first place.
// A waxed sign is the one that does nothing at all: measured on 26.2, a
// waxed sign answers no use, not even with an editor.
//
// A sign another player is editing answers nothing either, whatever is in
// hand: measured on 26.2, a dye aimed at a sign whose editor another
// player holds changes no colour and sends no packet, while the holder
// itself goes on dyeing and writing its own sign as usual.
func (s *Server) useSign(sess *Session, d *dimension, pos world.Pos, b world.Block) {
	if st, ok := s.signAt(d, pos); ok && st.waxed {
		return
	}
	if s.signEditorHeldByOther(d, pos, sess) {
		return
	}
	px, _, pz, _, _, _ := sess.Pos()
	front, _ := signSideFor(px, pz, pos, b)
	name := ""
	if item, ok := sess.heldItem(); ok {
		name, _ = world.ItemName(item)
	}
	s.ensureSign(d, pos)
	d.signs.mu.Lock()
	st := *d.signs.signs[pos]
	d.signs.mu.Unlock()

	side := signSideOf(st, front)
	changed := false
	switch {
	case name == "glow_ink_sac" && signSideHasText(side):
		side.glowing, changed = true, true
	case name == "ink_sac" && side.glowing:
		side.glowing, changed = false, true
	case name == "honeycomb":
		st.waxed, changed = true, true
	default:
		if c, isDye := dyeColor(name); isDye && signSideHasText(side) {
			side.color, changed = c, true
		}
	}
	if !changed {
		s.claimSignEditor(d, pos, sess)
		s.sendOpenSignEditor(sess, pos, front)
		return
	}
	d.signs.mu.Lock()
	cur := d.signs.signs[pos]
	setSignSide(cur, front, side)
	cur.waxed = st.waxed
	snapshot := *cur
	d.signs.mu.Unlock()
	if spec, ok := signSpecOf(b); ok {
		s.broadcastBlockEntityData(d, pos, spec.id, signNBT(snapshot))
	}
	// Survival spends the item and syncs the slot; creative keeps it.
	if sess.mode() != GameModeCreative {
		sess.consumeHeld(1)
	}
}

// handleUpdateSign stores edited sign text and fans the block entity
// data out to tracking sessions.
//
// The packet names the side it writes, and the real server keeps only a
// write that names the side the player is standing on: measured on
// 26.2, a player in front of a sign who writes its back changes nothing
// and is answered with nothing, and the same the other way round. That
// is the same side test the editor opens by, so a client that writes
// the side it was given always matches.
//
// It is also only the player whose editor is open who may write at all.
// Measured by reading the sign back with the real server's own /data
// after a scripted client that was never given the editor sent the
// packet: nothing is stored, and nothing is sent - whether or not
// another player was editing the sign at the time.
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
	blk := d.w.GetBlock(pos)
	if !isSignLikeBlock(blk) {
		return
	}
	px, _, pz, _, _, _ := sess.Pos()
	if near, ok := signSideFor(px, pz, pos, blk); ok && near != front {
		return
	}
	if s.signEditorHolder(d, pos) != sess {
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
	if spec, ok := signSpecOf(blk); ok {
		s.broadcastBlockEntityData(d, pos, spec.id, signNBT(snapshot))
	}
	// Writing the sign is what the editor was open for, so it ends there:
	// the real server lets another player edit the sign straight after.
	s.releaseSignEditor(d, pos, sess)
}
