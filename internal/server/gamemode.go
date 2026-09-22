package server

// parseGameMode maps a /gamemode argument to its id. Vanilla only
// accepts the exact lowercase names; anything else (including
// capitalised variants) fails with argument.gamemode.invalid.
func parseGameMode(tok string) (byte, bool) {
	switch tok {
	case "survival":
		return GameModeSurvival, true
	case "creative":
		return GameModeCreative, true
	case "adventure":
		return GameModeAdventure, true
	case "spectator":
		return GameModeSpectator, true
	}
	return 0, false
}

// runGamemode implements /gamemode, measured against vanilla 26.2. A
// mode change sends the target GameEvent reason 3 with the mode, a
// commands.gamemode.success.self chat to the issuer (or
// .success.other naming the target), gameMode.changed to a remote
// target plus a gray-italic chat.type.admin broadcast to the other
// operators (which also fires on self switches), refreshed abilities,
// re-sent reach attributes to the target when creative is left or
// entered, and a gamemode-only Player Info Update to every player,
// target included. Repeating the current
// mode is silent; the mode argument is required, unknown names fail,
// an unknown target fails, and a trailing argument is rejected.
func (s *Server) runGamemode(player string, args []string) reply {
	sess := s.findSessionByName(player)
	if sess == nil || sess.opLevel() < 2 {
		// Hidden command, exactly as an unknown one reads.
		return reply{comp: unknownCommand(), styled: true, fail: &failMark{}}
	}
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 1)
	}
	mode, ok := parseGameMode(args[0])
	if !ok {
		// Vanilla reads the token and rejects the value, so the
		// cursor stops at the end of the token rather than its start.
		return errTokenEnd("argument.gamemode.invalid", 1, args[0])
	}
	target := sess
	if len(args) > 1 {
		target = s.resolveTarget(player, args[1])
		if target == nil {
			return componentReply(commandError("argument.entity.notfound.player"))
		}
	}
	if len(args) > 2 {
		return errToken("command.unknown.argument", 3)
	}
	// Vanilla's setGameMode returns false on an unchanged mode and the
	// command emits nothing at all — no chat, no packets.
	old := target.mode()
	if old == mode {
		return reply{}
	}
	target.setMode(mode)
	if mode != GameModeCreative && mode != GameModeSpectator {
		// Leaving a flighted mode grounds the player: a later switch
		// back to creative resends the bare creative flags, with no
		// flying bit, exactly as the real server does.
		target.flying = false
	}
	_ = s.sendAbilities(target)
	_ = s.sendGameEvent(target, 3, float32(mode))
	modeName := translatable("gameMode." + args[0])
	if target != sess {
		s.sendSystemChatComponent(target, translatable("gameMode.changed", modeName))
	}
	var selfMsg component
	if target == sess {
		selfMsg = translatable("commands.gamemode.success.self", modeName)
	} else {
		selfMsg = translatable("commands.gamemode.success.other", s.entityTargetComponent(target), modeName)
	}
	s.broadcastAdmin(sess, selfMsg)
	if old == GameModeCreative || mode == GameModeCreative {
		// The reach resync goes to the changed player only: trackers
		// get the tab-list update, not the attributes.
		_ = target.write(target.ids().PlayUpdateAttributes, updateAttributesMode(target.entityID.Load(), mode))
	}
	s.broadcastPlayerInfoGamemode(target)
	return componentReply(selfMsg)
}

// broadcastAdmin relays a command's message to the other operators as
// a gray-italic chat.type.admin broadcast naming the issuing player,
// which is how vanilla reports one operator's command to the rest.
// Which permission levels see it is unmeasured; mc-go shows it to all
// operators.
func (s *Server) broadcastAdmin(sender *Session, msg component) {
	c := component{
		color:     "gray",
		italic:    true,
		translate: "chat.type.admin",
		with:      []any{s.entityTargetComponent(sender), msg},
	}
	body := systemChatBody(c)
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other == sender || other.state.Load() != StatePlay || other.opLevel() == 0 {
			return true
		}
		_ = other.write(other.ids().PlaySystemChatCB, body)
		return true
	})
}
