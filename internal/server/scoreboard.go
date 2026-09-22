package server

import (
	"bytes"
	"strconv"
	"strings"
	"sync"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

// scoreboard holds objectives (name -> def) and per-holder scores,
// plus the sidebar display slot and the team table.
type scoreboard struct {
	mu         sync.Mutex
	objectives map[string]*sbObjective // name -> objective
	scores     map[string]map[string]int32
	// scoreOrder and holderOrder record insertion order: vanilla's
	// per-holder score map and tracked-holder set iterate in
	// insertion/JVM-hash order, and the wire-visible parts of that
	// order are pinned by the tests.
	scoreOrder   map[string][]string // holder -> objective names
	holderOrder  []string            // holder names in creation order
	sidebar      string
	displaySlots map[int]string // slot id -> objective name
	teams        map[string]*team
}

// sbObjective is a scoreboard objective: the criteria name is stored
// only for the packet, display is the parsed component tree used in
// square-bracket replies (nil means the bare name).
type sbObjective struct {
	criteria string
	display  any
}

// objectiveChatComponent renders an objective the way vanilla's
// scoreboard replies do: the display name wrapped in square brackets
// with a show_text hover of the objective name.
func (s *Server) objectiveChatComponent(name string) component {
	display := any(name)
	if o := s.board.objectives[name]; o != nil && o.display != nil {
		display = o.display
	}
	inner := component{
		hover: map[string]any{
			"action": "show_text",
			"value":  name,
		},
	}
	switch v := display.(type) {
	case map[string]any:
		// A parsed display component keeps its own fields and gains
		// the hover, the same merge vanilla's wrapInSquareBrackets
		// performs on the display-name component.
		innerMap := map[string]any{}
		for k, e := range v {
			innerMap[k] = e
		}
		innerMap["hover_event"] = inner.hover
		return translatable("chat.square_brackets", innerMap)
	case string:
		inner.text = v
	}
	return translatable("chat.square_brackets", inner)
}

// objectiveDisplay renders an objective's bare display name — the
// custom display component when one was given at add, else the name
// itself, with no bracket wrap or hover. setdisplay uses this form.
func (s *Server) objectiveDisplay(name string) any {
	if o := s.board.objectives[name]; o != nil && o.display != nil {
		if m, ok := o.display.(map[string]any); ok {
			return m
		}
		if str, ok := o.display.(string); ok {
			return literal(str)
		}
		return o.display
	}
	return name
}

// scoreHolderComponent renders a score holder in a reply: a live
// player gets the tell-suggest + show_text name; a fake name is plain.
func scoreHolderComponent(name string, sess *Session) component {
	if sess != nil && sess.getName() == name {
		return component{
			text:      name,
			insertion: name,
			click:     "/tell " + name + " ",
			hover: map[string]any{
				"action": "show_text",
				"value":  name,
			},
		}
	}
	return literal(name)
}

// sbEntityName renders a non-player holder the way the scoreboard
// replies name it: the entity display name (type translatable or
// custom name) with the UUID string as insertion and a show_text
// hover of that UUID — the shape Entity.getDisplayName produces.
func sbEntityName(e *Entity) component {
	typeName, ok := EntityTypeName(e.Type)
	if !ok {
		typeName = "pig"
	}
	u := e.UUID
	uuidStr := sprintUUID(u)
	var comp component
	if e.CustomName != "" {
		comp = component{text: e.CustomName}
	} else {
		comp = component{translate: "entity.minecraft." + typeName}
	}
	comp.insertion = uuidStr
	comp.hover = map[string]any{
		"action": "show_text",
		"value":  uuidStr,
	}
	return comp
}

// sprintUUID formats a 16-byte UUID as the hyphenated string vanilla
// uses for entity scoreboard names.
func sprintUUID(u [16]byte) string {
	hex := "0123456789abcdef"
	var b [36]byte
	j := 0
	for i, c := range u {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			b[j] = '-'
			j++
		}
		b[j] = hex[c>>4]
		b[j+1] = hex[c&15]
		j += 2
	}
	return string(b[:])
}

