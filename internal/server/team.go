package server

// Scoreboard teams: the /team command family, the SetPlayerTeam packet
// they broadcast, and /teammsg's team-scoped chat. Reply shapes and
// packet bodies were measured on vanilla 26.2 — see
// TestTeamCommandParity.

import (
	"strings"

	"mc-go/internal/protocol"
)

// team is one scoreboard team: a name, a display name shown for it in
// replies, member styling (color, prefix, suffix), behaviour flags and
// the entity names that joined it. mc-go tracks players only.
type team struct {
	name     string
	display  string // literal display text; defaults to name
	prefix   string
	suffix   string
	color    int32 // ChatFormatting id; -1 = unset
	vis      int32 // name-tag visibility enum ordinal; always=0
	deathVis int32 // death-message visibility; state only, not on the wire
	coll     int32 // collision rule enum ordinal; always=0
	flags    byte  // bit1 friendlyFire | bit2 seeFriendlyInvisibles
	members  map[string]bool
	order    []string // member names in join order
}

// chatFormattingName maps a color name to its ChatFormatting id; the
// wire carries the id inside an optional marker byte + varint.
var chatFormattingName = map[string]int32{
	"black": 0, "dark_blue": 1, "dark_green": 2, "dark_aqua": 3,
	"dark_red": 4, "dark_purple": 5, "gold": 6, "gray": 7,
	"dark_gray": 8, "blue": 9, "green": 10, "aqua": 11, "red": 12,
	"light_purple": 13, "yellow": 14, "white": 15,
}

var chatFormattingID = func() map[int32]string {
	m := map[int32]string{}
	for n, id := range chatFormattingName {
		m[id] = n
	}
	return m
}()

