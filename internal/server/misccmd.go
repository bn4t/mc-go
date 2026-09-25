package server

// Server-management and misc commands measured against the vanilla
// 26.2 server jar:
//
//	save-all [flush]          two replies: saving then success
//	save-off | save-on        autosave flag + disabled/enabled
//	save-off again            alreadyOff / alreadyOn errors
//	stop                      commands.stop.stopping, then shutdown
//	defaultgamemode <mode>    gameMode.<mode> translatable arg
//	setidletimeout <minutes>  0 -> success.disabled
//	reload                    resends registries, tags, command tree
//	debug|perf start|stop     started/stopped-with-timing replies
//	jfr start|stop            started / stopped-with-file-link
//	trigger <obj> ...         objective.notFound (none exist)
//	random value|roll <range> [<seed>]
//	spectate [<target>]       spectator checks + set_camera packet
//	transfer <host> [<port> [<players>]]  transfer packet + reply

import (
	_ "embed"
	"fmt"
	"math/rand"
	"strconv"
	"sync/atomic"
	"time"

	"mc-go/internal/protocol"
)

func registerMiscCommands(s *Server) {
	s.registerComponentCommand("save-all", func(player string, args []string) reply {
		return s.runSaveAll(player, args)
	})
	s.registerComponentCommand("save-off", func(player string, args []string) reply {
		return s.runSaveOffOn(player, args, false)
	})
	s.registerComponentCommand("save-on", func(player string, args []string) reply {
		return s.runSaveOffOn(player, args, true)
	})
	s.registerComponentCommand("stop", func(player string, args []string) reply {
		return s.runStopCmd(args)
	})
	s.registerComponentCommand("defaultgamemode", func(player string, args []string) reply {
		return s.runDefaultGamemode(args)
	})
	s.registerComponentCommand("reload", func(player string, args []string) reply {
		return s.runReloadCmd(player, args)
	})
	s.registerComponentCommand("debug", func(player string, args []string) reply {
		return s.runProfileCmd(args, true)
	})
	s.registerComponentCommand("perf", func(player string, args []string) reply {
		return s.runProfileCmd(args, false)
	})
	s.registerComponentCommand("jfr", func(player string, args []string) reply {
		return s.runJfr(args)
	})
	s.registerComponentCommand("trigger", func(player string, args []string) reply {
		return s.runTrigger(player, args)
	})
	s.registerComponentCommand("random", func(player string, args []string) reply {
		return s.runRandom(player, args)
	})
	s.registerComponentCommand("spectate", func(player string, args []string) reply {
		return s.runSpectate(player, args)
	})
	s.registerComponentCommand("transfer", func(player string, args []string) reply {
		return s.runTransfer(player, args)
	})
}

func (s *Server) runSaveAll(player string, args []string) reply {
	if len(args) > 0 {
		if args[0] != "flush" {
			return errToken("command.unknown.argument", 1)
		}
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
	}
	if err := s.SaveWorld(); err != nil {
		return componentReply(commandError("commands.save.failed"))
	}
	success := translatable("commands.save.success")
	return replyEchoLast(
		translatable("commands.save.saving"),
		success)
}

func (s *Server) runSaveOffOn(player string, args []string, on bool) reply {
	if len(args) > 0 {
		return errToken("command.unknown.argument", 1)
	}
	if s.autosaveOff.Load() == !on {
		if on {
			return componentReply(commandError("commands.save.alreadyOn"))
		}
		return componentReply(commandError("commands.save.alreadyOff"))
	}
	s.autosaveOff.Store(!on)
	var msg component
	if on {
		msg = translatable("commands.save.enabled")
	} else {
		msg = translatable("commands.save.disabled")
	}
	return opsEchoReply(msg)
}

func (s *Server) runStopCmd(args []string) reply {
	if len(args) > 0 {
		return errToken("command.unknown.argument", 1)
	}
	// The reply is queued ahead of the kicks, so the issuer sees it
	// before the disconnect lands.
	go s.Shutdown("Server closed")
	return opsEchoReply(translatable("commands.stop.stopping"))
}

func (s *Server) runDefaultGamemode(args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	mode, ok := parseGameMode(args[0])
	if !ok {
		return errTokenEnd("argument.gamemode.invalid", 1, args[0])
	}
	if len(args) > 1 {
		return errToken("command.unknown.argument", 2)
	}
	s.GameMode = mode
	return opsEchoReply(translatable("commands.defaultgamemode.success",
		translatable("gameMode."+args[0])))
}

// Reload-order packet bodies captured from the vanilla jar: the same
// tag and recipe data the config phase sends, but iterating the
// registries in the order a live reload emits them.
//
//go:embed testdata/vanilla-26.2/reload/update_tags.bin
var reloadTagsBody []byte

//go:embed testdata/vanilla-26.2/reload/update_recipes.bin
var reloadRecipesBody []byte