// sbHolder is a resolved score-holder argument: the scoreboard key
// (player name, entity UUID string, or fake name) plus the entity the
// key came from when it is a live one, for display names in replies.
type sbHolder struct {
	name string
	sess *Session
	mob  *Entity
}

// arg renders the holder inside a translation's with list: fake names
// go out as bare strings, players and entities as their components.
func (h sbHolder) arg() any {
	if h.sess != nil {
		return scoreHolderComponent(h.name, h.sess)
	}
	if h.mob != nil {
		return sbEntityName(h.mob)
	}
	return h.name
}

// resolveHoldersPlural resolves a score_holders argument: a literal
// name stands for itself (a possibly fake holder), '*' widens to
// every tracked holder, and an entity selector resolves to every
// match — an empty selector match is argument.entity.notfound.entity.
func (s *Server) resolveHoldersPlural(sender *Session, tok string, tokIdx int) ([]sbHolder, *reply) {
	if tok == "*" {
		var out []sbHolder
		s.board.mu.Lock()
		for _, h := range s.board.holderOrder {
			out = append(out, s.holderFor(h))
		}
		s.board.mu.Unlock()
		return out, nil
	}
	if len(tok) > 0 && tok[0] == '@' {
		cands, rep := s.selectDataEntities(sender, tok, tokIdx, false)
		if rep != nil {
			return nil, rep
		}
		if len(cands) == 0 {
			r := componentReply(commandError("argument.entity.notfound.entity"))
			return nil, &r
		}
		out := make([]sbHolder, 0, len(cands))
		for _, d := range cands {
			out = append(out, sbHolder{name: d.selHolder(), sess: d.sess, mob: d.mob})
		}
		return out, nil
	}
	h := sbHolder{name: tok}
	if sess := s.findSessionByName(tok); sess != nil {
		h.sess = sess
	}
	return []sbHolder{h}, nil
}

// holderFor attaches the live entity to a tracked holder name when one
// is online or loaded, so replies render the decorated name.
func (s *Server) holderFor(name string) sbHolder {
	h := sbHolder{name: name}
	if sess := s.findSessionByName(name); sess != nil {
		h.sess = sess
		return h
	}
	for _, e := range s.entities.All() {
		if sprintUUID(e.UUID) == name {
			h.mob = &e
			break
		}
	}
	return h
}

// resolveHolderSingle resolves a single score_holder argument: a
// literal name resolves whether or not it exists, while a selector
// must be single-shaped — @e/@a/@r-shaped multi forms are a parse-time
// argument.entity.toomany at the token's end — and an empty match is
// argument.entity.notfound.entity.
func (s *Server) resolveHolderSingle(sender *Session, tok string, tokIdx int) (sbHolder, *reply) {
	if len(tok) > 0 && tok[0] == '@' {
		spec, serr := parseSelector(tok, tokIdx)
		if serr != nil {
			return sbHolder{}, serr
		}
		if spec.singleMaxRes() > 1 {
			r := errTokenEnd("argument.entity.toomany", tokIdx)
			return sbHolder{}, &r
		}
		cands, rep := s.selectDataEntities(sender, tok, tokIdx, false)
		if rep != nil {
			return sbHolder{}, rep
		}
		if len(cands) == 0 {
			r := componentReply(commandError("argument.entity.notfound.entity"))
			return sbHolder{}, &r
		}
		return sbHolder{name: cands[0].selHolder(), sess: cands[0].sess, mob: cands[0].mob}, nil
	}
	h := sbHolder{name: tok}
	if sess := s.findSessionByName(tok); sess != nil {
		h.sess = sess
	}
	return h, nil
}

