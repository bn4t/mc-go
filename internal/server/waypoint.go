// Locator-bar waypoints: the tracked_waypoint (0x8A) emit layer, modelled
// on vanilla's ServerWaypointManager + WaypointTransmitter connections.
//
// Wire layout (measured on 26.2 + ClientboundTrackedWaypointPacket):
//
//	TRACK   op 0: Either[UUID,String] identifier, Icon, waypoint type+contents
//	UNTRACK op 1: identifier only
//	UPDATE  op 2: identifier, Icon, waypoint type+contents
//	identifier: bool(1)=UUID + 16 bytes for entity transmitters
//	Icon: ResourceLocation style string + optional int32 color
//	waypoint type: 0 empty, 1 VEC3I (x,y,z VarInts), 2 CHUNK (cx,cz VarInts),
//	3 AZIMUTH (float32 angle radians)
//
// Mechanics (javap on ServerWaypointManager / WaypointTransmitter /
// Entity{Block,Chunk,Azimuth}Connection):
//   - Every ServerPlayer is a receiver (gamerule locator_bar) and every
//     LivingEntity with waypoint_transmit_range > 0 is a transmitter
//     (players: 6e7; mobs: 0 unless overridden).
//   - createConnection: skip self, receiver-with-locatorBar-off,
//     source.firstTick, and doesSourceIgnoreReceiver; then the kind is
//     AZIMUTH when distance > 332, CHUNK when the source chunk is outside
//     the receiver's chunk-tracking view, else VEC3I.
//   - The tracking view is centred on the receiver's chunk with radius
//     clamp(requestedViewDistance, 2, serverViewDistance); a source chunk is
//     visible while chessboard distance <= radius (measured: chunks <=2
//     around the receiver emit VEC3I at view radius 2, chunk 3 emits CHUNK).
//   - doesSourceIgnoreReceiver: receiver spectator -> visible; else ignored
//     when the source is a spectator, the receiver is an indirect
//     passenger of the source, or distance >= min(transmit,receive).
//   - updateConnection each tick: !broken -> update() (UPDATE emit when the
//     kind's contents moved); broken -> remake (TRACK) or disconnect
//     (UNTRACK + drop).
//   - isBroken: VEC3I conn: manhattan(lastPos, source.blockPos) > 1, or
//     ignore. CHUNK conn: chessboard(lastChunk, source.chunk) > 1, or
//     ignore, or the STORED lastChunk turned visible. AZIMUTH conn:
//     ignore, or source chunk visible, or distance <= 332.
//   - Emitted angle = atan2(diff.x, -diff.z) with diff = receiver - source;
//     updates emit once |angle - last| > 0.008726646 rad (0.5 deg).
package server

import (
	"fmt"
	"math"

	"mc-go/internal/protocol"
)

// Waypoint connection kinds — TrackedWaypoint$Type ordinals on the wire.
const (
	wpKindEmpty   = 0
	wpKindVec3i   = 1
	wpKindChunk   = 2
	wpKindAzimuth = 3
)

const (
	// wpAzimuthDist is WaypointTransmitter.isReallyFar's 332-block bound.
	wpAzimuthDist = 332.0
	// wpAzimuthEpsilon is EntityAzimuthConnection's 0.5-degree emit gate.
	wpAzimuthEpsilon = 0.008726646
)

// wpConn is one WaypointTransmitter$Connection on a receiver session.
type wpConn struct {
	kind      int32
	uuid      [16]byte // source identifier — UNTRACK needs it post-death
	style     string
	hasColor  bool
	color     int32
	lastPos   [3]int32 // vec3i kind: last emitted blockPos
	lastChunk [2]int32 // chunk kind: last emitted chunkPos
	lastAngle float32  // azimuth kind: last emitted angle
}

// wpSource is a snapshot of one transmitter for one tick's decisions.
type wpSource struct {
	id        int32
	uuid      [16]byte
	pos       [3]float64
	blockPos  [3]int32
	chunk     [2]int32
	transmit  float64 // effective waypoint_transmit_range
	spectator bool
	firstTick bool
	passenger bool // receiver is an indirect passenger of this source
	alive     bool // resolved source exists this tick
}