func (s *Server) runReloadCmd(player string, args []string) reply {
	if len(args) > 0 {
		return errToken("command.unknown.argument", 1)
	}
	if sender := s.findSessionByName(player); sender != nil {
		s.sendSystemChatComponent(sender, translatable("commands.reload.success"))
	}
	s.sessions.Range(func(_, v any) bool {
		if sess, ok := v.(*Session); ok {
			_ = sess.write(sess.ids().PlayUpdateTags, reloadTagsBody)
			_ = sess.write(sess.ids().PlayUpdateRecipes, reloadRecipesBody)
			_ = s.sendRecipeBookSettings(sess)
		}
		return true
	})
	return reply{}
}

type profileRun struct {
	started atomic.Bool
	start   time.Time
	tick0   int64
}

func (s *Server) runProfileCmd(args []string, debug bool) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	if len(args) > 1 {
		return errToken("command.unknown.argument", 2)
	}
	var running *profileRun
	if debug {
		running = &s.debugRun
	} else {
		running = &s.perfRun
	}
	prefix := "commands.debug."
	if !debug {
		prefix = "commands.perf."
	}
	switch args[0] {
	case "start":
		if running.started.Load() {
			return componentReply(commandError(prefix + "alreadyRunning"))
		}
		running.started.Store(true)
		running.start = time.Now()
		running.tick0 = s.TickCount()
		return opsEchoReply(translatable(prefix + "started"))
	case "stop":
		if !running.started.Load() {
			return componentReply(commandError(prefix + "notRunning"))
		}
		running.started.Store(false)
		elapsed := time.Since(running.start).Seconds()
		ticks := s.TickCount() - running.tick0
		var tps float64
		if elapsed > 0 {
			tps = float64(ticks) / elapsed
		}
		return opsEchoReply(translatable(prefix+"stopped",
			nestedCompound{key: "", value: fmt.Sprintf("%.2f", elapsed)},
			nestedCompound{key: "", value: int32(ticks)},
			nestedCompound{key: "", value: fmt.Sprintf("%.2f", tps)}))
	default:
		return errToken("command.unknown.argument", 1)
	}
}

func (s *Server) runJfr(args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	if len(args) > 1 {
		return errToken("command.unknown.argument", 2)
	}
	switch args[0] {
	case "start":
		if s.jfrRunning.Swap(true) {
			return componentReply(commandError("commands.jfr.start.failed"))
		}
		return componentReply(translatable("commands.jfr.started"))
	case "stop":
		if !s.jfrRunning.Swap(false) {
			return componentReply(translatable("commands.jfr.stopped",
				jfrLink(time.Now())))
		}
		return componentReply(translatable("commands.jfr.stopped",
			jfrLink(time.Now())))
	default:
		return errToken("command.unknown.argument", 1)
	}
}

// jfrLink renders the dumped-recording link: underlined text that
// copies the path on click and explains itself on hover.
func jfrLink(t time.Time) component {
	name := "debug/server-" + t.Format("2006-01-02-150405") + ".jfr"
	return component{
		text:       name,
		copyTo:     name,
		underlined: true,
		hover: map[string]any{
			"action": "show_text",
			"value":  component{translate: "chat.copy.click"}.nested(),
		},
	}
}

// runTrigger implements /trigger: trigger <objective> [add|set
// <value>]. Only trigger-criteria objectives qualify, and the
// caller's own score must have been released by
// scoreboard players enable — a missing or locked score is
// trigger.failed.unprimed, a non-trigger objective
// trigger.failed.invalid. A successful trigger re-locks the score.
func (s *Server) runTrigger(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	obj := args[0]
	mode := ""
	var value int32
	if len(args) >= 2 {
		switch args[1] {
		case "add", "set":
			mode = args[1]
			if len(args) < 3 {
				return errTokenEnd("command.unknown.command", len(args)+1)
			}
			if len(args) > 3 {
				return errToken("command.unknown.argument", 4)
			}
			v, perr := sbParseInt(args[2], 3)
			if perr != nil {
				return *perr
			}
			value = v
		default:
			return errToken("command.unknown.argument", 2)
		}
	}
	s.board.mu.Lock()
	o, objOK := s.board.objectives[obj]
	s.board.mu.Unlock()
	if !objOK {
		return componentReply(commandError("arguments.objective.notFound", obj))
	}
	if o.criteria != "trigger" {
		return componentReply(commandError("commands.trigger.failed.invalid"))
	}
	s.board.mu.Lock()
	e := s.board.scores[player][obj]
	locked := e == nil || e.locked
	s.board.mu.Unlock()
	if locked {
		return componentReply(commandError("commands.trigger.failed.unprimed"))
	}
	cur := scoreValue(e)
	key := "commands.trigger.simple.success"
	switch mode {
	case "add":
		cur += value
		key = "commands.trigger.add.success"
	case "set":
		cur = value
		key = "commands.trigger.set.success"
	default:
		cur++
	}
	s.board.mu.Lock()
	s.board.scores[player][obj].value = cur
	s.board.scores[player][obj].locked = true
	s.board.mu.Unlock()
	s.broadcastScore(player, obj, cur)
	if mode == "" {
		return opsEchoReply(translatable(key, s.objectiveChatComponent(obj)))
	}
	return opsEchoReply(translatable(key, s.objectiveChatComponent(obj),
		nestedCompound{key: "", value: value}))
}