// setScoreLocked writes a holder's score, creating the holder and its
// insertion order entries on first write. Callers hold board.mu.
func (s *Server) setScoreLocked(holder, objective string, value int32) {
	if s.board.scores == nil {
		s.board.scores = map[string]map[string]int32{}
		s.board.scoreOrder = map[string][]string{}
	}
	if s.board.scores[holder] == nil {
		s.board.scores[holder] = map[string]int32{}
		s.board.holderOrder = append(s.board.holderOrder, holder)
	}
	if _, ok := s.board.scores[holder][objective]; !ok {
		s.board.scoreOrder[holder] = append(s.board.scoreOrder[holder], objective)
	}
	s.board.scores[holder][objective] = value
}

// removeScoreLocked drops one score and the holder itself once it has
// none left, mirroring vanilla's tracked-player set.
func (s *Server) removeScoreLocked(holder, objective string) {
	scores := s.board.scores[holder]
	if scores == nil {
		return
	}
	delete(scores, objective)
	order := s.board.scoreOrder[holder]
	for i, o := range order {
		if o == objective {
			s.board.scoreOrder[holder] = append(order[:i:i], order[i+1:]...)
			break
		}
	}
	if len(scores) == 0 {
		delete(s.board.scores, holder)
		delete(s.board.scoreOrder, holder)
		for i, h := range s.board.holderOrder {
			if h == holder {
				s.board.holderOrder = append(s.board.holderOrder[:i:i], s.board.holderOrder[i+1:]...)
				break
			}
		}
	}
}

// joinScoreboardList renders formatList's joined form: the items as
// comma-separated extras under an empty text root, the separator gray.
func joinScoreboardList(items []component) component {
	out := component{text: ""}
	for i, it := range items {
		if i > 0 {
			out.extra = append(out.extra, component{color: "gray", text: ", "})
		}
		out.extra = append(out.extra, it)
	}
	return out
}

// validCriteria mirrors vanilla's ObjectiveCriteria registry: the
// fixed names, the per-color team-kill pair, and the minecraft.<stat>
// family, which requires a stat type and id joined by a colon.
func validCriteria(name string) bool {
	switch name {
	case "dummy", "trigger", "deathCount", "playerKillCount",
		"totalKillCount", "health", "xp", "level", "food", "air",
		"armor":
		return true
	}
	for _, prefix := range []string{"teamkill.", "killedByTeam."} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			for _, c := range vanillaTeamColors {
				if rest == c {
					return true
				}
			}
			return false
		}
	}
	if rest, ok := strings.CutPrefix(name, "minecraft."); ok {
		// minecraft.<stat-type>:<id> — the stat type is one of the
		// registry-backed buckets (custom, mined, used, ...).
		statType, _, ok := strings.Cut(rest, ":")
		if !ok {
			return false
		}
		switch statType {
		case "custom", "killed", "killed_by", "mined", "crafted",
			"used", "broken", "picked_up", "dropped":
			return true
		}
		return false
	}
	return false
}

// vanillaTeamColors are the 16 colors teamkill/killedByTeam criteria
// register one objective criterion each for.
var vanillaTeamColors = []string{
	"black", "dark_blue", "dark_green", "dark_aqua", "dark_red",
	"dark_purple", "gold", "gray", "dark_gray", "blue", "green",
	"aqua", "red", "light_purple", "yellow", "white",
}

// scoreboardSlotID maps a setdisplay slot name to the packet's slot
// id: list=0, sidebar=1, belowname=2 and the per-color team slots
// stack on 3 in color-table order.
func scoreboardSlotID(name string) (int, bool) {
	switch name {
	case "list":
		return 0, true
	case "sidebar":
		return 1, true
	case "below_name":
		return 2, true
	}
	if rest, ok := strings.CutPrefix(name, "sidebar.team."); ok {
		for i, c := range vanillaTeamColors {
			if rest == c {
				return 3 + i, true
			}
		}
	}
	return 0, false
}