// runTeam implements /team: add, remove, join, leave, empty, list and
// modify. Vanilla's replies are components; every mutation also
// broadcasts a SetPlayerTeam packet.
func (s *Server) runTeam(player string, args []string) reply {
	if len(args) == 0 {
		return textReply("Usage: /team <add|remove|join|leave|empty|list|modify> ...")
	}
	sender := s.findSessionByName(player)
	switch args[0] {
	case "add":
		if len(args) < 2 {
			return textReply("Usage: /team add <team> [displayName]")
		}
		name := args[1]
		if s.board.teamByName(name) != nil {
			return componentReply(commandError("commands.team.add.duplicate"))
		}
		display := name
		if len(args) > 2 {
			display = unquote(strings.Join(args[2:], " "))
		}
		t := &team{name: name, display: display, color: -1, flags: 0x03,
			members: map[string]bool{}}
		s.board.mu.Lock()
		if s.board.teams == nil {
			s.board.teams = map[string]*team{}
		}
		s.board.teams[name] = t
		s.board.mu.Unlock()
		// Vanilla emits the create packet and then a full info update.
		s.broadcastTeamCreate(t)
		s.broadcastTeamUpdate(t)
		msg := translatable("commands.team.add.success", t.brackets())
		return opsEchoReply(msg)
	case "remove":
		if len(args) < 2 {
			return textReply("Usage: /team remove <team>")
		}
		t := s.board.teamByName(args[1])
		if t == nil {
			return componentReply(commandError("team.notFound", args[1]))
		}
		s.board.mu.Lock()
		delete(s.board.teams, t.name)
		s.board.mu.Unlock()
		s.broadcastTeamPacket(t.name, 1, nil)
		msg := translatable("commands.team.remove.success", t.brackets())
		return opsEchoReply(msg)
	case "join":
		if len(args) < 2 {
			return textReply("Usage: /team join <team> [members]")
		}
		t := s.board.teamByName(args[1])
		if t == nil {
			return componentReply(commandError("team.notFound", args[1]))
		}
		targets := []string{player}
		if len(args) > 2 {
			targets = args[2:]
		}
		var names []string
		for _, tok := range targets {
			if len(tok) > 0 && tok[0] == '@' {
				// Entity selectors join every match under its
				// scoreboard name — a mob's is its UUID string.
				cands, rep := s.selectDataEntities(sender, tok, 2, false, nil)
				if rep != nil {
					return *rep
				}
				for _, d := range cands {
					names = append(names, d.selHolder())
				}
				continue
			}
			if sess := s.findSessionByName(tok); sess != nil {
				names = append(names, sess.getName())
				continue
			}
			// ScoreHolder arguments accept free-form names — a holder
			// need not be a live entity.
			names = append(names, tok)
		}
		for _, n := range names {
			s.teamJoin(t, n)
		}
		var msg component
		if len(names) == 1 {
			if sess := s.findSessionByName(names[0]); sess != nil {
				msg = translatable("commands.team.join.success.single",
					s.teamMemberName(sess), t.brackets())
			} else {
				msg = translatable("commands.team.join.success.single",
					literal(names[0]), t.brackets())
			}
		} else {
			msg = translatable("commands.team.join.success.multiple",
				nestedCompound{key: "", value: int32(len(names))}, t.brackets())
		}
		return opsEchoReply(msg)
	case "leave":
		targets := []string{player}
		if len(args) > 1 {
			targets = args[1:]
		}
		var names []string
		for _, tok := range targets {
			if len(tok) > 0 && tok[0] == '@' {
				cands, rep := s.selectDataEntities(sender, tok, 1, false, nil)
				if rep != nil {
					return *rep
				}
				for _, d := range cands {
					names = append(names, d.selHolder())
				}
				continue
			}
			if sess := s.findSessionByName(tok); sess != nil {
				names = append(names, tok)
				continue
			}
			matched := false
			for _, sess := range s.resolveTargets(player, tok) {
				names = append(names, sess.getName())
				matched = true
			}
			if !matched {
				names = append(names, tok)
			}
		}
		for _, n := range names {
			s.teamLeave(n)
		}
		var msg component
		if len(names) == 1 {
			msg = translatable("commands.team.leave.success.single",
				s.plainTargetName(names[0]))
		} else {
			msg = translatable("commands.team.leave.success.multiple",
				nestedCompound{key: "", value: int32(len(names))})
		}
		return opsEchoReply(msg)
	case "empty":
		if len(args) < 2 {
			return textReply("Usage: /team empty <team>")
		}
		t := s.board.teamByName(args[1])
		if t == nil {
			return componentReply(commandError("team.notFound", args[1]))
		}
		if len(t.order) == 0 {
			return componentReply(commandError("commands.team.empty.unchanged"))
		}
		count := int32(len(t.order))
		for _, n := range append([]string{}, t.order...) {
			s.broadcastTeamPacket(t.name, 4, playerListBody([]string{n}))
			s.teamDrop(t, n)
		}
		msg := translatable("commands.team.empty.success", nestedCompound{key: "", value: count}, t.brackets())
		return opsEchoReply(msg)
	case "list":
		if len(args) > 1 {
			t := s.board.teamByName(args[1])
			if t == nil {
				return componentReply(commandError("team.notFound", args[1]))
			}
			if len(t.order) == 0 {
				return opsEchoReply(translatable(
					"commands.team.list.members.empty", t.brackets()))
			}
			var names []component
			for i, n := range t.order {
				if i > 0 {
					names = append(names, component{color: "gray", text: ", "})
				}
				names = append(names, component{color: "green", text: n})
			}
			memberList := names[0]
			if len(names) > 1 {
				memberList = component{text: "", extra: names}
			}
			return opsEchoReply(translatable(
				"commands.team.list.members.success",
				t.brackets(), nestedCompound{key: "", value: int32(len(t.order))},
				memberList))
		}
		var all []*team
		s.board.mu.Lock()
		for _, t := range s.board.teams {
			all = append(all, t)
		}
		s.board.mu.Unlock()
		if len(all) == 0 {
			return componentReply(translatable("commands.team.list.teams.empty"))
		}
		sortTeams(all)
		var names []component
		for i, t := range all {
			if i > 0 {
				names = append(names, component{color: "gray", text: ", "})
			}
			names = append(names, t.brackets())
		}
		teamList := names[0]
		if len(names) > 1 {
			teamList = component{text: "", extra: names}
		}
		return opsEchoReply(translatable(
			"commands.team.list.teams.success",
			nestedCompound{key: "", value: int32(len(all))},
			teamList))
	case "modify":
		if len(args) < 4 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		t := s.board.teamByName(args[1])
		if t == nil {
			return componentReply(commandError("team.notFound", args[1]))
		}
		return s.teamModify(sender, t, args)
	}
	return textReply("Unknown team subcommand: " + args[0])
}

