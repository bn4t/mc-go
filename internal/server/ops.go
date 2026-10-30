package server

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"mc-go/internal/world"
)

// Vanilla-subset operator commands: /tp, /kick, /save-all, /stop.
// Registered alongside the building-block commands in registerBuiltins;
// plugin commands may override any of these names via Commands.Register.
//
// Permissions are intentionally open: every player may run them, matching
// a single-operator local server. An ops/permission milestone will gate
// these; until then the open behavior is documented, not accidental.

// registerOps adds the operator command set.
func (s *Server) registerOps() {
	s.registerComponentCommand("tp", func(player string, args []string) reply {
		return s.runTp(player, args)
	})
	s.registerComponentCommand("teleport", func(player string, args []string) reply {
		return s.runTp(player, args)
	})
	s.registerComponentCommand("save-all", func(player string, args []string) reply {
		return s.runSaveAll(player, args)
	})
}

// parseDouble resolves one teleport axis: "12", "12.5", "~", "~-3.5".
// The base is the entity's exact position — vanilla's "~" moves from
// the player's fractional spot, not the block floor. center is the
// vec arg's centerCorrect flag: a bare integer token (no '.' and not
// relative) means the block column and lands on its center (+0.5).
// Vanilla centers the x and z of vec3() arguments — the y slot is
// always parsed with centerCorrect=false, so y never centers.
func parseDouble(tok string, base float64, center bool) (float64, error) {
	if tok == "~" {
		return base, nil
	}
	if strings.HasPrefix(tok, "~") {
		off, err := strconv.ParseFloat(strings.TrimPrefix(tok, "~"), 64)
		if err != nil {
			return 0, err
		}
		return base + off, nil
	}
	v, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return 0, err
	}
	if center && !strings.Contains(tok, ".") {
		v += 0.5
	}
	return v, nil
}

// baseExactPos returns the player's exact position for "~" resolution
// in teleport-style arguments, falling back to world spawn.
func (s *Server) baseExactPos(player string) (float64, float64, float64) {
	if sess := s.findSessionByName(player); sess != nil {
		// Inside an execute chain ~ resolves at the chain origin —
		// the sender's spot for `as`, the entity's for `at`/
		// positioned — same precedence as baseBlockPos.
		if sess.execPos != nil {
			p := sess.execPos
			return p[0], p[1], p[2]
		}
		if sess.execEnt != nil && sess.execEnt.mob != nil {
			m := sess.execEnt.mob
			return m.X, m.Y, m.Z
		}
		x, y, z, _, _, _ := sess.Pos()
		return x, y, z
	}
	return s.WorldSpawn()
}

// tpReply announces a teleport the way vanilla's sendSuccess does: the
// issuer gets the translatable component, the other operators a
// gray-italic chat.type.admin relay.
func (s *Server) tpReply(issuer string, msg component) reply {
	return opsEchoReply(msg)
}

// tpCoords renders the coordinate arguments the way vanilla's
// formatDouble does: String.format("%f"), always six decimals.
func tpCoords(x, y, z float64) (string, string, string) {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }
	return f(x), f(y), f(z)
}

// runTp mirrors vanilla's /tp brigadier tree:
//
//	tp → vec3(self) | entity(dest) | entities(targets)
//	     targets → vec3(→ rotation → facing) | entity(dest)
//
// Brigadier tries children in registration order; every child that
// parses but leaves input is a "potential" whose reader rests where
// its subtree stopped. Complete parses sort first, then error-free,
// then registration order. With no potentials the sole child error is
// thrown, else command.unknown.argument at the literal's rest
// position (token 1).
type tpPot struct {
	rest     int // token index where this partial parse stops
	complete bool
	exec     func() reply // nil → parse consumed a commandless node
	errs     int
	err      *reply // the single throwable exception inside, if any
}

func tpBest(pots []tpPot) (tpPot, bool) {
	if len(pots) == 0 {
		return tpPot{}, false
	}
	best := 0
	for i := 1; i < len(pots); i++ {
		a, b := pots[best], pots[i]
		if b.complete && !a.complete || a.complete == b.complete && b.errs < a.errs {
			best = i
		}
	}
	return pots[best], true
}