func (s *Server) runRandom(player string, args []string) reply {
	if len(args) < 1 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	roll := false
	switch args[0] {
	case "value":
	case "roll":
		roll = true
	default:
		return errToken("command.unknown.argument", 1)
	}
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	lo, hi, ok := parseIntRange(args[1])
	if !ok {
		return errToken("parsing.int.expected", 2)
	}
	if lo > hi {
		return errToken("argument.range.swapped", 2)
	}
	if lo >= hi {
		return componentReply(commandError("commands.random.error.range_too_small"))
	}
	if hi-lo+1 > 2147483646 {
		return componentReply(commandError("commands.random.error.range_too_large"))
	}
	if len(args) > 3 {
		return errToken("command.unknown.argument", 4)
	}
	n := int32(lo + rand.Int63n(hi-lo+1))
	if !roll {
		return componentReply(translatable("commands.random.sample.success", n))
	}
	sender := s.findSessionByName(player)
	name := literal(player)
	if sender != nil {
		name = playerChatComponent(sender)
	}
	return componentReply(translatable("commands.random.roll", name,
		nestedCompound{key: "", value: n},
		nestedCompound{key: "", value: int32(lo)},
		nestedCompound{key: "", value: int32(hi)}))
}

func (s *Server) runSpectate(player string, args []string) reply {
	sender := s.findSessionByName(player)
	name := literal(player)
	if sender != nil {
		name = playerChatComponent(sender)
	}
	notSpectator := func() reply {
		return componentReply(commandError("commands.spectate.not_spectator", name))
	}
	if len(args) == 0 {
		return notSpectator()
	}
	if len(args) > 1 {
		return errToken("command.unknown.argument", 2)
	}
	if args[0][0] != '@' && s.findSessionByName(args[0]) == nil {
		// /spectate's target is an entity argument: a plain name that
		// resolves to nobody fails with notfound.entity, not the
		// player-flavoured one.
		return componentReply(commandError("argument.entity.notfound.entity"))
	}
	targets, rep := s.selectDataEntities(sender, args[0], 1, true)
	if rep != nil {
		return *rep
	}
	if len(targets) == 0 {
		return componentReply(commandError("argument.entity.notfound.entity"))
	}
	t := targets[0]
	if t.sess != nil && sender != nil && t.sess == sender {
		return componentReply(commandError("commands.spectate.self"))
	}
	if sender == nil {
		return componentReply(commandError("commands.spectate.cannot_spectate"))
	}
	if sender.mode() != GameModeSpectator {
		return notSpectator()
	}
	var p protocol.Buffer
	if t.sess != nil {
		p.VarInt(t.sess.entityID.Load())
	} else {
		p.VarInt(t.mob.ID)
	}
	_ = sender.write(sender.ids().PlaySetCamera, p.Bytes())
	return componentReply(translatable("commands.spectate.success.started"))
}

func (s *Server) runTransfer(player string, args []string) reply {
	if len(args) < 1 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	host := args[0]
	port := int64(25565)
	if len(args) >= 2 {
		var err error
		port, err = strconv.ParseInt(args[1], 10, 32)
		if err != nil {
			return errToken("parsing.int.expected", 2)
		}
		if port < 1 {
			return errToken("argument.integer.low", 2, int32(1), int32(port))
		}
		if port > 65535 {
			return errToken("argument.integer.big", 2, int32(65535), int32(port))
		}
	}
	targets := []*Session{s.findSessionByName(player)}
	if len(args) >= 3 {
		t, rep := s.playerTargets(player, args[2:], 3)
		if rep != nil {
			return *rep
		}
		targets = t
	}
	if len(args) > 3 {
		return errToken("command.unknown.argument", 4)
	}
	var live []*Session
	for _, t := range targets {
		if t != nil {
			live = append(live, t)
		}
	}
	if len(live) == 0 {
		return componentReply(commandError("commands.transfer.error.no_players"))
	}
	for _, t := range live {
		var p protocol.Buffer
		p.String(host)
		p.VarInt(int32(port))
		_ = t.write(t.ids().PlayTransfer, p.Bytes())
	}
	hostComp := nestedCompound{key: "", value: host}
	portComp := nestedCompound{key: "", value: int32(port)}
	if len(live) == 1 {
		return opsEchoReply(translatable("commands.transfer.success.single",
			playerChatComponent(live[0]), hostComp, portComp))
	}
	return opsEchoReply(translatable("commands.transfer.success.multiple",
		nestedCompound{key: "", value: int32(len(live))}, hostComp, portComp))
}