// runScoreboard implements /scoreboard as vanilla's literal tree:
//
//	objectives list|add|remove|setdisplay|modify
//	players list|set|add|remove|reset|get|operation|enable|random|display
//
// A bare root or subcommand is command.unknown.command at the input's
// end; an unknown literal is command.unknown.argument at its start.
func (s *Server) runScoreboard(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "objectives":
		return s.runScoreboardObjectives(player, args)
	case "players":
		return s.runScoreboardPlayers(player, args)
	}
	return errToken("command.unknown.argument", 1)
}

func (s *Server) runScoreboardObjectives(player string, args []string) reply {
	// args[0] == "objectives"; token indexes count the command root.
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args))
	}
	switch args[1] {
	case "list":
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		s.board.mu.Lock()
		var names []string
		for name := range s.board.objectives {
			names = append(names, name)
		}
		s.board.mu.Unlock()
		if len(names) == 0 {
			return componentReply(translatable("commands.scoreboard.objectives.list.empty"))
		}
		// objectivesByName is a fastutil open map — the values() view
		// iterates in descending mix-hash slot order.
		items := make([]component, 0, len(names))
		for _, n := range fastutilSetOrder(names) {
			items = append(items, s.objectiveChatComponent(n))
		}
		return componentReply(translatable("commands.scoreboard.objectives.list.success",
			nestedCompound{key: "", value: len(items)},
			joinScoreboardList(items)))
	case "add":
		// objectives add <name> <criteria> [<display>]
		if len(args) < 4 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		name, criteria := args[2], args[3]
		if !validCriteria(criteria) {
			return errToken("argument.criteria.invalid", 4, criteria)
		}
		var display any
		if len(args) > 4 {
			joined := strings.Join(args[4:], " ")
			v, end, perr := parseComponentArg(joined)
			if perr != nil {
				return errAbsCmd(perr.key, compAbsOff(len("scoreboard "), args, 4, perr.pos), perr.with...)
			}
			if extra := strings.TrimSpace(joined[end:]); extra != "" {
				return errToken("command.unknown.argument", 5+componentTokAfter(args, joined, end))
			}
			display = v
		}
		s.board.mu.Lock()
		if s.board.objectives == nil {
			s.board.objectives = map[string]*sbObjective{}
		}
		if _, dup := s.board.objectives[name]; dup {
			s.board.mu.Unlock()
			return componentReply(commandError("commands.scoreboard.objectives.add.duplicate"))
		}
		s.board.objectives[name] = &sbObjective{criteria: criteria, display: display}
		s.board.mu.Unlock()
		s.broadcastObjectiveCreate(name, display)
		return componentReply(translatable("commands.scoreboard.objectives.add.success",
			s.objectiveChatComponent(name)))
	case "remove":
		if len(args) < 3 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		if len(args) > 3 {
			return errToken("command.unknown.argument", 4)
		}
		name := args[2]
		s.board.mu.Lock()
		if _, ok := s.board.objectives[name]; !ok {
			s.board.mu.Unlock()
			return componentReply(commandError("arguments.objective.notFound", name))
		}
		delete(s.board.objectives, name)
		for _, holder := range s.board.holderOrder {
			s.removeScoreLocked(holder, name)
		}
		if s.board.sidebar == name {
			s.board.sidebar = ""
		}
		s.board.mu.Unlock()
		s.broadcastObjectiveRemove(name)
		return componentReply(translatable("commands.scoreboard.objectives.remove.success",
			s.objectiveChatComponent(name)))
	case "setdisplay":
		// setdisplay <slot> [<objective>] — slot is one of list,
		// sidebar, belowname or sidebar.team.<color>; omitting the
		// objective clears the slot.
		if len(args) < 3 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		slot, ok := scoreboardSlotID(args[2])
		if !ok {
			return errTokenEnd("argument.scoreboardDisplaySlot.invalid", 3, args[2])
		}
		if len(args) > 4 {
			return errToken("command.unknown.argument", 5)
		}
		name := ""
		if len(args) == 4 {
			name = args[3]
		}
		s.board.mu.Lock()
		cur := s.board.displaySlots[slot]
		s.board.mu.Unlock()
		if name == "" {
			if cur == "" {
				return componentReply(commandError(
					"commands.scoreboard.objectives.display.alreadyEmpty"))
			}
			s.board.mu.Lock()
			if s.board.displaySlots == nil {
				s.board.displaySlots = map[int]string{}
			}
			s.board.displaySlots[slot] = ""
			s.board.mu.Unlock()
			s.broadcastDisplayObjective(int32(slot), "")
			return componentReply(translatable(
				"commands.scoreboard.objectives.display.cleared", args[2]))
		}
		s.board.mu.Lock()
		_, objOK := s.board.objectives[name]
		s.board.mu.Unlock()
		if !objOK {
			return componentReply(commandError("arguments.objective.notFound", name))
		}
		if cur == name {
			return componentReply(commandError(
				"commands.scoreboard.objectives.display.alreadySet"))
		}
		s.board.mu.Lock()
		if s.board.displaySlots == nil {
			s.board.displaySlots = map[int]string{}
		}
		s.board.displaySlots[slot] = name
		s.board.mu.Unlock()
		s.broadcastDisplayObjective(int32(slot), name)
		return componentReply(translatable(
			"commands.scoreboard.objectives.display.set",
			args[2], s.objectiveDisplay(name)))
	}
	return errToken("command.unknown.argument", 2)
}