func (s *Server) runTp(player string, args []string) reply {
	n := len(args)
	if n == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	var pots []tpPot
	var errs []*reply
	addErr := func(r *reply) {
		if r != nil {
			errs = append(errs, r)
		}
	}
	mark := func(r reply) *reply { return &r }
	// vec3 needs args[i..i+2] all coordinate tokens, and ^-locals
	// cannot mix with the other kinds.
	vec3 := func(i int) *reply {
		if i+2 >= n {
			return mark(errTokenEnd("parsing.double.expected", n))
		}
		local := strings.HasPrefix(args[i], "^") ||
			strings.HasPrefix(args[i+1], "^") || strings.HasPrefix(args[i+2], "^")
		for j := 0; j < 3; j++ {
			tok := args[i+j]
			if strings.HasPrefix(tok, "^") != local {
				return mark(errToken("argument.pos.mixed", i+j+1))
			}
			if !tpCoordToken(tok) {
				return mark(errToken("parsing.double.expected", i+j+1))
			}
		}
		return nil
	}
	// entity arg; single forbids selectors that may match >1 target,
	// exactly like vanilla's entity() argument rejecting @e[limit=2].
	entity := func(i int, single bool) *reply {
		tok := args[i]
		if len(tok) > 0 && tok[0] == '@' {
			spec, serr := parseSelector(tok, i+1)
			if serr != nil {
				return serr
			}
			if single {
				maxRes := 1
				if spec.variable == 'e' || spec.variable == 'a' {
					maxRes = 1 << 30
				}
				if spec.hasLimit {
					maxRes = spec.limit
				}
				if maxRes > 1 {
					return mark(errToken("argument.entity.toomany", i+1))
				}
			}
		}
		return nil
	}
	rotation := func(i int) *reply {
		if i+1 >= n {
			return mark(errTokenEnd("parsing.float.expected", n))
		}
		for j := 0; j < 2; j++ {
			if !tpFloatToken(args[i+j]) {
				return mark(errToken("parsing.float.expected", i+j+1))
			}
		}
		return nil
	}

	// Child 1: vec3 self-location — a leaf with no children.
	if r := vec3(0); r != nil {
		addErr(r)
	} else if n == 3 {
		pots = append(pots, tpPot{complete: true,
			exec: func() reply { return s.tpToPos(player, "", args[0:3], nil) }})
	} else {
		pots = append(pots, tpPot{rest: 4})
	}

	// Child 2: single-entity destination — leaf.
	if r := entity(0, true); r != nil {
		addErr(r)
	} else if n == 1 {
		pots = append(pots, tpPot{complete: true,
			exec: func() reply { return s.tpToEntity(player, "", args[0]) }})
	} else {
		pots = append(pots, tpPot{rest: 2})
	}

	// Child 3: multi-entity targets with children vec3 and entity.
	if r := entity(0, false); r != nil {
		addErr(r)
	} else if n == 1 {
		pots = append(pots, tpPot{complete: true}) // leaf has no command
	} else {
		var inner []tpPot
		var innerErrs []*reply
		if r := vec3(1); r != nil {
			innerErrs = append(innerErrs, r)
		} else if n == 4 {
			inner = append(inner, tpPot{complete: true,
				exec: func() reply { return s.tpToPos(player, args[0], args[1:4], nil) }})
		} else {
			if rr := rotation(4); rr != nil {
				inner = append(inner, tpPot{rest: 5, errs: 1, err: rr})
			} else if n == 6 {
				inner = append(inner, tpPot{complete: true,
					exec: func() reply { return s.tpToPos(player, args[0], args[1:4], args[4:6]) }})
			} else {
				// Leftover tokens past <rot> — vanilla's tp tree ends
				// at rotation (no `facing` node here in 26.2).
				inner = append(inner, tpPot{rest: 7})
			}
		}
		if r := entity(1, true); r != nil {
			innerErrs = append(innerErrs, r)
		} else if n == 2 {
			inner = append(inner, tpPot{complete: true,
				exec: func() reply { return s.tpToEntity(player, args[0], args[1]) }})
		} else {
			inner = append(inner, tpPot{rest: 3})
		}
		if best, ok := tpBest(inner); ok {
			pots = append(pots, best)
		} else {
			p := tpPot{rest: 2, errs: len(innerErrs)}
			if len(innerErrs) == 1 {
				p.err = innerErrs[0]
			}
			pots = append(pots, p)
		}
	}

	best, ok := tpBest(pots)
	if !ok {
		if len(errs) == 1 {
			return *errs[0]
		}
		return errToken("command.unknown.argument", 1)
	}
	if best.complete {
		if best.exec == nil {
			return errTokenEnd("command.unknown.command", n)
		}
		return best.exec()
	}
	if best.err != nil && best.errs == 1 {
		return *best.err
	}
	return errToken("command.unknown.argument", best.rest)
}

