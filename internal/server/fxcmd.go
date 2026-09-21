package server

// Effect commands measured against the vanilla 26.2 server jar:
// /playsound, /stopsound and /particle.
//
// playsound <sound> [<source> [<targets> [<pos> [<volume> [<pitch>
// [<minVolume>]]]]]] sends one sound packet per resolved target with
// a random seed; the sound id is never validated.
//
// stopsound <targets> [<source> [<sound>]] sends stop_sound with a
// flag byte covering the supplied fields.
//
// particle <type> [<pos> [<dx> <dy> <dz> [<speed> [<count> [<mode>
// [<targets>]]]]]]] sends level_particles to the viewers (all
// players when none are given). The particle name must be a
// particle_type registry entry; for data-carrying particles the next
// token is the option map and missing keys fail with
// particle.invalidOptions.

import (
	"math/rand"
	"strconv"
	"strings"

	"mc-go/internal/protocol"
)

var soundSources = map[string]int32{
	"master": 0, "music": 1, "record": 2, "weather": 3, "block": 4,
	"hostile": 5, "neutral": 6, "player": 7, "ambient": 8, "voice": 9,
	"ui": 10,
}

// particleOptionKeys lists required option keys for data-carrying
// particles, in the order particle.invalidOptions reports them.
var particleOptionKeys = map[string][]string{
	"block":                 {"block_state"},
	"block_crumble":         {"block_state"},
	"block_marker":          {"block_state"},
	"dust":                  {"scale", "color"},
	"dust_color_transition": {"scale", "from_color", "to_color"},
	"dust_pillar":           {"block_state"},
	"falling_dust":          {"block_state"},
	"item":                  {"item"},
	"item_cobble":           {"item"},
	"item_photon":           {"item"},
	"sculk_charge":          {"roll"},
	"shriek":                {"delay"},
	"trail":                 {"color", "duration", "target"},
	"vibration":             {"destination"},
	"entity_effect":         {"color"},
	"tinted_leaves":         {"color"},
}

func registerFxCommands(s *Server) {
	s.registerComponentCommand("playsound", func(player string, args []string) reply {
		return s.runPlaysound(player, args)
	})
	s.registerComponentCommand("stopsound", func(player string, args []string) reply {
		return s.runStopsound(player, args)
	})
	s.registerComponentCommand("particle", func(player string, args []string) reply {
		return s.runParticle(player, args)
	})
}

func floatArg(tok string) (float32, error) {
	f, err := strconv.ParseFloat(tok, 32)
	return float32(f), err
}

func senderPos(sender *Session) (float64, float64, float64) {
	if sender == nil {
		return 0, 0, 0
	}
	x, y, z, _, _, _ := sender.Pos()
	return x, y, z
}

func senderName(sender *Session) string {
	if sender == nil {
		return ""
	}
	return sender.getName()
}

func (s *Server) runPlaysound(player string, args []string) reply {
	sender := s.findSessionByName(player)
	if len(args) < 1 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	sound := bossbarID(args[0])
	cat := int32(0)
	if len(args) >= 2 {
		c, ok := soundSources[args[1]]
		if !ok {
			return errToken("command.unknown.argument", 2)
		}
		cat = c
	}
	targets := []*Session{sender}
	if len(args) >= 3 {
		t, rep := s.playerTargets(player, args[2:], 3)
		if rep != nil {
			return *rep
		}
		targets = t
	}
	px, py, pz := senderPos(sender)
	if len(args) >= 4 && len(args) < 6 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	if len(args) >= 6 {
		pos := []float64{px, py, pz}
		for k := 0; k < 3; k++ {
			v, err := parseDouble(args[3+k], pos[k], false)
			if err != nil {
				return errToken("parsing.double.expected", 4+k)
			}
			pos[k] = v
		}
		px, py, pz = pos[0], pos[1], pos[2]
	}
	volume, pitch := float32(1), float32(1)
	var err error
	if len(args) >= 7 {
		if volume, err = floatArg(args[6]); err != nil {
			return errToken("parsing.float.expected", 7)
		}
		if volume < 0 {
			return errToken("argument.float.low", 7, float32(0), volume)
		}
	}
	if len(args) >= 8 {
		if pitch, err = floatArg(args[7]); err != nil {
			return errToken("parsing.float.expected", 8)
		}
		if pitch < 0 {
			return errToken("argument.float.low", 8, float32(0), pitch)
		}
		if pitch > 2 {
			return errToken("argument.float.big", 8, float32(2), pitch)
		}
	}
	if len(args) >= 9 {
		minVol, e := floatArg(args[8])
		if e != nil {
			return errToken("parsing.float.expected", 9)
		}
		if minVol < 0 {
			return errToken("argument.float.low", 9, float32(0), minVol)
		}
		if minVol > 1 {
			return errToken("argument.float.big", 9, float32(1), minVol)
		}
	}
	if len(args) > 9 {
		return errToken("command.unknown.argument", 10)
	}
	seed := rand.Int63()
	for _, t := range targets {
		var p protocol.Buffer
		p.Byte(0)
		p.String(sound)
		p.VarInt(cat)
		p.Byte(0)
		p.Int(int32(px * 8))
		p.Int(int32(py * 8))
		p.Int(int32(pz * 8))
		p.Float(volume)
		p.Float(pitch)
		p.Long(seed)
		_ = t.write(t.ids().PlaySound, p.Bytes())
	}
	snd := nestedCompound{key: "", value: sound}
	if len(targets) == 1 {
		return componentReply(translatable("commands.playsound.success.single",
			snd, playerChatComponent(targets[0])))
	}
	return componentReply(translatable("commands.playsound.success.multiple",
		snd, nestedCompound{key: "", value: int32(len(targets))}))
}

