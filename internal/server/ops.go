package server

import (
	"fmt"
	"math"
	"strconv"
	"strings"
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
// the player's fractional spot, not the block floor. When center is
// set (the horizontal axes of a teleport), a bare integer means the
// block column and lands on its center — vanilla's vec3 arg centers
// x/z on 0.5 while y stays exact.
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
	if center && !strings.ContainsAny(tok, ".eE") {
		v += 0.5
	}
	return v, nil
}

// baseExactPos returns the player's exact position for "~" resolution
// in teleport-style arguments, falling back to world spawn.
func (s *Server) baseExactPos(player string) (float64, float64, float64) {
	if sess := s.findSessionByName(player); sess != nil {
		x, y, z, _, _, _ := sess.Pos()
		return x, y, z
	}
	return s.WorldSpawn()
}

// tpReply announces a teleport the way vanilla's sendSuccess does: the
// issuer gets the translatable component, the other operators a
// gray-italic chat.type.admin relay.
func (s *Server) tpReply(issuer string, msg component) reply {
	if iss := s.findSessionByName(issuer); iss != nil {
		s.broadcastAdmin(iss, msg)
	}
	return componentReply(msg)
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
		ds, serr := s.selectDataEntities(sender, tok, tokIdx, false)
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
		ds, serr := s.selectDataEntities(sender, tok, tokIdx, true)
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
func (d *dataEntity) tpComp() component {
	if d.sess != nil {
		return entityTargetComponent(d.sess)
	}
	return translatable("entity.minecraft." + d.typeName())
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
	for _, v := range victims {
		s.tpMove(v, dx, dy, dz, nil)
	}
	if len(victims) == 1 {
		return s.tpReply(player, translatable("commands.teleport.success.entity.single",
			victims[0].tpComp(), dest.tpComp()))
	}
	return s.tpReply(player, translatable("commands.teleport.success.entity.multiple",
		int32(len(victims)), dest.tpComp()))
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
	x, y, z := tpResolveCoords(coords, bx, by, bz, issuer)
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
	for _, vic := range victims {
		s.tpMove(vic, x, y, z, rotFn)
	}
	sx, sy, sz := tpCoords(x, y, z)
	xcomp := nestedCompound{key: "", value: sx}
	ycomp := nestedCompound{key: "", value: sy}
	zcomp := nestedCompound{key: "", value: sz}
	if len(victims) == 1 {
		return s.tpReply(player, translatable("commands.teleport.success.location.single",
			victims[0].tpComp(), xcomp, ycomp, zcomp))
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
	if strings.HasPrefix(toks[0], "^") {
		var yaw, pitch float64
		if issuer != nil {
			_, _, _, yw, pt, _ := issuer.Pos()
			yaw, pitch = float64(yw), float64(pt)
		}
		yr, pr := yaw*math.Pi/180, pitch*math.Pi/180
		fwd := [3]float64{-math.Sin(yr) * math.Cos(pr), -math.Sin(pr), math.Cos(yr) * math.Cos(pr)}
		left := [3]float64{math.Cos(yr), 0, -math.Sin(yr)}
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

// tpMove applies a resolved teleport to one entity: sessions go
// through the player teleport path, mobs move and broadcast.
func (s *Server) tpMove(v *dataEntity, x, y, z float64,
	rotFn func(vyaw, vpitch float32) (float32, float32),
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
		s.TeleportPlayerRot(v.sess.getName(), x, y, z, yaw, pitch)
		return
	}
	e := v.mob
	e.X, e.Y, e.Z, e.Yaw, e.Pitch = x, y, z, yaw, pitch
	s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, e.Pitch, true)
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