// tpCoordToken accepts a vec3 component: absolute double, ~-relative
// or ^-local.
func tpCoordToken(tok string) bool {
	if tok == "" {
		return false
	}
	if tok[0] == '~' || tok[0] == '^' {
		if len(tok) == 1 {
			return true
		}
		_, err := strconv.ParseFloat(tok[1:], 64)
		return err == nil
	}
	_, err := strconv.ParseFloat(tok, 64)
	return err == nil
}

// tpFloatToken accepts a rotation component: float or ~-relative.
func tpFloatToken(tok string) bool {
	if tok == "" {
		return false
	}
	if tok[0] == '~' && len(tok) > 1 {
		tok = tok[1:]
	}
	_, err := strconv.ParseFloat(tok, 64)
	return err == nil || tok == "~"
}

// tpResolveVictims resolves the targets argument at exec: selector
// machinery for @-forms, name/UUID lookups otherwise.
func (s *Server) tpResolveVictims(sender *Session, tok string, tokIdx int) ([]*dataEntity, *reply) {
	if len(tok) > 0 && tok[0] == '@' {
		ds, serr := s.selectDataEntities(sender, tok, tokIdx, false, nil)
		if serr != nil {
			return nil, serr
		}
		if len(ds) == 0 {
			r := componentReply(commandError("argument.entity.notfound.entity"))
			return nil, &r
		}
		return ds, nil
	}
	if sess := s.findSessionByName(tok); sess != nil {
		return []*dataEntity{{sess: sess}}, nil
	}
	if vs := s.sessionUUIDs(tok); len(vs) > 0 {
		return []*dataEntity{{sess: vs[0]}}, nil
	}
	r := componentReply(commandError("argument.entity.notfound.entity"))
	return nil, &r
}

// sessionUUIDs lists sessions whose UUID string matches the token.
func (s *Server) sessionUUIDs(tok string) []*Session {
	if len(tok) != 36 {
		return nil
	}
	var out []*Session
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		u := sess.getUUID()
		if fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16]) == tok {
			out = append(out, sess)
		}
		return true
	})
	return out
}

// tpResolveDest resolves a single-entity destination argument.
func (s *Server) tpResolveDest(sender *Session, tok string, tokIdx int) (*dataEntity, *reply) {
	if len(tok) > 0 && tok[0] == '@' {
		ds, serr := s.selectDataEntities(sender, tok, tokIdx, true, nil)
		if serr != nil {
			return nil, serr
		}
		if len(ds) != 1 {
			r := componentReply(commandError("argument.entity.notfound.entity"))
			return nil, &r
		}
		return ds[0], nil
	}
	if sess := s.findSessionByName(tok); sess != nil {
		return &dataEntity{sess: sess}, nil
	}
	if vs := s.sessionUUIDs(tok); len(vs) == 1 {
		return &dataEntity{sess: vs[0]}, nil
	}
	r := componentReply(commandError("argument.entity.notfound.entity"))
	return nil, &r
}

// tpComp renders a dataEntity as the command-feedback target component.
func (s *Server) tpComp(d *dataEntity) component {
	if d.sess != nil {
		return s.entityTargetComponent(d.sess)
	}
	return entityDisplayName(d.mob)
}

