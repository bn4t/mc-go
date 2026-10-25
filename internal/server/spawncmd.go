package server

import (
	"math"
	"strconv"
	"strings"

	"mc-go/internal/protocol"
)

// registerSpawnCommands wires /spawnpoint and /setworldspawn.
func (s *Server) registerSpawnCommands() {
	s.registerComponentCommand("spawnpoint", func(player string, args []string) reply {
		return s.runSpawnpoint(player, args)
	})
	s.registerComponentCommand("setworldspawn", func(player string, args []string) reply {
		return s.runSetWorldSpawn(player, args)
	})
}

// parseBlockPos parses three block-position args (ints or ~relative)
// starting at args[0] of the sub-list, where tokBase is the input token
// index of args[0]. A partial position fails pos3d.incomplete at the
// first position token; a bad int fails parsing.int.expected (bad
// literal) or parsing.int.invalid (overflows int32) at its token.
func (s *Server) parseBlockPos(sender *Session, args []string, tokBase int) ([3]int, *reply) {
	if len(args) < 3 {
		r := errToken("argument.pos3d.incomplete", tokBase)
		return [3]int{}, &r
	}
	var base [3]float64
	if sender != nil {
		x, y, z, _, _, _ := sender.Pos()
		base = [3]float64{math.Floor(x), math.Floor(y), math.Floor(z)}
	}
	var pos [3]int
	for i := 0; i < 3; i++ {
		tok := args[i]
		rel := false
		v := tok
		if strings.HasPrefix(v, "~") {
			rel = true
			v = v[1:]
		}
		n := 0
		if v != "" {
			num, err := strconv.ParseInt(v, 10, 32)
			if err != nil {
				var r reply
				if _, perr := strconv.ParseInt(v, 10, 64); perr == nil {
					// Parsed past int32: invalid with the literal back.
					r = errToken("parsing.int.invalid", tokBase+i, tok)
				} else {
					r = errToken("parsing.int.expected", tokBase+i)
				}
				return [3]int{}, &r
			}
			n = int(num)
		}
		if rel {
			n += int(base[i])
		}
		pos[i] = n
	}
	return pos, nil
}

// parseRotation parses the yaw+pitch pair, tokens tokBase and
// tokBase+1. One value alone fails rotation.incomplete at it; a
// non-double fails parsing.double.expected at its token. ~ is
// relative to the sender's view angles.
func (s *Server) parseRotation(sender *Session, args []string, tokBase int) (float32, float32, *reply) {
	var baseYaw, basePitch float32
	if sender != nil {
		_, _, _, baseYaw, basePitch, _ = sender.Pos()
	}
	parse := func(tok string, base float32, tokIdx int) (float32, *reply) {
		v := tok
		rel := false
		if strings.HasPrefix(v, "~") {
			rel = true
			v = v[1:]
		}
		f := float64(0)
		if v != "" {
			var err error
			f, err = strconv.ParseFloat(v, 64)
			if err != nil {
				r := errToken("parsing.double.expected", tokIdx)
				return 0, &r
			}
		}
		if rel {
			f += float64(base)
		}
		return float32(f), nil
	}
	yaw, r := parse(args[0], baseYaw, tokBase)
	if r != nil {
		return 0, 0, r
	}
	if len(args) < 2 {
		rr := errToken("argument.rotation.incomplete", tokBase)
		return 0, 0, &rr
	}
	pitch, r := parse(args[1], basePitch, tokBase+1)
	if r != nil {
		return 0, 0, r
	}
	if len(args) > 2 {
		rr := errToken("command.unknown.argument", tokBase+2)
		return 0, 0, &rr
	}
	return yaw, pitch, nil
}

// spawnReply builds the success component both commands share: block
// pos ints, yaw/pitch floats, dimension name — and for spawnpoint the
// decorated target name appended last (the single-target form).
func spawnReply(key string, pos [3]int, yaw, pitch float32, dimName string, with ...any) reply {
	args := []any{
		nestedCompound{key: "", value: int32(pos[0])},
		nestedCompound{key: "", value: int32(pos[1])},
		nestedCompound{key: "", value: int32(pos[2])},
		nestedCompound{key: "", value: yaw},
		nestedCompound{key: "", value: pitch},
		nestedCompound{key: "", value: dimName},
	}
	args = append(args, with...)
	return opsEchoReply(translatable(key, args...))
}

