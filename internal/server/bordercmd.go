package server

import (
	"strconv"

	"mc-go/internal/protocol"
)

// border is one dimension's world border state, mirrored from the
// vanilla WorldBorder fields the command and join packet read.
type border struct {
	centerX, centerZ float64
	size             float64
	warnBlocks       int32
	warnTime         int32
	damagePerBlock   float32
	damageSafeZone   float32
}

func defaultBorder() *border {
	return &border{
		size:           59999968,
		warnBlocks:     5,
		warnTime:       15,
		damagePerBlock: 0.2,
		damageSafeZone: 5,
	}
}

// borderFor returns the sender-dimension border, creating it lazily.
func (s *Server) borderFor(dim string) *border {
	s.borderMu.Lock()
	defer s.borderMu.Unlock()
	b, ok := s.borders[dim]
	if !ok {
		b = defaultBorder()
		s.borders[dim] = b
	}
	return b
}

// registerWorldBorder wires /worldborder.
func (s *Server) registerWorldBorder() {
	s.registerComponentCommand("worldborder", func(player string, args []string) reply {
		return s.runWorldBorder(player, args)
	})
}

// wbFmt formats the border sizes/durations vanilla echoes: whole
// numbers without decimals, else plain float text.
func wbFmt(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func (s *Server) runWorldBorder(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 1)
	}
	sender := s.findSessionByName(player)
	dim := "minecraft:overworld"
	if sender != nil {
		dim = sender.dimName()
	}
	b := s.borderFor(dim)
	switch args[0] {
	case "get":
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
		return componentReply(translatable("commands.worldborder.get", wbFmt(b.size)))
	case "center":
		return s.runWorldBorderCenter(sender, b, dim, args)
	case "set":
		return s.runWorldBorderSet(b, dim, args, false)
	case "add":
		return s.runWorldBorderSet(b, dim, args, true)
	case "damage":
		return s.runWorldBorderDamage(b, args)
	case "warning":
		return s.runWorldBorderWarning(b, dim, args)
	default:
		return errToken("command.unknown.argument", 1)
	}
}

// runWorldBorderCenter implements /worldborder center <x> <z>.
func (s *Server) runWorldBorderCenter(sender *Session, b *border, dim string, args []string) reply {
	if len(args) < 3 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	bx, bz := float64(0), float64(0)
	if sender != nil {
		bx, _, bz, _, _, _ = sender.Pos()
	}
	x, err := parseDouble(args[1], bx)
	if err != nil {
		return errToken("parsing.double.expected", 2)
	}
	z, err := parseDouble(args[2], bz)
	if err != nil {
		return errToken("parsing.double.expected", 3)
	}
	if len(args) > 3 {
		return errToken("command.unknown.argument", 4)
	}
	b.centerX, b.centerZ = x, z
	var pkt protocol.Buffer
	pkt.Double(x)
	pkt.Double(z)
	s.broadcastToDim(dim, nil, LatestVersion().IDs.PlaySetBorderCenter, pkt.Bytes())
	return opsEchoReply(translatable("commands.worldborder.center.success",
		strconv.FormatFloat(x, 'f', 2, 64), strconv.FormatFloat(z, 'f', 2, 64)))
}