// tpToEntity is `tp <dest>` (victimsTok empty = issuer) and
// `tp <victims> <dest>`.
func (s *Server) tpToEntity(player, victimsTok, destTok string) reply {
	issuer := s.findSessionByName(player)
	victims, r := s.tpVictims(issuer, victimsTok)
	if r != nil {
		return *r
	}
	dest, r := s.tpResolveDest(issuer, destTok, 2)
	if r != nil {
		return *r
	}
	dx, dy, dz, _, _ := dest.posRot()
	// A tp-to-entity crosses dimensions when the destination entity
	// stands in another one — vanilla resolves targetLevel off the
	// destination, not the source.
	tdim := ""
	if dest.sess != nil {
		tdim = dest.sess.dimName()
	} else if dest.mob != nil {
		tdim = dest.mob.Dim
	}
	for _, v := range victims {
		s.tpMove(v, dx, dy, dz, nil, tdim, 0)
	}
	if len(victims) == 1 {
		return s.tpReply(player, translatable("commands.teleport.success.entity.single",
			s.tpComp(victims[0]), s.tpComp(dest)))
	}
	return s.tpReply(player, translatable("commands.teleport.success.entity.multiple",
		int32(len(victims)), s.tpComp(dest)))
}

func (s *Server) tpVictims(issuer *Session, victimsTok string) ([]*dataEntity, *reply) {
	if victimsTok == "" {
		if issuer == nil {
			r := componentReply(commandError("argument.entity.notfound.entity"))
			return nil, &r
		}
		return []*dataEntity{{sess: issuer}}, nil
	}
	return s.tpResolveVictims(issuer, victimsTok, 1)
}

// tpToPos is `tp [victims] <x> <y> <z> [<yaw> <pitch>]` — coordinates
// resolve against the source, rotation components against each
// victim's own rotation when ~-relative.
func (s *Server) tpToPos(player, victimsTok string, coords, rot []string) reply {
	issuer := s.findSessionByName(player)
	victims, r := s.tpVictims(issuer, victimsTok)
	if r != nil {
		return *r
	}
	// Coordinates resolve against the command source's position.
	bx, by, bz := s.baseExactPos(player)
	rotation := s.functionSource(player).rotation
	x, y, z := tpResolveSourceCoords(coords, bx, by, bz, rotation)
	if !tpSpawnable(x, y, z) {
		return componentReply(commandError("commands.teleport.invalidPosition"))
	}
	var rotFn func(vyaw, vpitch float32) (float32, float32)
	if rot != nil {
		// Each component is absolute, or ~-relative to the victim's own
		// rotation (Relative.Y_ROT/X_ROT in vanilla's performTeleport).
		rotFn = func(vyaw, vpitch float32) (float32, float32) {
			yaw, pitch := vyaw, vpitch
			vals := [2]float64{}
			for i := 0; i < 2; i++ {
				vals[i], _ = strconv.ParseFloat(strings.TrimPrefix(rot[i], "~"), 64)
			}
			if strings.HasPrefix(rot[0], "~") {
				yaw += float32(vals[0])
			} else {
				yaw = float32(vals[0])
			}
			if strings.HasPrefix(rot[1], "~") {
				pitch += float32(vals[1])
			} else {
				pitch = float32(vals[1])
			}
			return tpWrapDeg(yaw), tpWrapDeg(pitch)
		}
	}
	// teleportTo resolves its target level off the command source —
	// under `execute in <dim>` that is the forked dimension (the
	// issuer's swapped dim), not each victim's own.
	tdim := ""
	if issuer != nil {
		tdim = issuer.dimName()
	}
	for _, vic := range victims {
		// The relatives set is resolved per victim: vanilla's isDelta
		// arg is each entity's dimension == the target level's, which
		// decides whether position() bits join the direction() ones.
		vdim := ""
		if vic.sess != nil {
			vdim = vic.sess.dimName()
		} else if vic.mob != nil {
			vdim = vic.mob.Dim
		}
		s.tpMove(vic, x, y, z, rotFn, tdim, tpRelMask(coords, rot, vdim == tdim))
	}
	sx, sy, sz := tpCoords(x, y, z)
	xcomp := nestedCompound{key: "", value: sx}
	ycomp := nestedCompound{key: "", value: sy}
	zcomp := nestedCompound{key: "", value: sz}
	if len(victims) == 1 {
		return s.tpReply(player, translatable("commands.teleport.success.location.single",
			s.tpComp(victims[0]), xcomp, ycomp, zcomp))
	}
	return s.tpReply(player, translatable("commands.teleport.success.location.multiple",
		int32(len(victims)), xcomp, ycomp, zcomp))
}