// runSpawnpoint implements /spawnpoint [targets] [pos] [yaw pitch]:
// the targets' per-player respawn point is set and the reply reports
// it. No packet is sent — the point takes effect on respawn.
func (s *Server) runSpawnpoint(player string, args []string) reply {
	targets, r := s.spawnTargets(player, args)
	if r != nil {
		return *r
	}
	rest := args
	if len(args) > 0 {
		rest = args[1:]
	}
	sender := s.findSessionByName(player)
	var pos [3]int
	var yaw, pitch float32
	switch {
	case len(rest) == 0:
		if sender != nil {
			x, y, z, _, _, _ := sender.Pos()
			pos = [3]int{int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))}
		}
	case len(rest) >= 3:
		var pr *reply
		pos, pr = s.parseBlockPos(sender, rest, 2)
		if pr != nil {
			return *pr
		}
		if len(rest) > 3 {
			var rr *reply
			yaw, pitch, rr = s.parseRotation(sender, rest[3:], 5)
			if rr != nil {
				return *rr
			}
		}
	default:
		return errToken("argument.pos3d.incomplete", 2)
	}
	dimName := "minecraft:overworld"
	if sender != nil {
		dimName = sender.dimName()
	}
	for _, t := range targets {
		t.posMu.Lock()
		t.spawnPos = pos
		t.spawnYaw, t.spawnPitch = yaw, pitch
		t.spawnDim = dimName
		t.hasSpawnPos = true
		t.posMu.Unlock()
	}
	if len(targets) == 1 {
		return spawnReply("commands.spawnpoint.success.single", pos, yaw, pitch, dimName,
			playerChatName(targets[0]))
	}
	// The multi-target form is unmeasured — one-player worlds never
	// reach it — so report the count in place of the name.
	return spawnReply("commands.spawnpoint.success.multiple", pos, yaw, pitch, dimName,
		nestedCompound{key: "", value: int32(len(targets))})
}

// spawnTargets resolves the optional leading targets arg of
// /spawnpoint: absent is the sender, present is the players the
// selector or name resolves to. tokIdx of the target arg is 1.
func (s *Server) spawnTargets(player string, args []string) ([]*Session, *reply) {
	if len(args) == 0 {
		sess := s.findSessionByName(player)
		if sess == nil {
			r := componentReply(commandError("argument.entity.notfound.player"))
			return nil, &r
		}
		return []*Session{sess}, nil
	}
	return s.playerTargets(player, args, 1)
}

// runSetWorldSpawn implements /setworldspawn [pos] [yaw pitch]: moves
// the world spawn and fans Set Default Spawn Position out.
func (s *Server) runSetWorldSpawn(player string, args []string) reply {
	sender := s.findSessionByName(player)
	var pos [3]int
	var yaw, pitch float32
	switch {
	case len(args) == 0:
		if sender != nil {
			x, y, z, _, _, _ := sender.Pos()
			pos = [3]int{int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))}
		}
	case len(args) >= 3:
		var pr *reply
		pos, pr = s.parseBlockPos(sender, args, 1)
		if pr != nil {
			return *pr
		}
		if len(args) > 3 {
			var rr *reply
			yaw, pitch, rr = s.parseRotation(sender, args[3:], 4)
			if rr != nil {
				return *rr
			}
		}
	default:
		return errToken("argument.pos3d.incomplete", 1)
	}
	dimName := "minecraft:overworld"
	if sender != nil {
		dimName = sender.dimName()
	}
	// The real server fans Set Default Spawn Position out only when the
	// stored point actually moved.
	if s.setWorldSpawnAngles(pos, yaw, pitch) {
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay {
				return true
			}
			var b protocol.Buffer
			b.String(dimName)
			b.Position(int32(pos[0]), int32(pos[1]), int32(pos[2]))
			b.Float(yaw)
			b.Float(pitch)
			_ = sess.write(sess.ids().PlayDefaultSpawn, b.Bytes())
			return true
		})
	}
	return spawnReply("commands.setworldspawn.success", pos, yaw, pitch, dimName)
}