// unquote strips one level of surrounding quotes — display names and
// affixes arrive quoted when they carry spaces.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// sortTeams orders a team list the way /team list prints it: by name.
func sortTeams(ts []*team) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j-1].name > ts[j].name; j-- {
			ts[j-1], ts[j] = ts[j], ts[j-1]
		}
	}
}

// teamModify handles the writable team properties. Option literals
// are case-sensitive — Brigadier's literal parser — and prefix and
// suffix are the only writes that skip the other-operator echo.
// Token indices mark where vanilla's parser would point: the property
// is input token 3, the value token 4.
func (s *Server) teamModify(sender *Session, t *team, args []string) reply {
	prop := args[2]
	value := strings.Join(args[3:], " ")
	var msg component
	echo := true
	switch prop {
	case "displayName":
		v := unquote(value)
		if v == t.display {
			return componentReply(commandError("commands.team.option.name.unchanged"))
		}
		t.display = v
		msg = translatable("commands.team.option.name.success", t.brackets())
	case "prefix":
		t.prefix = unquote(value)
		msg = translatable("commands.team.option.prefix.success", t.prefix)
		echo = false
	case "suffix":
		t.suffix = unquote(value)
		msg = translatable("commands.team.option.suffix.success", t.suffix)
		echo = false
	case "color":
		low := strings.ToLower(value)
		if low == "reset" {
			if t.color < 0 {
				return componentReply(commandError("commands.team.option.color.unchanged"))
			}
			t.color = -1
			msg = translatable("commands.team.option.color.clear.success", t.brackets())
			break
		}
		id, ok := chatFormattingName[low]
		if !ok {
			return errTokenEnd("argument.color.invalid", 4, value)
		}
		if t.color == id {
			return componentReply(commandError("commands.team.option.color.unchanged"))
		}
		t.color = id
		msg = translatable("commands.team.option.color.success",
			t.brackets(), component{emptyKey: low, forceCompound: true})
	case "friendlyFire", "seeFriendlyInvisibles":
		bit := byte(0x01)
		key := "friendlyfire"
		if prop == "seeFriendlyInvisibles" {
			bit = 0x02
			key = "seeFriendlyInvisibles"
		}
		var want bool
		switch value {
		case "true":
			want = true
		case "false":
		default:
			return errToken("parsing.bool.invalid", 4, value)
		}
		have := t.flags&bit != 0
		if want == have {
			state := "Disabled"
			if want {
				state = "Enabled"
			}
			return componentReply(commandError("commands.team.option." + key +
				".already" + state))
		}
		if want {
			t.flags |= bit
		} else {
			t.flags &^= bit
		}
		state := "disabled"
		if want {
			state = "enabled"
		}
		msg = translatable("commands.team.option."+key+"."+state, t.brackets())
	case "collisionRule":
		id, name, ok := teamCollID(value)
		if !ok {
			return errToken("command.unknown.argument", 4)
		}
		if t.coll == id {
			return componentReply(commandError("commands.team.option.collisionRule.unchanged"))
		}
		t.coll = id
		msg = translatable("commands.team.option.collisionRule.success",
			t.brackets(), translatable("team.collision."+name))
	case "nametagVisibility", "deathMessageVisibility":
		id, name, ok := teamVisID(value)
		if !ok {
			return errToken("command.unknown.argument", 4)
		}
		field := &t.vis
		if prop == "deathMessageVisibility" {
			field = &t.deathVis
		}
		if *field == id {
			return componentReply(commandError("commands.team.option." + prop + ".unchanged"))
		}
		*field = id
		msg = translatable("commands.team.option."+prop+".success",
			t.brackets(), translatable("team.visibility."+name))
	default:
		return errToken("command.unknown.argument", 3)
	}
	s.broadcastTeamUpdate(t)
	// onTeamChanged: every online member's waypoint conns re-create.
	s.wpRemakeTeam(t)
	r := componentReply(msg)
	r.opsEcho = echo
	return r
}

// teamVisID maps a visibility value to its enum ordinal: always,
// never, hideForOtherTeams, hideForOwnTeam in declaration order.
func teamVisID(v string) (id int32, name string, ok bool) {
	switch v {
	case "always":
		return 0, "always", true
	case "never":
		return 1, "never", true
	case "hideForOtherTeams":
		return 2, "hideForOtherTeams", true
	case "hideForOwnTeam":
		return 3, "hideForOwnTeam", true
	}
	return 0, "", false
}