// tpSpawnable mirrors Level.isInSpawnableBounds: horizontal within
// ±30M blocks and y inside [-20M, 20M).
func tpSpawnable(x, y, z float64) bool {
	fx, fy, fz := math.Floor(x), math.Floor(y), math.Floor(z)
	return fy >= -20000000 && fy < 20000000 &&
		fx > -30000000 && fx < 30000000 &&
		fz > -30000000 && fz < 30000000
}

// tpResolveCoords turns three vec3 tokens into absolute coordinates:
// doubles, ~-relative to the source position, or ^-local in the
// source's look frame.
func tpResolveCoords(toks []string, bx, by, bz float64, issuer *Session) (x, y, z float64) {
	var rotation [2]float32
	if issuer != nil {
		_, _, _, yaw, pitch, _ := issuer.Pos()
		rotation = [2]float32{yaw, pitch}
	}
	return tpResolveSourceCoords(toks, bx, by, bz, rotation)
}

func tpResolveSourceCoords(toks []string, bx, by, bz float64, rotation [2]float32) (x, y, z float64) {
	if strings.HasPrefix(toks[0], "^") {
		yaw, pitch := float64(rotation[0]), float64(rotation[1])
		yr, pr := yaw*math.Pi/180, pitch*math.Pi/180
		fwd := [3]float64{-math.Sin(yr) * math.Cos(pr), -math.Sin(pr), math.Cos(yr) * math.Cos(pr)}
		left := [3]float64{math.Cos(yr), 0, math.Sin(yr)}
		up := [3]float64{
			fwd[1]*left[2] - fwd[2]*left[1],
			fwd[2]*left[0] - fwd[0]*left[2],
			fwd[0]*left[1] - fwd[1]*left[0],
		}
		var amts [3]float64
		for i := 0; i < 3; i++ {
			if len(toks[i]) > 1 {
				amts[i], _ = strconv.ParseFloat(toks[i][1:], 64)
			}
		}
		return bx + left[0]*amts[0] + up[0]*amts[1] + fwd[0]*amts[2],
			by + left[1]*amts[0] + up[1]*amts[1] + fwd[1]*amts[2],
			bz + left[2]*amts[0] + up[2]*amts[1] + fwd[2]*amts[2]
	}
	x, _ = parseDouble(toks[0], bx, true)
	y, _ = parseDouble(toks[1], by, false)
	z, _ = parseDouble(toks[2], bz, true)
	return x, y, z
}

// Vanilla's Relative bitmask, as carried in teleport_entity's
// relatives int: bits 0-2 are the position() set (only the
// facing/delta command forms set it), bits 3-4 the rotation() set
// (also implied when no rotation args are given, so the entity keeps
// its own facing), bits 5-7 the direction() set for ~-relative and
// ^-local coordinates, bit 8 ROTATE_DELTA.
const (
	relPosX        int32 = 1
	relPosY        int32 = 2
	relPosZ        int32 = 4
	relYRot        int32 = 8
	relXRot        int32 = 16
	relDeltaX      int32 = 32
	relDeltaY      int32 = 64
	relDeltaZ      int32 = 128
	relRotateDelta int32 = 256
)

