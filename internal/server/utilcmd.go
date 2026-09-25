package server

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// registerUtilCommands wires the utility command surface measured
// against the real server: /difficulty, /seed, /list, the
// /whitelist + /banlist + /setidletimeout admin trio, and the
// /say + /me + /msg chat family.
func (s *Server) registerUtilCommands() {
	s.registerComponentCommand("difficulty", func(player string, args []string) reply {
		return s.runDifficulty(args)
	})
	s.registerComponentCommand("seed", func(player string, args []string) reply {
		return s.runSeed(args)
	})
	s.registerComponentCommand("list", func(player string, args []string) reply {
		return s.runList(args)
	})
	s.registerComponentCommand("whitelist", func(player string, args []string) reply {
		return s.runWhitelist(args)
	})
	s.registerComponentCommand("banlist", func(player string, args []string) reply {
		return s.runBanlist(args)
	})
	s.registerComponentCommand("setidletimeout", func(player string, args []string) reply {
		return s.runSetIdleTimeout(args)
	})
	s.registerComponentCommand("say", func(player string, args []string) reply {
		return s.runSay(player, args)
	})
	s.registerComponentCommand("me", func(player string, args []string) reply {
		return s.runMe(player, args)
	})
	for _, name := range []string{"msg", "tell", "w"} {
		s.registerComponentCommand(name, func(player string, args []string) reply {
			return s.runMsg(player, args)
		})
	}
	// experience is xp's long name; they are the same node.
	s.registerComponentCommand("experience", func(player string, args []string) reply {
		return s.runXp(player, args)
	})
}

// runDifficulty implements /difficulty: bare queries the current
// level, a name switches it and fans Change Difficulty out, anything
// else is the unknown-argument error at the bad token.
func (s *Server) runDifficulty(args []string) reply {
	if len(args) == 0 {
		s.rulesMu.Lock()
		name := difficultyName(s.difficulty)
		s.rulesMu.Unlock()
		return componentReply(translatable("commands.difficulty.query", translatable("options.difficulty."+name)))
	}
	var d byte
	switch args[0] {
	case "peaceful":
		d = 0
	case "easy":
		d = 1
	case "normal":
		d = 2
	case "hard":
		d = 3
	default:
		return errToken("command.unknown.argument", 1)
	}
	if len(args) > 1 {
		return errToken("command.unknown.argument", 2)
	}
	s.setDifficulty(d)
	return opsEchoReply(translatable("commands.difficulty.success", translatable("options.difficulty."+args[0])))
}

// worldSeed reports the seed the world was generated with: the
// configured WorldSeed when one is set, else the flat generator's
// fixed seed.
func (s *Server) worldSeed() int64 {
	if s.WorldSeed != 0 {
		return s.WorldSeed
	}
	return 1
}

// runSeed implements /seed: the world seed as a green bracketed
// component that copies itself on click, wrapped in
// chat.square_brackets.
func (s *Server) runSeed(args []string) reply {
	if len(args) > 0 {
		return errToken("command.unknown.argument", 1)
	}
	seed := strconv.FormatInt(s.worldSeed(), 10)
	inner := component{
		text:      seed,
		copyTo:    seed,
		insertion: seed,
		color:     "green",
		hover: map[string]any{
			"action": "show_text",
			"value":  component{translate: "chat.copy.click"}.nested(),
		},
	}
	return componentReply(translatable("commands.seed.success", translatable("chat.square_brackets", inner)))
}

// runList implements /list: players.joined names, or
// commands.list.nameAndId pairs under `list uuids`.
func (s *Server) runList(args []string) reply {
	uuids := false
	if len(args) > 0 {
		if args[0] != "uuids" {
			return errToken("command.unknown.argument", 1)
		}
		uuids = true
	}
	if len(args) > 1 {
		return errToken("command.unknown.argument", 2)
	}
	var sess []*Session
	s.sessions.Range(func(_, v any) bool {
		se := v.(*Session)
		if se.state.Load() != StatePlay || se.getName() == "" {
			return true
		}
		sess = append(sess, se)
		return true
	})
	sort.Slice(sess, func(i, j int) bool { return sess[i].getName() < sess[j].getName() })
	var names []component
	for _, se := range sess {
		if uuids {
			u := se.getUUID()
			names = append(names, translatable("commands.list.nameAndId",
				se.getName(),
				fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])))
		} else {
			names = append(names, playerChatName(se))
		}
	}
	var list component
	switch len(names) {
	case 0:
		list = component{text: ""}
	case 1:
		list = names[0]
	default:
		extra := make([]component, 0, 2*len(names)-1)
		for i, n := range names {
			if i > 0 {
				extra = append(extra, component{text: ", "})
			}
			extra = append(extra, n)
		}
		list = component{text: "", extra: extra}
	}
	return componentReply(translatable("commands.list.players",
		int32(len(sess)), int32(s.MaxPlayers), list))
}

