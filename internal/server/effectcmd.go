package server

// /effect — vanilla EffectCommands parity: give and clear over
// player targets, with vanilla's reply lattice and the amplifier/
// duration rule that decides whether a re-application lands. Effects
// applied and removed through here run the same store + wire path as
// totem death effects, so effects_changed fires on every transition.

import (
	"strconv"
	"strings"
)

// effectArg renders an effect's display name for the give/clear
// replies — vanilla puts MobEffectInstance.getDisplayName() in the
// %s slot, which is the effect's translatable key.
func effectArg(name string) component {
	return translatable("effect.minecraft." + name)
}

// runEffect implements /effect give|clear. Vanilla's grammar:
//
//	effect give <targets> <effect> [<seconds>] [<amplifier>] [<hideParticles>]
//	effect clear [<targets>] [<effect>]
//
// Duration is seconds (30 default); "infinite" means never expire.
func (s *Server) runEffect(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "give":
		return s.runEffectGive(player, args[1:])
	case "clear":
		return s.runEffectClear(player, args[1:])
	}
	return errToken("command.unknown.argument", 1)
}

func (s *Server) runEffectGive(player string, args []string) reply {
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args))
	}
	targets := s.resolveTargets(player, args[0])
	name := strings.TrimPrefix(args[1], "minecraft:")
	id, ok := MobEffectID(name)
	if !ok {
		return errTokenEnd("command.unknown.argument", 1)
	}
	// Seconds, then "infinite": vanilla parses the duration token as a
	// resource first and accepts the literal "infinite" for -1 ticks.
	seconds := int32(30)
	ticks := int32(600)
	if len(args) > 2 {
		if args[2] == "infinite" {
			ticks = -1
		} else {
			n, err := strconv.Atoi(args[2])
			if err != nil || n < 0 {
				return errTokenEnd("command.unknown.argument", 2)
			}
			seconds = int32(n)
			ticks = seconds * 20
		}
	}
	amplifier := int32(0)
	if len(args) > 3 {
		n, err := strconv.Atoi(args[3])
		if err != nil || n < 0 || n > 255 {
			return errTokenEnd("command.unknown.argument", 3)
		}
		amplifier = int32(n)
	}
	hideParticles := false
	if len(args) > 4 {
		if args[4] != "true" && args[4] != "false" {
			return errTokenEnd("command.unknown.argument", 4)
		}
		hideParticles = args[4] == "true"
	}
	if len(targets) == 0 {
		return errTokenEnd("command.unknown.argument", 0)
	}
	// Vanilla's addEffect routes through MobEffectInstance.update:
	// a stronger amplifier replaces outright, an equal amplifier only
	// updates when the new duration is longer-or-equal (durations are
	// compared remaining-vs-new, infinite counting as -1), and a
	// weaker one never applies but is stashed into the hiddenEffect
	// chain when it outlasts the current — all other re-gives fail
	// the whole command with give.failed.
	applied := 0
	for _, t := range targets {
		now := s.clock.count
		if e, live := sessEffect(t, id, now); live {
			newInst := mobEffectInst{amplifier: amplifier, expiry: effectExpiry(now, ticks)}
			switch {
			case amplifier > e.amplifier:
				// fall through and apply
			case amplifier == e.amplifier && shorterDur(e, newInst, now):
				// duration update — apply
			case amplifier < e.amplifier && shorterDur(e, newInst, now):
				s.stashHiddenEffect(t, id, newInst)
				s.applyInstantEffect(t, id, amplifier)
				continue
			default:
				// Declined stores still run onEffectStarted — instant
				// effects apply (measured: a shorter re-give heals
				// while replying give.failed).
				s.applyInstantEffect(t, id, amplifier)
				continue
			}
		}
		s.applyMobEffectFlags(t, id, amplifier, ticks, hideParticles)
		applied++
	}
	if applied == 0 {
		return componentReply(commandError("commands.effect.give.failed"))
	}
	if len(targets) == 1 {
		return componentReply(translatable("commands.effect.give.success.single",
			effectArg(name), s.entityTargetComponent(targets[0])))
	}
	return componentReply(translatable("commands.effect.give.success.multiple",
		effectArg(name), nestedCompound{key: "", value: applied}))
}

func (s *Server) runEffectClear(player string, args []string) reply {
	targetTok := "@s"
	if len(args) > 0 {
		targetTok = args[0]
		args = args[1:]
		if len(args) > 0 {
			// clear <targets> <effect>
		}
	}
	targets := s.resolveTargets(player, targetTok)
	if len(targets) == 0 {
		return errTokenEnd("command.unknown.argument", 0)
	}
	if len(args) == 0 {
		// Strip everything; fails flat when nobody has an effect.
		removed := 0
		for _, t := range targets {
			if sessEffectCount(t) == 0 {
				continue
			}
			s.clearMobEffects(t)
			removed++
		}
		if removed == 0 {
			return componentReply(commandError("commands.effect.clear.everything.failed"))
		}
		if len(targets) == 1 {
			return componentReply(translatable("commands.effect.clear.everything.success.single",
				s.entityTargetComponent(targets[0])))
		}
		return componentReply(translatable("commands.effect.clear.everything.success.multiple",
			nestedCompound{key: "", value: removed}))
	}
	name := strings.TrimPrefix(args[0], "minecraft:")
	id, ok := MobEffectID(name)
	if !ok {
		return errTokenEnd("command.unknown.argument", 1)
	}
	removed := 0
	for _, t := range targets {
		if !sessHasEffect(t, id) {
			continue
		}
		s.removeMobEffect(t, id)
		removed++
	}
	if removed == 0 {
		return componentReply(commandError("commands.effect.clear.specific.failed"))
	}
	if len(targets) == 1 {
		return componentReply(translatable("commands.effect.clear.specific.success.single",
			effectArg(name), s.entityTargetComponent(targets[0])))
	}
	return componentReply(translatable("commands.effect.clear.specific.success.multiple",
		effectArg(name), nestedCompound{key: "", value: removed}))
}

// sessEffect returns the live instance for id and whether it is
// actually live — an expired-but-unflushed entry counts as gone.
func sessEffect(sess *Session, id int32, now int64) (mobEffectInst, bool) {
	sess.effMu.Lock()
	defer sess.effMu.Unlock()
	e, ok := sess.effects[id]
	if ok && e.expiry >= 0 && e.expiry <= now {
		return mobEffectInst{}, false
	}
	return e, ok
}

func sessHasEffect(sess *Session, id int32) bool {
	_, ok := sessEffect(sess, id, sess.server.clock.count)
	return ok
}

func sessEffectCount(sess *Session) int {
	sess.effMu.Lock()
	defer sess.effMu.Unlock()
	return len(sess.effects)
}