// tpRelMask builds the relatives set TeleportCommand.getRelatives
// produces for a positional tp: direction() bits for each ~-relative
// or ^-local axis, position() bits on the same axes when the move
// stays in one dimension (the isDelta arg is the same-dimension
// check), and rotation() — which is the whole ROTATION set when the
// command carried no rotation argument at all.
func tpRelMask(coords, rot []string, sameDim bool) int32 {
	m := int32(0)
	for i, tok := range coords {
		if i >= 3 {
			break
		}
		if len(tok) > 0 && (tok[0] == '~' || tok[0] == '^') {
			m |= relDeltaX << i
			if sameDim {
				m |= relPosX << i
			}
		}
	}
	if rot == nil {
		m |= relYRot | relXRot
	} else {
		if len(rot) > 0 && strings.HasPrefix(rot[0], "~") {
			m |= relYRot
		}
		if len(rot) > 1 && strings.HasPrefix(rot[1], "~") {
			m |= relXRot
		}
	}
	return m
}

// tpMove applies a resolved teleport to one entity: sessions go
// through the player teleport path, mobs move and broadcast. tdim is
// the teleport's target level — the execute context's dimension.
func (s *Server) tpMove(v *dataEntity, x, y, z float64,
	rotFn func(vyaw, vpitch float32) (float32, float32), tdim string,
	relMask int32,
) {
	var yaw, pitch float32
	if v.sess != nil {
		_, _, _, yaw, pitch, _ = v.sess.Pos()
	} else {
		yaw, pitch = v.mob.Yaw, v.mob.Pitch
	}
	if rotFn != nil {
		yaw, pitch = rotFn(yaw, pitch)
	}
	if v.sess != nil {
		s.teleportPlayerRot(v.sess.getName(), x, y, z, yaw, pitch)
		return
	}
	e := v.mob
	if tdim != "" && tdim != e.Dim {
		s.tpCrossDimension(e, tdim, x, y, z, yaw, pitch)
		return
	}
	s.tpMountTeleport(e, x, y, z, yaw, pitch, relMask)
}

// tpMountTeleport is Entity.teleport's same-dimension branch: a mount
// leaves its own vehicle first (stopRiding on the non-passenger arm),
// each direct passenger teleports through its own calculated
// transition, the mount then repositions, and
// sendTeleportTransitionToRidingPlayers emits teleport_entity to every
// player anywhere in the rider stack so client-side riding keeps the
// mount's new spot glued.
func (s *Server) tpMountTeleport(e *Entity, x, y, z float64, yaw, pitch float32, relMask int32) {
	s.tpTeleportTransition(e, x, y, z, yaw, pitch, relMask, false)
}

// tpTeleportTransition is the shared same-dimension transition body,
// parameterized by whether the moving entity itself rides as a
// passenger (nested passenger teleports skip the dismount and the
// rider notifications — those fire once at the outermost mount).
func (s *Server) tpTeleportTransition(e *Entity, x, y, z float64, yaw, pitch float32, relMask int32, asPassenger bool) {
	if !asPassenger {
		s.tpDismountVehicle(e)
	}
	mx, my, mz, myaw, mpitch := e.X, e.Y, e.Z, e.Yaw, e.Pitch
	for _, pid := range append([]int32(nil), e.Passengers...) {
		s.tpPassengerTransition(e, pid, x, y, z, yaw, pitch, relMask, mx, my, mz, myaw, mpitch)
	}
	e.X, e.Y, e.Z, e.Yaw, e.Pitch = x, y, z, yaw, pitch
	e.knownSpeedInit = false // Teleport resets the next position baseline, not the cached value.
	// Vanilla's teleportSetPosition just repositions — the tracker
	// emits entity_position_sync on the next sendChanges (its
	// teleportDelay branch), never from the command itself —
	// PosDirty is vanilla's needsSync, forcing sendChanges next tick.
	e.teleportDelay = 401
	e.PosDirty = true
	e.NeedsSync = true
	if !asPassenger {
		s.tpNotifyRidingPlayers(e, relMask, mx, my, mz, myaw, mpitch)
	}
}

// tpDismountVehicle is Entity.stopRiding for a teleported mount: it
// drops out of its own vehicle's passenger list and lands vanilla's
// 60-tick boarding cooldown.
func (s *Server) tpDismountVehicle(e *Entity) {
	veh := s.vehicleOf(e.ID)
	if veh == nil {
		return
	}
	keep := veh.Passengers[:0]
	for _, p := range veh.Passengers {
		if p != e.ID {
			keep = append(keep, p)
		}
	}
	veh.Passengers = append([]int32(nil), keep...)
	s.broadcastSetPassengers(veh)
	e.Riding = 0
	e.BoardingCooldown = 60
}