// runWhitelist implements /whitelist on|off|add <name>|remove <name>|
// list|reload. on/off and the name ops each have their own
// already/failed keys; none of them broadcast to the other operators.
func (s *Server) runWhitelist(args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	var key string
	switch args[0] {
	case "on", "off":
		on := args[0] == "on"
		if s.whitelistOn.Load() == on {
			if on {
				return componentReply(commandError("commands.whitelist.alreadyOn"))
			}
			return componentReply(commandError("commands.whitelist.alreadyOff"))
		}
		s.whitelistOn.Store(on)
		if on {
			key = "commands.whitelist.enabled"
		} else {
			key = "commands.whitelist.disabled"
		}
	case "list":
		s.banMu.Lock()
		names := make([]string, 0, len(s.whitelisted))
		for n := range s.whitelisted {
			names = append(names, n)
		}
		s.banMu.Unlock()
		sort.Strings(names)
		if len(names) == 0 {
			return componentReply(translatable("commands.whitelist.none"))
		}
		// The names arg rides the with list as a compound so the int
		// count serializes as the {"":n} form vanilla's NBT codec
		// produces for mixed lists.
		return componentReply(translatable("commands.whitelist.list",
			int32(len(names)), nestedCompound{key: "", value: strings.Join(names, ", ")}))
	case "add", "remove":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		name := args[1]
		if sess := s.findSessionByName(name); sess != nil {
			name = sess.getName()
		}
		s.banMu.Lock()
		if s.whitelisted == nil {
			s.whitelisted = map[string]bool{}
		}
		has := s.whitelisted[name]
		if args[0] == "add" {
			s.whitelisted[name] = true
			s.whitelistAdd(name)
		} else {
			delete(s.whitelisted, name)
			s.whitelistRemove(name)
		}
		s.banMu.Unlock()
		if args[0] == "add" {
			if has {
				return componentReply(commandError("commands.whitelist.add.failed"))
			}
			return opsEchoReply(translatable("commands.whitelist.add.success", name))
		}
		if !has {
			return componentReply(commandError("commands.whitelist.remove.failed"))
		}
		return opsEchoReply(translatable("commands.whitelist.remove.success", name))
	case "reload":
		s.reloadWhitelist()
		key = "commands.whitelist.reloaded"
	default:
		return errToken("command.unknown.argument", 1)
	}
	if len(args) > 1 {
		return errToken("command.unknown.argument", 2)
	}
	return opsEchoReply(translatable(key))
}

// runBanlist implements /banlist: both forms report the empty ban
// table.
func (s *Server) runBanlist(args []string) reply {
	if len(args) > 0 {
		switch args[0] {
		case "ips", "players":
		default:
			return errToken("command.unknown.argument", 1)
		}
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
	}
	return componentReply(translatable("commands.banlist.none"))
}

// runSetIdleTimeout implements /setidletimeout <minutes>.
func (s *Server) runSetIdleTimeout(args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		return errToken("parsing.int.expected", 1)
	}
	if n < 0 {
		return errToken("argument.integer.low", 1, int32(0), int32(n))
	}
	if len(args) > 1 {
		return errToken("command.unknown.argument", 2)
	}
	s.idleTimeoutMinutes.Store(int32(n))
	if n == 0 {
		return opsEchoReply(translatable("commands.setidletimeout.success.disabled"))
	}
	return opsEchoReply(translatable("commands.setidletimeout.success", int32(n)))
}

// runSay implements /say <message> — the say_command disguised chat
// broadcast, silent to the command system.
func (s *Server) runSay(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	name := literal(player)
	if sess := s.findSessionByName(player); sess != nil {
		name = playerChatName(sess)
	}
	s.broadcastDisguisedChat("say_command", name, s.messageText(player, strings.Join(args, " ")))
	return reply{}
}

// runMe implements /me <action> — the emote_command disguised chat
// broadcast.
func (s *Server) runMe(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	name := literal(player)
	if sess := s.findSessionByName(player); sess != nil {
		name = playerChatName(sess)
	}
	s.broadcastDisguisedChat("emote_command", name, s.messageText(player, strings.Join(args, " ")))
	return reply{}
}

// runMsg implements /msg|/tell|/w <targets> <message>: player-only
// targeting, incoming/outgoing disguised chat to each side.
func (s *Server) runMsg(player string, args []string) reply {
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args))
	}
	targets, r := s.playerTargets(player, args, 1)
	if r != nil {
		return *r
	}
	sender := s.findSessionByName(player)
	name := literal(player)
	if sender != nil {
		name = playerChatName(sender)
	}
	content := s.messageText(player, strings.Join(args[1:], " "))
	incoming := registryEntryID("minecraft:chat_type", "minecraft:msg_command_incoming")
	outgoing := registryEntryID("minecraft:chat_type", "minecraft:msg_command_outgoing")
	for _, target := range targets {
		if sender != nil && outgoing >= 0 {
			tName := playerChatName(target)
			sendDisguisedChat(sender, outgoing, name, content, &tName)
		}
		if incoming >= 0 {
			sendDisguisedChat(target, incoming, name, content, nil)
		}
	}
	return reply{}
}