func (s *Server) runStopsound(player string, args []string) reply {
	if len(args) < 1 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	targets, rep := s.playerTargets(player, args[:1], 1)
	if rep != nil {
		return *rep
	}
	hasSource, hasSound := false, false
	source := int32(0)
	sound := ""
	if len(args) >= 2 {
		c, ok := soundSources[args[1]]
		if !ok {
			return errToken("command.unknown.argument", 2)
		}
		hasSource, source = true, c
	}
	if len(args) >= 3 {
		hasSound = true
		sound = bossbarID(args[2])
	}
	if len(args) > 3 {
		return errToken("command.unknown.argument", 4)
	}
	for _, t := range targets {
		var p protocol.Buffer
		var flags byte
		if hasSource {
			flags |= 1
		}
		if hasSound {
			flags |= 2
		}
		p.Byte(flags)
		if hasSource {
			p.VarInt(source)
		}
		if hasSound {
			p.String(sound)
		}
		_ = t.write(t.ids().PlayStopSound, p.Bytes())
	}
	switch {
	case !hasSource:
		return componentReply(translatable("commands.stopsound.success.sourceless.any"))
	case !hasSound:
		return componentReply(translatable("commands.stopsound.success.source.any", args[1]))
	default:
		return componentReply(translatable("commands.stopsound.success.source.sound", sound, args[1]))
	}
}

func (s *Server) runParticle(player string, args []string) reply {
	sender := s.findSessionByName(player)
	if len(args) < 1 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	name := bossbarID(args[0])
	short := strings.TrimPrefix(name, "minecraft:")
	id, ok := particleTypeIDs[short]
	if !ok {
		return errTokenEnd("particle.notFound", len(args)+1, name)
	}
	rest := args[1:]
	hasOptions := false
	if keys, need := particleOptionKeys[short]; need {
		hasOptions = true
		var options map[string]*snbtTag
		if len(rest) > 0 {
			if t, _, perr := parseSNBTTag(rest[0], 0); perr == nil && t != nil && t.kind == snbtCompound {
				options = t.vals
			}
			rest = rest[1:]
		}
		missing := make([]string, 0, len(keys))
		for _, k := range keys {
			if options[k] == nil {
				missing = append(missing, "No key "+k+" in MapLike[{}]")
			}
		}
		if len(missing) > 0 {
			return componentReply(commandError("particle.invalidOptions", strings.Join(missing, "; ")))
		}
	}
	pend := func() reply { return errTokenEnd("command.unknown.command", len(args)+1) }
	px, py, pz := senderPos(sender)
	base := 2
	if hasOptions {
		base++
	}
	pos := []float64{px, py, pz}
	for k := 0; k < 3 && k < len(rest); k++ {
		v, err := parseDouble(rest[k], pos[k], false)
		if err != nil {
			return errToken("parsing.double.expected", base+k)
		}
		pos[k] = v
	}
	px, py, pz = pos[0], pos[1], pos[2]
	if len(rest) > 0 && len(rest) < 3 {
		return pend()
	}
	if len(rest) >= 3 {
		rest = rest[3:]
	}
	var dx, dy, dz, speed float32
	var count int32
	if len(rest) > 0 {
		for k, dst := range []*float32{&dx, &dy, &dz} {
			v, err := parseDouble(rest[k], 0, false)
			if err != nil {
				return errToken("parsing.double.expected", base+3+k)
			}
			*dst = float32(v)
			if len(rest) < k+2 {
				return pend()
			}
		}
		rest = rest[3:]
		if len(rest) == 0 {
			return pend()
		}
		if sp, err := floatArg(rest[0]); err != nil {
			return errToken("parsing.float.expected", base+6)
		} else {
			speed = sp
		}
		if speed < 0 {
			return errToken("argument.float.low", base+6, float32(0), speed)
		}
		rest = rest[1:]
		if len(rest) == 0 {
			return pend()
		}
		cnt, err := strconv.ParseInt(rest[0], 10, 32)
		if err != nil {
			return errToken("parsing.int.expected", base+7)
		}
		count = int32(cnt)
		if count < 0 {
			return errToken("argument.integer.low", base+7, int32(0), count)
		}
		rest = rest[1:]
	}
	force := false
	if len(rest) > 0 {
		switch rest[0] {
		case "force":
			force = true
		case "normal":
		default:
			return errToken("command.unknown.argument", base+8)
		}
		rest = rest[1:]
	}
	targets := s.allSessions()
	if len(rest) > 0 {
		t, rep := s.playerTargets(player, rest[:1], base+9)
		if rep != nil {
			return *rep
		}
		targets = t
		rest = rest[1:]
	}
	if len(rest) > 0 {
		return errToken("command.unknown.argument", base+10)
	}
	for _, t := range targets {
		var p protocol.Buffer
		if force {
			p.Byte(1)
		} else {
			p.Byte(0)
		}
		p.Byte(0)
		p.Double(px)
		p.Double(py)
		p.Double(pz)
		p.Float(dx)
		p.Float(dy)
		p.Float(dz)
		p.Float(speed)
		p.Int(count)
		p.VarInt(id)
		_ = t.write(t.ids().PlayLevelParticles, p.Bytes())
	}
	return componentReply(translatable("commands.particle.success", name))
}

func (s *Server) allSessions() []*Session {
	var out []*Session
	s.sessions.Range(func(_, v any) bool {
		if sess, ok := v.(*Session); ok {
			out = append(out, sess)
		}
		return true
	})
	return out
}