// tpPassengerTransition teleports one direct passenger the way
// calculatePassengerTransition computes it. The two arms collapse
// to the same outcome on every axis: a position-relative component
// composes rider.old + (transition − mount.old) on the client, while
// an absolute component is transition.pos + the rider's seat offset —
// both reduce to old + mountDelta. Rotation slides the same way.
func (s *Server) tpPassengerTransition(mount *Entity, pid int32, x, y, z float64, yaw, pitch float32, relMask int32, mx, my, mz float64, myaw, mpitch float32) {
	if psess := s.sessionForEntityID(pid); psess != nil {
		px, py, pz, pyaw, ppitch, _ := psess.Pos()
		s.teleportPlayerRot(psess.getName(),
			px+x-mx, py+y-my, pz+z-mz,
			pyaw+yaw-myaw, ppitch+pitch-mpitch)
		return
	}
	rp, ok := s.entities.GetPtr(pid)
	if !ok {
		return
	}
	s.tpTeleportTransition(rp,
		rp.X+x-mx, rp.Y+y-my, rp.Z+z-mz,
		rp.Yaw+yaw-myaw, rp.Pitch+pitch-mpitch, relMask, true)
}

// tpNotifyRidingPlayers is sendTeleportTransitionToRidingPlayers:
// every player anywhere in the rider stack receives teleport_entity
// for the mount. The controlling passenger — when the mount type has
// one — sees the transition's own PMR and relatives bitmask; every
// other rider sees the mount's post-teleport PMR with an empty set.
func (s *Server) tpNotifyRidingPlayers(e *Entity, relMask int32, ox, oy, oz float64, oyaw, opitch float32) {
	ctrl := s.tpControllingPassenger(e)
	// The transition PMR carries position() — delta-encoded on each
	// axis the relatives set marks position-relative — and the
	// transition's own rot, wrapped to ±180 like performTeleport's
	// wrapDegrees.
	px, py, pz := e.X, e.Y, e.Z
	if relMask&relPosX != 0 {
		px -= ox
	}
	if relMask&relPosY != 0 {
		py -= oy
	}
	if relMask&relPosZ != 0 {
		pz -= oz
	}
	pyaw, ppitch := e.Yaw, e.Pitch
	if relMask&relYRot != 0 {
		pyaw = wrapDegrees32(pyaw - oyaw)
	}
	if relMask&relXRot != 0 {
		ppitch = wrapDegrees32(ppitch - opitch)
	}
	ctrlPMR := encodeTeleportEntity(e.ID, px, py, pz,
		e.MotionX, e.MotionY, e.MotionZ, pyaw, ppitch, relMask, e.Grounded)
	mountPMR := encodeTeleportEntity(e.ID, e.X, e.Y, e.Z,
		e.MotionX, e.MotionY, e.MotionZ, e.Yaw, e.Pitch, 0, e.Grounded)
	stack := append([]int32(nil), e.Passengers...)
	for len(stack) > 0 {
		pid := stack[0]
		stack = stack[1:]
		// Player riders resolve first: their entity ids also live in
		// the entities map, so a GetPtr-first order would swallow them
		// as mobs and the mount hop would never reach the wire.
		if psess := s.sessionForEntityID(pid); psess != nil {
			if psess.state.Load() != StatePlay {
				continue
			}
			body := mountPMR
			if ctrl != 0 && pid == ctrl {
				body = ctrlPMR
			}
			_ = psess.write(psess.ids().PlayTeleportEntity, body)
			continue
		}
		if rp, ok := s.entities.GetPtr(pid); ok {
			stack = append(stack, rp.Passengers...)
		}
	}
}