// waypointTickStep is ServerWaypointManager.updatePlayer fan-out: once per
// level tick every receiver's connection table is walked — existing
// connections update-or-remake, and every transmitter not yet connected
// gets a createConnection try. Vanilla splits this into a per-player
// updatePlayer and a per-transmitter updateWaypoint; one receiver-side
// pass produces the same packets.
func (s *Server) waypointTickStep() {
	s.sessions.Range(func(_, v any) bool {
		r := v.(*Session)
		if r.state.Load() == StatePlay {
			s.waypointUpdatePlayer(r)
		}
		return true
	})
	s.entities.ClearWaypointFirstTicks()
}

// waypointUpdatePlayer is ServerWaypointManager.updatePlayer for one
// receiver: connections first (updateConnection), then not-yet-connected
// transmitters (createConnection).
func (s *Server) waypointUpdatePlayer(r *Session) {
	if r.entityID.Load() == 0 {
		return
	}
	r.wpMu.Lock()
	defer r.wpMu.Unlock()
	// Existing connections.
	for srcID, conn := range r.wpConns {
		src := s.wpResolve(srcID)
		if !src.alive {
			// untrackWaypoint: connection drop + UNTRACK.
			s.wpSend(r, wpEncodeOp(1, conn))
			delete(r.wpConns, srcID)
			continue
		}
		s.wpUpdateConnection(r, src, conn)
	}
	// Transmitters not yet connected.
	for _, src := range s.wpTransmitters(r) {
		if _, ok := r.wpConns[src.id]; !ok {
			s.wpCreateConnection(r, src)
		}
	}
}

// wpTransmitters lists the transmitting sources in the receiver's
// dimension: living entities with waypoint_transmit_range > 0.
func (s *Server) wpTransmitters(r *Session) []wpSource {
	dim := r.dimName()
	var out []wpSource
	all := s.entities.All()
	for i := range all {
		e := &all[i]
		if e.Dim != dim {
			continue
		}
		src := s.wpSourceOf(e, r)
		if src.transmit > 0 {
			out = append(out, src)
		}
	}
	return out
}

// wpResolve snapshots the connection source keyed by entity id.
func (s *Server) wpResolve(srcID int32) wpSource {
	e, ok := s.entities.GetPtr(srcID)
	if !ok {
		return wpSource{id: srcID}
	}
	return s.wpSourceOf(e, nil)
}

// wpSourceOf builds the wpSource snapshot for an entity record; live
// player positions come from the session, everything else from the
// record.
func (s *Server) wpSourceOf(e *Entity, receiver *Session) wpSource {
	src := wpSource{
		id:        e.ID,
		uuid:      e.UUID,
		firstTick: e.firstTick,
		alive:     true,
	}
	kn, _ := EntityTypeName(e.Type)
	src.transmit = s.mobAttr(e, kn, "waypoint_transmit_range", 0)
	if ps := s.sessionByEntityID(e.ID); ps != nil {
		x, y, z, _, _, _ := ps.Pos()
		src.pos = [3]float64{x, y, z}
		src.spectator = ps.gameMode == 3
	} else {
		src.pos = [3]float64{e.X, e.Y, e.Z}
	}
	src.blockPos = [3]int32{
		int32(math.Floor(src.pos[0])),
		int32(math.Floor(src.pos[1])),
		int32(math.Floor(src.pos[2])),
	}
	src.chunk = [2]int32{src.blockPos[0] >> 4, src.blockPos[2] >> 4}
	if receiver != nil {
		reID := receiver.entityID.Load()
		// hasIndirectPassenger: the receiver rides somewhere inside the
		// source's passenger tree.
		seen := map[int32]bool{}
		stack := append([]int32{}, e.Passengers...)
		for len(stack) > 0 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if pid == reID {
				src.passenger = true
				break
			}
			if seen[pid] {
				continue
			}
			seen[pid] = true
			if pe, ok := s.entities.GetPtr(pid); ok {
				stack = append(stack, pe.Passengers...)
			}
		}
	}
	return src
}