// teamCollID maps a collisionRule value to its enum ordinal: always,
// never, pushOtherTeams, pushOwnTeam.
func teamCollID(v string) (id int32, name string, ok bool) {
	switch v {
	case "always":
		return 0, "always", true
	case "never":
		return 1, "never", true
	case "pushOtherTeams":
		return 2, "pushOtherTeams", true
	case "pushOwnTeam":
		return 3, "pushOwnTeam", true
	}
	return 0, "", false
}

// teamByName resolves a team, nil when none exists.
func (b *scoreboard) teamByName(name string) *team {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.teams[name]
}

// teamOf is the team a player name belongs to.
func (s *Server) teamOf(name string) *team {
	s.board.mu.Lock()
	defer s.board.mu.Unlock()
	for _, t := range s.board.teams {
		if t.members[name] {
			return t
		}
	}
	return nil
}

// teamJoin moves a player name into t, leaving their previous team
// first — the wire pair is remove-from-old then add-to-new, even when
// the old team is t itself. A re-join keeps the member's slot: the
// member list prints in first-join order.
func (s *Server) teamJoin(t *team, name string) {
	if old := s.teamOf(name); old != nil {
		s.broadcastTeamPacket(old.name, 4, playerListBody([]string{name}))
		if old != t {
			s.teamDrop(old, name)
		} else {
			// old==t keeps membership, but vanilla still runs the
			// removePlayerFromTeam waypoint remake (TRACK re-emit).
			s.wpRemakeByName(name)
		}
	}
	s.board.mu.Lock()
	if !t.members[name] {
		t.members[name] = true
		t.order = append(t.order, name)
	}
	s.board.mu.Unlock()
	s.broadcastTeamPacket(t.name, 3, playerListBody([]string{name}))
	s.wpRemakeByName(name)
}

// teamLeave drops a name from whatever team holds it.
func (s *Server) teamLeave(name string) {
	if t := s.teamOf(name); t != nil {
		s.broadcastTeamPacket(t.name, 4, playerListBody([]string{name}))
		s.teamDrop(t, name)
	}
}

// teamDrop removes a member without broadcasting. The waypoint remake
// fires for the removal like vanilla's removePlayerFromTeam listener.
func (s *Server) teamDrop(t *team, name string) {
	s.board.mu.Lock()
	delete(t.members, name)
	for i, n := range t.order {
		if n == name {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
	s.board.mu.Unlock()
	s.wpRemakeByName(name)
}

// brackets is the team's display name as the square-bracketed component
// vanilla's team replies carry — chat.square_brackets wrapping the
// display text with the team's name as hover/insertion.
func (t *team) brackets() component {
	c := component{
		translate: "chat.square_brackets",
		with: []any{component{
			text:      t.display,
			insertion: t.name,
			hover: map[string]any{
				"action": "show_text",
				"value":  t.name,
			},
		}},
	}
	if t.color >= 0 {
		c.color = chatFormattingID[t.color]
	}
	return c
}

// chatName is the team name inside team_msg disguised-chat bindings:
// square brackets carrying the /teammsg suggest click and the
// chat.type.team.hover tooltip, around the plain team name with its
// own show_text name hover.
func (t *team) chatName() component {
	c := component{
		translate: "chat.square_brackets",
		with: []any{component{
			insertion: t.name,
			text:      t.name,
			hover: map[string]any{
				"action": "show_text",
				"value":  t.name,
			},
		}},
		click: "/teammsg ",
		hover: map[string]any{
			"action": "show_text",
			"value":  map[string]any{"translate": "chat.type.team.hover"},
		},
	}
	if t.color >= 0 {
		c.color = chatFormattingID[t.color]
	}
	return c
}

// teamFormattedName is a player's display name with their team's
// styling folded in — vanilla's Entity.getDisplayName. A teamed player
// wraps prefix/name/suffix literals in extra under the decorated name;
// a teamless player is the plain decorated name.
func (s *Server) teamFormattedName(sess *Session) component {
	name := sess.getName()
	t := s.teamOf(name)
	if t == nil {
		return s.entityTargetComponent(sess)
	}
	c := s.entityTargetComponent(sess)
	c.text = ""
	c.extra = []component{literal(t.prefix), literal(name), literal(t.suffix)}
	if t.color >= 0 {
		c.color = chatFormattingID[t.color]
	}
	return c
}

// teamMemberName is the name component /team join's reply uses: the
// same prefix/name/suffix wrapper but with a show_text hover instead
// of show_entity.
func (s *Server) teamMemberName(sess *Session) component {
	name := sess.getName()
	c := s.plainTargetName(name)
	t := s.teamOf(name)
	if t == nil {
		return c
	}
	c.text = ""
	c.extra = []component{literal(t.prefix), literal(name), literal(t.suffix)}
	if t.color >= 0 {
		c.color = chatFormattingID[t.color]
	}
	return c
}

// plainTargetName is the undecorated name component vanilla uses for
// the /team leave reply: literal name, /tell click, show_text hover.
func (s *Server) plainTargetName(name string) component {
	return component{
		text:      name,
		click:     "/tell " + name + " ",
		insertion: name,
		hover: map[string]any{
			"action": "show_text",
			"value":  name,
		},
	}
}

// broadcastTeamPacket sends one SetPlayerTeam packet to every
// play-state session: method byte plus a per-method tail.
func (s *Server) broadcastTeamPacket(name string, method byte, tail []byte) {
	var b protocol.Buffer
	b.String(name)
	b.Byte(method)
	b.Raw(tail)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() == StatePlay {
			_ = sess.write(sess.ids().PlaySetPlayerTeam, body)
		}
		return true
	})
}