// tpControllingPassenger is the mount's getControllingPassenger:
// the base Entity returns null, and each mountable type adds its own
// rule — a boat's first passenger when it is living, a steerable's
// first rider when saddled and holding its stick, the horse family's
// first rider when saddled, the happy_ghast's first rider when it
// wears its harness. Everything else has no controlling rider, so a
// mounted pig without a saddle reports none the way vanilla does.
func (s *Server) tpControllingPassenger(veh *Entity) int32 {
	if len(veh.Passengers) == 0 {
		return 0
	}
	first := veh.Passengers[0]
	name, ok := EntityTypeName(veh.Type)
	if !ok {
		return 0
	}
	psess := s.sessionForEntityID(first)
	if isBoatEntityName(name) {
		// AbstractBoat: firstPassenger instanceof LivingEntity. Player
		// riders are living by definition; a mob rider is living when
		// its entity record carries the flag.
		if psess != nil {
			return first
		}
		if rp, ok := s.entities.GetPtr(first); ok && rp.Living {
			return first
		}
		return 0
	}
	if psess == nil {
		return 0
	}
	switch {
	case name == "pig":
		if veh.Saddle && sessHoldsItem(psess, "carrot_on_a_stick") {
			return first
		}
	case name == "strider":
		if veh.Saddle && sessHoldsItem(psess, "warped_fungus_on_a_stick") {
			return first
		}
	case name == "happy_ghast":
		// isWearingBodyArmor + !isOnStillTimeout — mc-go latches the
		// harness on Saddle and does not model the still timeout.
		if veh.Saddle {
			return first
		}
	case name == "nautilus" || name == "zombie_nautilus":
		if veh.Saddle {
			return first
		}
	case horseVehicleNames[name]:
		if veh.Saddle {
			return first
		}
	}
	return 0
}

// horseVehicleNames is the AbstractHorse family whose
// getControllingPassenger is the saddled first player rider (camels
// inherit it too).
var horseVehicleNames = map[string]bool{
	"horse": true, "donkey": true, "mule": true,
	"llama": true, "trader_llama": true, "camel": true,
	"camel_husk": true, "skeleton_horse": true, "zombie_horse": true,
}

// sessHoldsItem is Player.isHolding(item): either hand carries it.
func sessHoldsItem(sess *Session, name string) bool {
	id, ok := world.ItemID(name)
	if !ok {
		return false
	}
	if st := sess.heldStack(); st.ID == id && st.Count > 0 {
		return true
	}
	st := sess.stackAt(slotOffhand)
	return st.ID == id && st.Count > 0
}

// sessionForEntityID resolves a session by its tracked entity id —
// the inverse of sess.entityID used to find player passengers sitting
// in a mount's passenger list.
func (s *Server) sessionForEntityID(eid int32) *Session {
	var found *Session
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.entityID.Load() == eid {
			found = sess
			return false
		}
		return true
	})
	return found
}

// tpCrossDimension is Entity.teleportCrossDimension: the entity leaves
// its level for the target dimension — passengers eject, the entity
// despawns for the old dimension's trackers and respawns for the new
// one's. When the destination holds no players the entity parks
// dormant the way vanilla's addDuringTeleport parks it inside an
// unticketed chunk: it exists, it persists, but no selector reaches it
// until a player tickets the chunk (measured: a pig tp'd into an empty
// nether is gone from @e in every dimension).
func (s *Server) tpCrossDimension(e *Entity, tdim string, x, y, z float64, yaw, pitch float32) {
	oldDim := e.Dim
	// ejectPassengers + each passenger's own calculatePassenger
	// Transition: riders hop too on vanilla, but the measurement
	// surface only pins the unmounted base case — eject at the seat.
	s.mobRemovedPassengerCleanup(e)
	e.Dim = tdim
	e.X, e.Y, e.Z, e.Yaw, e.Pitch = x, y, z, yaw, pitch
	e.Dormant = s.shouldPark(tdim)
	s.broadcastEntityRemoveIn(oldDim, nil, []int32{e.ID})
	if !e.Dormant {
		s.broadcastEntitySpawnIn(tdim, nil, *e)
	}
}

func tpWrapDeg(f float32) float32 {
	f = float32(math.Mod(float64(f), 360))
	if f < -180 {
		f += 360
	} else if f >= 180 {
		f -= 360
	}
	return f
}