// wpDistance is Entity.distanceTo — euclidean between positions.
func wpDistance(src wpSource, rx, ry, rz float64) float64 {
	dx := src.pos[0] - rx
	dy := src.pos[1] - ry
	dz := src.pos[2] - rz
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// wpIgnore is WaypointTransmitter.doesSourceIgnoreReceiver.
func (s *Server) wpIgnore(src wpSource, r *Session, rx, ry, rz float64) bool {
	if r.gameMode == 3 {
		return false
	}
	if src.spectator || src.passenger {
		return true
	}
	reID := r.entityID.Load()
	pe, _ := s.entities.GetPtr(reID)
	receive := 6.0e7
	if pe != nil {
		receive = s.mobAttr(pe, "player", "waypoint_receive_range", 6.0e7)
	}
	if src.transmit < receive {
		receive = src.transmit
	}
	return wpDistance(src, rx, ry, rz) >= receive
}

// wpChunkVisible is WaypointTransmitter.isChunkVisible: the source chunk
// is inside the receiver's tracking view — centred on the receiver's
// chunk, radius clamp(requestedViewDistance, 2, serverViewDistance),
// chessboard distance.
func (s *Server) wpChunkVisible(chunk [2]int32, r *Session) bool {
	rx, _, rz, _, _, _ := r.Pos()
	rcx, rcz := int32(math.Floor(rx))>>4, int32(math.Floor(rz))>>4
	vd := r.viewRadius(s.ViewDistance)
	dx := chunk[0] - rcx
	if dx < 0 {
		dx = -dx
	}
	dz := chunk[1] - rcz
	if dz < 0 {
		dz = -dz
	}
	return dx <= int32(vd) && dz <= int32(vd)
}

// wpRemakeConnections is ServerWaypointManager.remakeConnections:
// the transmitter's identity changed (team join/leave/modify), so every
// receiver re-creates its connection — a live one is replaced by a fresh
// TRACK (no UNTRACK; Table.put drops the old silently), a dead one drops
// with UNTRACK. Fires from the scoreboard's team callbacks.
func (s *Server) wpRemakeConnections(e *Entity) {
	s.sessions.Range(func(_, v any) bool {
		r := v.(*Session)
		if r.state.Load() != StatePlay || r.entityID.Load() == 0 || r.dimName() != e.Dim {
			return true
		}
		r.wpMu.Lock()
		s.wpRemakeFor(r, e)
		r.wpMu.Unlock()
		return true
	})
}

// wpRemakeFor is createConnection on remake: self and locatorBar-off
// receivers skip; the make result replaces any existing conn (TRACK) or
// drops it (UNTRACK).
func (s *Server) wpRemakeFor(r *Session, e *Entity) {
	if e.ID == r.entityID.Load() || !s.wpLocatorBar(r) {
		return
	}
	src := s.wpSourceOf(e, r)
	x, y, z, _, _, _ := r.Pos()
	conn := s.wpMakeConnection(r, src, x, y, z)
	if conn == nil {
		if old, ok := r.wpConns[e.ID]; ok {
			s.wpSend(r, wpEncodeOp(1, old))
			delete(r.wpConns, e.ID)
		}
		return
	}
	if r.wpConns == nil {
		r.wpConns = map[int32]*wpConn{}
	}
	r.wpConns[e.ID] = conn
	s.wpSend(r, wpEncodeOp(0, conn))
}

// wpRemakeByName remakes one holder's connections — vanilla resolves the
// scoreboard name to an online player only (getPlayerByName), so mob
// members and free-form names never reach here.
func (s *Server) wpRemakeByName(name string) {
	sess := s.findSessionByName(name)
	if sess == nil {
		return
	}
	if e, ok := s.entities.GetPtr(sess.entityID.Load()); ok {
		s.wpRemakeConnections(e)
	}
}

// wpRemakeTeam remakes every member's connections — the scoreboard's
// updateTeamWaypoints for an onTeamChanged (any /team modify).
func (s *Server) wpRemakeTeam(t *team) {
	s.board.mu.Lock()
	members := make([]string, 0, len(t.order))
	members = append(members, t.order...)
	s.board.mu.Unlock()
	for _, n := range members {
		s.wpRemakeByName(n)
	}
}

// wpLocatorBar is isLocatorBarEnabledFor — the receiver's gamerule flag.
func (s *Server) wpLocatorBar(r *Session) bool {
	return s.gameRules["minecraft:locator_bar"] == "true"
}

// wpIconFor is locatorBarIcon.cloneAndAssignStyle: the /waypoint-modify
// color wins, else the holder's team color applies — vanilla lifts
// black to 0x303030 so the icon stays readable on the dark bar.
func (s *Server) wpIconFor(e *Entity) (style string, hasColor bool, color int32) {
	style = "minecraft:default"
	if e.WaypointStyle != "" {
		style = e.WaypointStyle
	}
	if e.WaypointColor != "" {
		return style, true, wpColorValue(e.WaypointColor)
	}
	if t := s.teamOf(s.wpScoreboardName(e)); t != nil && t.color >= 0 {
		if name := chatFormattingID[t.color]; name == "black" {
			return style, true, 0x303030
		} else if rgb, ok := wpNamedColors[name]; ok {
			return style, true, rgb
		}
	}
	return style, false, 0
}

// wpScoreboardName is the entity's team-membership key — the player name
// for player records, the dashed UUID string for everything else, like
// vanilla's getScoreboardName.
func (s *Server) wpScoreboardName(e *Entity) string {
	if e == nil {
		return ""
	}
	if sess := s.sessionByEntityID(e.ID); sess != nil {
		return sess.getName()
	}
	u := e.UUID
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// wpColorValue maps a waypoint color override to its RGB int: named chat
// colors carry the same RGB table as text colors; "#rrggbb" parses hex.
var wpNamedColors = map[string]int32{
	"black": 0x000000, "dark_blue": 0x0000AA, "dark_green": 0x00AA00,
	"dark_aqua": 0x00AAAA, "dark_red": 0xAA0000, "dark_purple": 0xAA00AA,
	"gold": 0xFFAA00, "gray": 0xAAAAAA, "dark_gray": 0x555555,
	"blue": 0x5555FF, "green": 0x55FF55, "aqua": 0x55FFFF,
	"red": 0xFF5555, "light_purple": 0xFF55FF, "yellow": 0xFFFF55,
	"white": 0xFFFFFF,
}

func wpColorValue(c string) int32 {
	if len(c) == 7 && c[0] == '#' {
		var v int32
		for i := 1; i < 7; i++ {
			d := c[i]
			var n int32
			switch {
			case d >= '0' && d <= '9':
				n = int32(d - '0')
			case d >= 'a' && d <= 'f':
				n = int32(d-'a') + 10
			case d >= 'A' && d <= 'F':
				n = int32(d-'A') + 10
			default:
				return 0
			}
			v = v*16 + n
		}
		return v
	}
	return wpNamedColors[c]
}

// wpMakeConnection is LivingEntity.makeWaypointConnectionWith: empty on
// firstTick/self/ignore, else the kind-selected connection.
func (s *Server) wpMakeConnection(r *Session, src wpSource, rx, ry, rz float64) *wpConn {
	if src.firstTick || src.id == r.entityID.Load() {
		return nil
	}
	if s.wpIgnore(src, r, rx, ry, rz) {
		return nil
	}
	e, _ := s.entities.GetPtr(src.id)
	style, hasColor, color := s.wpIconFor(e)
	conn := &wpConn{uuid: src.uuid, style: style, hasColor: hasColor, color: color}
	switch {
	case wpDistance(src, rx, ry, rz) > wpAzimuthDist:
		conn.kind = wpKindAzimuth
		conn.lastAngle = wpAzimuthAngle(src, rx, ry, rz)
	case !s.wpChunkVisible(src.chunk, r):
		conn.kind = wpKindChunk
		conn.lastChunk = src.chunk
	default:
		conn.kind = wpKindVec3i
		conn.lastPos = src.blockPos
	}
	return conn
}

// wpAzimuthAngle is EntityAzimuthConnection's emitted angle:
// atan2(diff.x, -diff.z) with diff = receiver - source.
func wpAzimuthAngle(src wpSource, rx, _, rz float64) float32 {
	dx := rx - src.pos[0]
	dz := rz - src.pos[2]
	return float32(math.Atan2(dx, -dz))
}

// wpCreateConnection is ServerWaypointManager.createConnection.
func (s *Server) wpCreateConnection(r *Session, src wpSource) {
	if src.id == r.entityID.Load() || !s.wpLocatorBar(r) {
		return
	}
	rx, ry, rz, _, _, _ := r.Pos()
	conn := s.wpMakeConnection(r, src, rx, ry, rz)
	if conn == nil {
		return
	}
	if r.wpConns == nil {
		r.wpConns = map[int32]*wpConn{}
	}
	r.wpConns[src.id] = conn
	s.wpSend(r, wpEncodeOp(0, conn))
}

// wpUpdateConnection is ServerWaypointManager.updateConnection: not
// broken -> update(); broken -> remake-or-disconnect.
func (s *Server) wpUpdateConnection(r *Session, src wpSource, conn *wpConn) {
	if src.id == r.entityID.Load() || !s.wpLocatorBar(r) {
		return
	}
	rx, ry, rz, _, _, _ := r.Pos()
	if !s.wpIsBroken(conn, src, r, rx, ry, rz) {
		s.wpConnUpdate(conn, src, r, rx, ry, rz)
		return
	}
	next := s.wpMakeConnection(r, src, rx, ry, rz)
	if next == nil {
		s.wpSend(r, wpEncodeOp(1, conn))
		delete(r.wpConns, src.id)
		return
	}
	r.wpConns[src.id] = next
	s.wpSend(r, wpEncodeOp(0, next))
}

// wpIsBroken is the per-kind Connection.isBroken.
func (s *Server) wpIsBroken(conn *wpConn, src wpSource, r *Session, rx, ry, rz float64) bool {
	switch conn.kind {
	case wpKindVec3i:
		m := int32(0)
		for i := 0; i < 3; i++ {
			d := src.blockPos[i] - conn.lastPos[i]
			if d < 0 {
				d = -d
			}
			m += d
		}
		return m > 1 || s.wpIgnore(src, r, rx, ry, rz)
	case wpKindChunk:
		dx := src.chunk[0] - conn.lastChunk[0]
		if dx < 0 {
			dx = -dx
		}
		dz := src.chunk[1] - conn.lastChunk[1]
		if dz < 0 {
			dz = -dz
		}
		chess := dx
		if dz > chess {
			chess = dz
		}
		return chess > 1 || s.wpIgnore(src, r, rx, ry, rz) || s.wpChunkVisible(conn.lastChunk, r)
	case wpKindAzimuth:
		return s.wpIgnore(src, r, rx, ry, rz) ||
			s.wpChunkVisible(src.chunk, r) ||
			wpDistance(src, rx, ry, rz) <= wpAzimuthDist
	}
	return true
}

// wpConnUpdate is Connection.update: emit UPDATE when the kind's
// contents moved.
func (s *Server) wpConnUpdate(conn *wpConn, src wpSource, r *Session, rx, ry, rz float64) {
	switch conn.kind {
	case wpKindVec3i:
		if src.blockPos != conn.lastPos {
			conn.lastPos = src.blockPos
			s.wpSend(r, wpEncodeOp(2, conn))
		}
	case wpKindChunk:
		if src.chunk != conn.lastChunk {
			conn.lastChunk = src.chunk
			s.wpSend(r, wpEncodeOp(2, conn))
		}
	case wpKindAzimuth:
		angle := wpAzimuthAngle(src, rx, ry, rz)
		if d := angle - conn.lastAngle; d > wpAzimuthEpsilon || d < -wpAzimuthEpsilon {
			conn.lastAngle = angle
			s.wpSend(r, wpEncodeOp(2, conn))
		}
	}
}

// wpSend writes one tracked_waypoint packet to the receiver.
func (s *Server) wpSend(r *Session, body []byte) {
	_ = r.write(r.ids().PlayWaypoint, body)
}

// wpEncodeOp builds the packet body. op 1 (UNTRACK) is the bare
// identifier; ops 0/2 append the icon and the kind's contents.
func wpEncodeOp(op int32, conn *wpConn) []byte {
	b := &protocol.Buffer{}
	b.VarInt(op)
	b.Bool(true) // identifier Either: left = UUID
	b.UUID(conn.uuid)
	if op == 1 {
		return b.Bytes()
	}
	b.String(conn.style)
	b.Bool(conn.hasColor)
	if conn.hasColor {
		// RGB_COLOR is 3 bytes, not an int32.
		b.Byte(byte(conn.color >> 16))
		b.Byte(byte(conn.color >> 8))
		b.Byte(byte(conn.color))
	}
	b.VarInt(conn.kind)
	switch conn.kind {
	case wpKindVec3i:
		b.VarInt(conn.lastPos[0])
		b.VarInt(conn.lastPos[1])
		b.VarInt(conn.lastPos[2])
	case wpKindChunk:
		b.VarInt(conn.lastChunk[0])
		b.VarInt(conn.lastChunk[1])
	case wpKindAzimuth:
		b.Float(conn.lastAngle)
	}
	return b.Bytes()
}

// wpDropAll disconnects every waypoint connection of a leaving receiver
// (removePlayer) — the receiver is gone so nothing is emitted; the
// transmitter's other receivers drop it via wpResolve's dead-source path.
func (s *Server) wpDropAll(r *Session) {
	r.wpMu.Lock()
	r.wpConns = nil
	r.wpMu.Unlock()
}