func (s *Server) runScoreboardPlayers(player string, args []string) reply {
	// args[0] == "players"; token indexes count the command root.
	sess := s.findSessionByName(player)
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args))
	}
	switch args[1] {
	case "list":
		if len(args) == 2 {
			// Every tracked holder: live player names, fake names and
			// entity UUID strings — vanilla iterates playerScores'
			// HashMap keySet, ascending spread-hash bucket order.
			s.board.mu.Lock()
			holders := append([]string(nil), s.board.holderOrder...)
			s.board.mu.Unlock()
			javaHashOrder(holders, func(s string) string { return s })
			if len(holders) == 0 {
				return componentReply(translatable("commands.scoreboard.players.list.empty"))
			}
			items := make([]component, 0, len(holders))
			for _, h := range holders {
				items = append(items, component{emptyKey: h})
			}
			return componentReply(translatable("commands.scoreboard.players.list.success",
				nestedCompound{key: "", value: len(holders)},
				joinScoreboardList(items)))
		}
		if len(args) > 3 {
			return errToken("command.unknown.argument", 4)
		}
		h, rep := s.resolveHolderSingle(sess, args[2], 3)
		if rep != nil {
			return *rep
		}
		s.board.mu.Lock()
		scores := s.board.scores[h.name]
		order := append([]string(nil), s.board.scoreOrder[h.name]...)
		s.board.mu.Unlock()
		if len(scores) == 0 {
			return componentReply(translatable("commands.scoreboard.players.list.entity.empty", h.arg()))
		}
		head := translatable("commands.scoreboard.players.list.entity.success",
			h.arg(), nestedCompound{key: "", value: len(scores)})
		var extra []component
		for _, obj := range order {
			extra = append(extra, translatable("commands.scoreboard.players.list.entity.entry",
				s.objectiveChatComponent(obj), nestedCompound{key: "", value: scores[obj]}))
		}
		return componentReplyWith(head, extra...)
	case "get":
		if len(args) < 4 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		if len(args) > 4 {
			return errToken("command.unknown.argument", 5)
		}
		h, rep := s.resolveHolderSingle(sess, args[2], 3)
		if rep != nil {
			return *rep
		}
		objective := args[3]
		s.board.mu.Lock()
		_, objOK := s.board.objectives[objective]
		value, ok := s.board.scores[h.name][objective]
		s.board.mu.Unlock()
		if !objOK {
			return componentReply(commandError("arguments.objective.notFound", objective))
		}
		if !ok {
			return componentReply(commandError("commands.scoreboard.players.get.null",
				objective, h.arg()))
		}
		r := componentReply(translatable("commands.scoreboard.players.get.success",
			h.arg(),
			nestedCompound{key: "", value: value},
			s.objectiveChatComponent(objective)))
		r.result, r.hasResult = int(value), true
		return r
	case "set", "add", "remove":
		// set/add/remove <targets> <objective> <value>
		if len(args) < 5 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		if len(args) > 5 {
			return errToken("command.unknown.argument", 6)
		}
		// Parse-order: the targets arg parses first (selector syntax
		// only — score_holders is plural), then the value int.
		if len(args[2]) > 0 && args[2][0] == '@' {
			if _, serr := parseSelector(args[2], 3); serr != nil {
				return *serr
			}
		}
		value, perr := sbParseInt(args[4])
		if perr != nil {
			return *perr
		}
		holders, rep := s.resolveHoldersPlural(sess, args[2], 3)
		if rep != nil {
			return *rep
		}
		objective := args[3]
		s.board.mu.Lock()
		_, objOK := s.board.objectives[objective]
		s.board.mu.Unlock()
		if !objOK {
			return componentReply(commandError("arguments.objective.notFound", objective))
		}
		s.board.mu.Lock()
		for _, h := range holders {
			v := int32(value)
			switch args[1] {
			case "add":
				v = s.board.scores[h.name][objective] + v
			case "remove":
				v = s.board.scores[h.name][objective] - v
			}
			s.setScoreLocked(h.name, objective, v)
		}
		s.board.mu.Unlock()
		for _, h := range holders {
			s.broadcastScore(h.name, objective, s.board.scores[h.name][objective])
		}
		objComp := s.objectiveChatComponent(objective)
		if len(holders) == 1 {
			h := holders[0]
			total := s.board.scores[h.name][objective]
			switch args[1] {
			case "set":
				return componentReply(translatable("commands.scoreboard.players.set.success.single",
					objComp, h.arg(), nestedCompound{key: "", value: int32(value)}))
			case "add":
				return componentReply(translatable("commands.scoreboard.players.add.success.single",
					nestedCompound{key: "", value: int32(value)}, objComp, h.arg(),
					nestedCompound{key: "", value: total}))
			default:
				return componentReply(translatable("commands.scoreboard.players.remove.success.single",
					nestedCompound{key: "", value: int32(value)}, objComp, h.arg(),
					nestedCompound{key: "", value: total}))
			}
		}
		n := len(holders)
		switch args[1] {
		case "set":
			return componentReply(translatable("commands.scoreboard.players.set.success.multiple",
				objComp, nestedCompound{key: "", value: n}, nestedCompound{key: "", value: int32(value)}))
		case "add":
			return componentReply(translatable("commands.scoreboard.players.add.success.multiple",
				nestedCompound{key: "", value: int32(value)}, objComp, nestedCompound{key: "", value: n}))
		default:
			return componentReply(translatable("commands.scoreboard.players.remove.success.multiple",
				nestedCompound{key: "", value: int32(value)}, objComp, nestedCompound{key: "", value: n}))
		}
	case "reset":
		// reset <targets> [<objective>]
		if len(args) < 3 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		if len(args) > 4 {
			return errToken("command.unknown.argument", 5)
		}
		if len(args[2]) > 0 && args[2][0] == '@' {
			if _, serr := parseSelector(args[2], 3); serr != nil {
				return *serr
			}
		}
		holders, rep := s.resolveHoldersPlural(sess, args[2], 3)
		if rep != nil {
			return *rep
		}
		objective := ""
		if len(args) == 4 {
			objective = args[3]
			s.board.mu.Lock()
			_, objOK := s.board.objectives[objective]
			s.board.mu.Unlock()
			if !objOK {
				return componentReply(commandError("arguments.objective.notFound", objective))
			}
		}
		s.board.mu.Lock()
		for _, h := range holders {
			if objective == "" {
				for _, obj := range append([]string(nil), s.board.scoreOrder[h.name]...) {
					s.removeScoreLocked(h.name, obj)
				}
			} else {
				s.removeScoreLocked(h.name, objective)
			}
		}
		s.board.mu.Unlock()
		for _, h := range holders {
			if objective == "" {
				s.broadcastScoreReset(h.name, nil)
			} else {
				s.broadcastScoreReset(h.name, &objective)
			}
		}
		if len(holders) == 1 {
			h := holders[0]
			if objective == "" {
				return componentReply(translatable("commands.scoreboard.players.reset.all.single", h.arg()))
			}
			return componentReply(translatable("commands.scoreboard.players.reset.specific.single",
				s.objectiveChatComponent(objective), h.arg()))
		}
		if objective == "" {
			return componentReply(translatable("commands.scoreboard.players.reset.all.multiple",
				len(holders)))
		}
		return componentReply(translatable("commands.scoreboard.players.reset.specific.multiple",
			s.objectiveChatComponent(objective), len(holders)))
	case "operation":
		// operation <targets> <targetObjective> <op> <source> <sourceObjective>
		if len(args) < 7 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		if len(args) > 7 {
			return errToken("command.unknown.argument", 8)
		}
		// Parse-order: the plural target parses (syntax), the
		// single-holder source parses (syntax + single shape).
		if len(args[2]) > 0 && args[2][0] == '@' {
			if _, serr := parseSelector(args[2], 3); serr != nil {
				return *serr
			}
		}
		if len(args[5]) > 0 && args[5][0] == '@' {
			spec, serr := parseSelector(args[5], 6)
			if serr != nil {
				return *serr
			}
			if spec.singleMaxRes() > 1 {
				return errTokenEnd("argument.entity.toomany", 6)
			}
		}
		targetObj, op, sourceObj := args[3], args[4], args[6]
		holders, rep := s.resolveHoldersPlural(sess, args[2], 3)
		if rep != nil {
			return *rep
		}
		s.board.mu.Lock()
		_, tOK := s.board.objectives[targetObj]
		_, sOK := s.board.objectives[sourceObj]
		s.board.mu.Unlock()
		if !tOK {
			return componentReply(commandError("arguments.objective.notFound", targetObj))
		}
		src, rep2 := s.resolveHolderSingle(sess, args[5], 6)
		if rep2 != nil {
			return *rep2
		}
		if !sOK {
			return componentReply(commandError("arguments.objective.notFound", sourceObj))
		}
		switch op {
		case "=", "+=", "-=", "*=", "/=", "%=", "<", ">", "><", "swap":
		default:
			return componentReply(commandError("arguments.operation.invalid"))
		}
		srcVal := s.board.scores[src.name][sourceObj]
		s.board.mu.Lock()
		for _, h := range holders {
			cur := s.board.scores[h.name][targetObj]
			v := cur
			switch op {
			case "=":
				v = srcVal
			case "+=":
				v = cur + srcVal
			case "-=":
				v = cur - srcVal
			case "*=":
				v = cur * srcVal
			case "/=":
				if srcVal != 0 {
					v = cur / srcVal
				}
			case "%=":
				if srcVal != 0 {
					v = cur % srcVal
				}
			case "<":
				if srcVal < cur {
					v = srcVal
				}
			case ">":
				if srcVal > cur {
					v = srcVal
				}
			case "><", "swap":
				// Swap writes both sides: target takes srcVal, source
				// adopts the target's old value.
				v = srcVal
				s.setScoreLocked(src.name, sourceObj, cur)
			}
			s.setScoreLocked(h.name, targetObj, v)
		}
		result := s.board.scores[holders[0].name][targetObj]
		s.board.mu.Unlock()
		for _, h := range holders {
			s.broadcastScore(h.name, targetObj, s.board.scores[h.name][targetObj])
		}
		if len(holders) == 1 {
			return componentReply(translatable("commands.scoreboard.players.operation.success.single",
				s.objectiveChatComponent(targetObj), holders[0].arg(),
				nestedCompound{key: "", value: result}))
		}
		return componentReply(translatable("commands.scoreboard.players.operation.success.multiple",
			s.objectiveChatComponent(targetObj), nestedCompound{key: "", value: len(holders)}))
	}
	return errToken("command.unknown.argument", 2)
}

