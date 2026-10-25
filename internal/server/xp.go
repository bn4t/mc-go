package server

import (
	"math"
	"strconv"
)

// Experience state mirrors the real server's three independent fields:
// experienceProgress (bar fraction), experienceLevel, and
// totalExperience — total is a bookkeeping sum only, never the source
// of the level.
//
// xpNeededForLevel is Player.getXpNeededForNextLevel: 7+2l below 15,
// 37+5(l-15) below 30, 112+9(l-30) at 30 and up.
func xpNeededForLevel(level int32) int32 {
	switch {
	case level >= 30:
		return 112 + (level-30)*9
	case level >= 15:
		return 37 + (level-15)*5
	default:
		return 7 + level*2
	}
}

// addLevels is the saturated-add core of Player.giveExperienceLevels:
// a level that would drop below zero resets all three fields. It does
// not sync — callers batch the Set Experience themselves.
func (sess *Session) addLevels(n int32) {
	sum := sess.xpLevel + n
	if n > 0 && sum < sess.xpLevel {
		sum = math.MaxInt32
	}
	if n < 0 && sum > sess.xpLevel {
		sum = math.MinInt32
	}
	sess.xpLevel = sum
	if sess.xpLevel < 0 {
		sess.xpLevel = 0
		sess.xpProgress = 0
		sess.xpTotal = 0
	}
}

// giveExperienceLevels is Player.giveExperienceLevels through the
// ServerPlayer override: zero is a no-op.
func (sess *Session) giveExperienceLevels(n int32) {
	if n == 0 {
		return
	}
	sess.addLevels(n)
	sess.syncXP()
}

// giveExperiencePoints is ServerPlayer.giveExperiencePoints: zero is a
// no-op; otherwise the total clamps to [0, max], the bar moves by
// points/needed, and progress outside [0,1) carries whole levels down
// or up.
func (sess *Session) giveExperiencePoints(n int32) {
	if n == 0 {
		return
	}
	sess.xpProgress += float32(n) / float32(xpNeededForLevel(sess.xpLevel))
	if t := int64(sess.xpTotal) + int64(n); t < 0 {
		sess.xpTotal = 0
	} else if t > math.MaxInt32 {
		sess.xpTotal = math.MaxInt32
	} else {
		sess.xpTotal = int32(t)
	}
	for sess.xpProgress < 0 {
		carried := sess.xpProgress * float32(xpNeededForLevel(sess.xpLevel))
		if sess.xpLevel > 0 {
			sess.addLevels(-1)
			sess.xpProgress = 1.0 + carried/float32(xpNeededForLevel(sess.xpLevel))
		} else {
			sess.addLevels(-1)
			sess.xpProgress = 0
		}
	}
	for sess.xpProgress >= 1 {
		sess.xpProgress = (sess.xpProgress - 1) * float32(xpNeededForLevel(sess.xpLevel))
		sess.addLevels(1)
		sess.xpProgress /= float32(xpNeededForLevel(sess.xpLevel))
	}
	sess.syncXP()
}

// setExperiencePoints is ExperienceCommand POINTS.set plus the
// ServerPlayer override: the bar moves to i/needed capped a point shy
// of a full level; i >= needed refuses (the command's
// set.points.invalid error), and an unchanged bar skips the sync.
func (sess *Session) setExperiencePoints(i int32) bool {
	needed := xpNeededForLevel(sess.xpLevel)
	if i >= needed {
		return false
	}
	f := float32(needed)
	progress := float32(i) / f
	if max := (f - 1) / f; progress > max {
		progress = max
	}
	if progress == sess.xpProgress {
		return true
	}
	sess.xpProgress = progress
	sess.syncXP()
	return true
}

// setExperienceLevels is ServerPlayer.setExperienceLevels: it touches
// only the level — the bar and the total keep whatever they held — and
// an unchanged level skips the sync.
func (sess *Session) setExperienceLevels(i int32) {
	if i == sess.xpLevel {
		return
	}
	sess.xpLevel = i
	sess.syncXP()
}

// xpQueryPoints is POINTS.query: the bar's points within the level,
// floored.
func (sess *Session) xpQueryPoints() int32 {
	return int32(math.Floor(float64(sess.xpProgress * float32(xpNeededForLevel(sess.xpLevel)))))
}

// syncXP sends Set Experience — the real server latches lastSentExp=-1
// and the packet leaves on the next tick; sending inline lands the
// same wire bytes sooner.
func (sess *Session) syncXP() {
	_ = sess.server.sendSetExperience(sess, sess.xpProgress, sess.xpLevel, sess.xpTotal)
}