// runWorldBorderSet implements /worldborder set|add <size> [time].
func (s *Server) runWorldBorderSet(b *border, dim string, args []string, add bool) reply {
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	v, err := strconv.ParseFloat(args[1], 64)
	if err != nil {
		return errToken("parsing.double.expected", 2)
	}
	if add {
		v += b.size
	}
	if v < 0 {
		return errToken("argument.double.low", 2, float64(0), v)
	}
	if v > 59999968 {
		return errToken("argument.double.big", 2, float64(59999968), v)
	}
	var ticks int32
	if len(args) > 2 {
		var bad int32
		var ok bool
		ticks, bad, ok = parseTimeArg(args[2])
		if !ok {
			return errToken("parsing.float.expected", 3)
		}
		if bad < 0 {
			return errTokenEnd("argument.time.tick_count_too_low", 3, int32(0), bad)
		}
	}
	if len(args) > 3 {
		return errToken("command.unknown.argument", 4)
	}
	old := b.size
	if old == v {
		return componentReply(commandError("commands.worldborder.set.failed.nochange"))
	}
	b.size = v
	if ticks <= 0 {
		var pkt protocol.Buffer
		pkt.Double(v)
		s.broadcastToDim(dim, nil, LatestVersion().IDs.PlaySetBorderSize, pkt.Bytes())
		return opsEchoReply(translatable("commands.worldborder.set.immediate",
			strconv.FormatFloat(v, 'f', 1, 64)))
	}
	var pkt protocol.Buffer
	pkt.Double(old)
	pkt.Double(v)
	pkt.VarLong(int64(ticks))
	s.broadcastToDim(dim, nil, LatestVersion().IDs.PlaySetBorderLerpSize, pkt.Bytes())
	return opsEchoReply(translatable("commands.worldborder.set.grow",
		strconv.FormatFloat(v, 'f', 1, 64),
		strconv.FormatFloat(float64(ticks)*0.05, 'f', 2, 64)))
}

// runWorldBorderDamage implements /worldborder damage amount|buffer
// <f> — values the client never sees a packet for.
func (s *Server) runWorldBorderDamage(b *border, args []string) reply {
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	var key string
	switch args[1] {
	case "amount":
		key = "commands.worldborder.damage.amount.success"
	case "buffer":
		key = "commands.worldborder.damage.buffer.success"
	default:
		return errToken("command.unknown.argument", 2)
	}
	if len(args) < 3 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	v, err := strconv.ParseFloat(args[2], 32)
	if err != nil {
		return errToken("parsing.float.expected", 3)
	}
	if v < 0 {
		return errToken("argument.float.low", 3, float32(0), float32(v))
	}
	if len(args) > 3 {
		return errToken("command.unknown.argument", 4)
	}
	if args[1] == "amount" {
		b.damagePerBlock = float32(v)
	} else {
		b.damageSafeZone = float32(v)
	}
	return opsEchoReply(translatable(key, strconv.FormatFloat(v, 'f', 2, 32)))
}

// runWorldBorderWarning implements /worldborder warning time|distance
// <n> — both arguments parse as floats on the wire even though time
// stores whole seconds.
func (s *Server) runWorldBorderWarning(b *border, dim string, args []string) reply {
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	if args[1] != "time" && args[1] != "distance" {
		return errToken("command.unknown.argument", 2)
	}
	if len(args) < 3 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	if len(args) > 3 {
		return errToken("command.unknown.argument", 4)
	}
	if args[1] == "time" {
		t, bad, ok := parseTimeArg(args[2])
		if !ok {
			return errToken("parsing.float.expected", 3)
		}
		if bad < 0 {
			return errTokenEnd("argument.time.tick_count_too_low", 3, int32(0), bad)
		}
		b.warnTime = t
		var pkt protocol.Buffer
		pkt.VarInt(t)
		s.broadcastToDim(dim, nil, LatestVersion().IDs.PlaySetBorderWarningDelay, pkt.Bytes())
		return opsEchoReply(translatable("commands.worldborder.warning.time.success",
			strconv.FormatFloat(float64(t)*0.05, 'f', 2, 64)))
	}
	n, err := strconv.ParseInt(args[2], 10, 32)
	if err != nil {
		return errToken("parsing.int.expected", 3)
	}
	if n < 0 {
		return errToken("argument.integer.low", 3, int32(0), int32(n))
	}
	b.warnBlocks = int32(n)
	var pkt protocol.Buffer
	pkt.VarInt(int32(n))
	s.broadcastToDim(dim, nil, LatestVersion().IDs.PlaySetBorderWarnDistance, pkt.Bytes())
	return opsEchoReply(translatable("commands.worldborder.warning.distance.success",
		int32(n)))
}