// sbParseInt parses a vanilla int argument: non-numeric input is
// parsing.int.expected, digits that overflow an int32 are
// parsing.int.invalid — both at the token's start.
func sbParseInt(tok string) (int32, *reply) {
	if tok == "" {
		r := errToken("parsing.int.expected", 5)
		return 0, &r
	}
	neg := tok[0] == '-'
	digits := tok
	if neg {
		digits = tok[1:]
	}
	allDigits := len(digits) > 0
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			allDigits = false
			break
		}
	}
	if !allDigits {
		r := errToken("parsing.int.expected", 5)
		return 0, &r
	}
	v64, err := strconv.ParseInt(tok, 10, 64)
	if err != nil || v64 > 2147483647 || v64 < -2147483648 {
		r := errToken("parsing.int.invalid", 5, tok)
		return 0, &r
	}
	return int32(v64), nil
}

// broadcastObjectiveCreate sends Update Objectives mode 0.
func (s *Server) broadcastObjectiveCreate(name string, display any) {
	var b protocol.Buffer
	b.String(name)
	b.Byte(0) // create
	b.Bool(true)
	if display == nil {
		b.Raw(textComponentContent(name))
	} else {
		b.Raw(componentAnyContent(display))
	}
	b.Bool(true)
	b.VarInt(0) // integer
	b.Bool(false)
	s.broadcastToPlay(IDPlayObjectiveCB, b.Bytes())
}