// /experience (alias /xp): add|set <targets> <amount> [points|levels]
// and query <target> points|levels. add takes any integer (negatives
// remove), set requires >= 0, and both default a bare amount to
// points. set points can fail per-player above the level cap; when
// every target fails the command reports set.points.invalid.
func (s *Server) runXp(player string, args []string) reply {
	sess := s.findSessionByName(player)
	if sess == nil || sess.opLevel() < 2 {
		return reply{comp: unknownCommand(), styled: true, fail: &failMark{}}
	}
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 1)
	}
	switch args[0] {
	case "add", "set":
	case "query":
		return s.runXpQuery(player, args)
	default:
		return errToken("command.unknown.argument", 1)
	}
	add := args[0] == "add"
	if len(args) < 3 {
		return errTokenEnd("command.unknown.command", len(args))
	}
	targets := s.resolveTargets(player, args[1])
	if len(targets) == 0 {
		// Resolution failures carry no context line on the wire.
		return componentReply(commandError("argument.entity.notfound.player"))
	}
	amount, rep := xpAmountArg(args[2])
	if rep != nil {
		return *rep
	}
	if !add && amount < 0 {
		return errToken("argument.integer.low", 3, int32(0), int32(amount))
	}
	unit := "points"
	if len(args) > 3 {
		if args[3] != "points" && args[3] != "levels" {
			return errToken("command.unknown.argument", 4)
		}
		unit = args[3]
	}
	if len(args) > 4 {
		return errToken("command.unknown.argument", 5)
	}
	done := 0
	for _, t := range targets {
		if add {
			if unit == "points" {
				t.giveExperiencePoints(int32(amount))
			} else {
				t.giveExperienceLevels(int32(amount))
			}
		} else {
			if unit == "points" {
				if !t.setExperiencePoints(int32(amount)) {
					continue
				}
			} else {
				t.setExperienceLevels(int32(amount))
			}
		}
		done++
	}
	if !add && unit == "points" && done == 0 {
		return componentReply(commandError("commands.experience.set.points.invalid"))
	}
	verb := "set"
	if add {
		verb = "add"
	}
	if len(targets) == 1 {
		return opsEchoReply(translatable("commands.experience."+verb+"."+unit+".success.single",
			int32(amount), s.entityTargetComponent(targets[0])))
	}
	return opsEchoReply(translatable("commands.experience."+verb+"."+unit+".success.multiple",
		int32(amount), int32(len(targets))))
}

// xpAmountArg is brigadier's StringReader.readInt for the amount token:
// a missing number reads as parsing.int.expected, digits followed by
// junk end the argument mid-token (command.expected.separator), and a
// number outside int32 reads as parsing.int.invalid.
func xpAmountArg(tok string) (int64, *reply) {
	i := 0
	if i < len(tok) && (tok[i] == '-' || tok[i] == '+') {
		i++
	}
	digits := i
	for i < len(tok) && tok[i] >= '0' && tok[i] <= '9' {
		i++
	}
	if i == digits {
		return 0, ptrReply(errToken("parsing.int.expected", 3))
	}
	if i < len(tok) {
		return 0, ptrReply(errTokenOff("command.expected.separator", 3, i))
	}
	amount, err := strconv.ParseInt(tok, 10, 32)
	if err != nil {
		return 0, ptrReply(errToken("parsing.int.invalid", 3, tok))
	}
	return amount, nil
}

func ptrReply(r reply) *reply { return &r }

// runXpQuery is `experience query <target> points|levels` — a single
// player (multi-player selectors fail before reading the unit) and a
// source-local reply, never broadcast.
func (s *Server) runXpQuery(player string, args []string) reply {
	if len(args) < 3 {
		return errTokenEnd("command.unknown.command", len(args))
	}
	unit := args[2]
	if unit != "points" && unit != "levels" {
		return errToken("command.unknown.argument", 3)
	}
	if len(args) > 3 {
		return errToken("command.unknown.argument", 4)
	}
	// EntityArgument.player() rejects multi-target selectors outright;
	// the wildcard forms are the only tokens that allow more than one,
	// and the throw lands at cursor 0 like every execution-time
	// selector error.
	if args[1] == "@a" || args[1] == "@e" {
		return errToken("argument.player.toomany", 0)
	}
	target := s.resolveTarget(player, args[1])
	if target == nil {
		return componentReply(commandError("argument.entity.notfound.player"))
	}
	amount := target.xpLevel
	if unit == "points" {
		amount = target.xpQueryPoints()
	}
	return componentReply(translatable("commands.experience.query."+unit,
		s.entityTargetComponent(target), amount))
}