// teamInfoBody is the team-info field group shared by create and
// update: display, prefix, suffix components, then the visibility,
// collision and optional color varints and the flags byte.
func teamInfoBody(t *team) []byte {
	var b protocol.Buffer
	b.Raw(literal(t.display).content())
	b.Raw(literal(t.prefix).content())
	b.Raw(literal(t.suffix).content())
	b.VarInt(t.vis)
	b.VarInt(t.coll)
	if t.color >= 0 {
		b.Byte(1)
		b.VarInt(t.color)
	} else {
		b.Byte(0)
	}
	b.Byte(t.flags)
	return b.Bytes()
}

// broadcastTeamCreate emits method 0: info fields plus the member
// list. The display field here is the bare team name — vanilla creates
// the team unnamed and sets its display in the update that follows.
func (s *Server) broadcastTeamCreate(t *team) {
	var tail protocol.Buffer
	copy := *t
	copy.display = t.name
	tail.Raw(teamInfoBody(&copy))
	tail.VarInt(int32(len(t.order)))
	for _, n := range t.order {
		tail.String(n)
	}
	s.broadcastTeamPacket(t.name, 0, tail.Bytes())
}

// broadcastTeamUpdate emits method 2: the info fields again.
func (s *Server) broadcastTeamUpdate(t *team) {
	s.broadcastTeamPacket(t.name, 2, teamInfoBody(t))
}

// playerListBody is the mode-3/4 tail: VarInt count then names.
func playerListBody(names []string) []byte {
	var b protocol.Buffer
	b.VarInt(int32(len(names)))
	for _, n := range names {
		b.String(n)
	}
	return b.Bytes()
}

// runTeamMsg implements /teammsg (/tm): a disguised chat to the
// sender's own team only.
func (s *Server) runTeamMsg(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	t := s.teamOf(player)
	if t == nil {
		return componentReply(commandError("commands.teammsg.failed.noteam"))
	}
	sender := s.findSessionByName(player)
	content := s.messageText(player, strings.Join(args, " "))
	incoming := registryEntryID("minecraft:chat_type", "minecraft:team_msg_command_incoming")
	outgoing := registryEntryID("minecraft:chat_type", "minecraft:team_msg_command_outgoing")
	name := s.teamFormattedName(sender)
	teamName := t.chatName()
	if sender != nil && outgoing >= 0 {
		sendDisguisedChat(sender, outgoing, name, content, &teamName)
	}
	for _, member := range t.order {
		target := s.findSessionByName(member)
		if target == nil || target == sender {
			continue
		}
		if incoming >= 0 {
			sendDisguisedChat(target, incoming, name, content, &teamName)
		}
	}
	return reply{}
}