// componentAnyContent serializes a parsed component tree (or a plain
// string) to the packet's NBT content bytes.
func componentAnyContent(v any) []byte {
	var buf bytes.Buffer
	if err := nbt.WriteUnnamed(&buf, v); err != nil {
		return textComponentContent("")
	}
	return buf.Bytes()
}

// broadcastObjectiveRemove sends Update Objectives mode 1.
func (s *Server) broadcastObjectiveRemove(name string) {
	var b protocol.Buffer
	b.String(name)
	b.Byte(1) // remove
	s.broadcastToPlay(IDPlayObjectiveCB, b.Bytes())
}

// broadcastDisplayObjective sends Display Objective.
func (s *Server) broadcastDisplayObjective(position int32, name string) {
	var b protocol.Buffer
	b.VarInt(position)
	b.String(name)
	s.broadcastToPlay(IDPlayDisplayObjective, b.Bytes())
}

// broadcastScore sends Update Score.
func (s *Server) broadcastScore(entity, objective string, value int32) {
	var b protocol.Buffer
	b.String(entity)
	b.String(objective)
	b.VarInt(value)
	b.Bool(false) // no display name override
	b.Bool(false) // no number format
	s.broadcastToPlay(IDPlayUpdateScore, b.Bytes())
}

// broadcastScoreReset sends Reset Score.
func (s *Server) broadcastScoreReset(entity string, objective *string) {
	var b protocol.Buffer
	b.String(entity)
	if objective == nil {
		b.Bool(false)
	} else {
		b.Bool(true)
		b.String(*objective)
	}
	s.broadcastToPlay(IDPlayResetScore, b.Bytes())
}

// broadcastToPlay sends a body to every play-stage session.
func (s *Server) broadcastToPlay(packetID int32, body []byte) {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		_ = sess.write(packetID, body)
		return true
	})
}
