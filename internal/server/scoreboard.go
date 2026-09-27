package server

import (
	"bytes"
	"math"
	"strconv"
	"strings"
	"sync"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// scoreboard holds objectives (name -> def) and per-holder scores,
// plus the sidebar display slot and the team table.
type scoreboard struct {
	mu         sync.Mutex
	objectives map[string]*sbObjective // name -> objective
	scores     map[string]map[string]*sbScore
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
// square-bracket replies (nil means the bare name), renderType,
// numberFormat and autoUpdate carry the wire fields
// objectives modify sets.
type sbObjective struct {
	criteria     string
	display      any
	renderType   int // 0 integer, 1 hearts
	numberFormat *sbNumberFormat
	autoUpdate   bool
}

// sbScore is one holder's score in one objective: the int value plus
// the optional display-name and number-format overrides
// players display sets, and the locked flag trigger criteria scores
// carry until players enable releases them.
type sbScore struct {
	value   int32
	locked  bool // trigger-criteria scores start locked
	display any
	format  *sbNumberFormat
}

// sbNumberFormat is a number-format override the way the wire codec
// frames it: a type id (blank=0, styled=1, fixed=2) and the payload —
// the SNBT style map for styled, the component tree for fixed.
type sbNumberFormat struct {
	kind  int
	style any
	fixed any
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

// flatWireArg applies vanilla's flat component serialization inside a
// with list: a parsed component that is a bare {"text": x} literal
// serializes as the string x, everything else stays a compound.
func flatWireArg(v any) any {
	if m, ok := v.(map[string]any); ok && len(m) == 1 {
		if s, ok := m["text"].(string); ok {
			return s
		}
	}
	return v
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
		cands, rep := s.selectDataEntities(sender, tok, tokIdx, false, nil)
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
		cands, rep := s.selectDataEntities(sender, tok, tokIdx, false, nil)
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
		s.board.scores = map[string]map[string]*sbScore{}
		s.board.scoreOrder = map[string][]string{}
	}
	if s.board.scores[holder] == nil {
		s.board.scores[holder] = map[string]*sbScore{}
		s.board.holderOrder = append(s.board.holderOrder, holder)
	}
	e := s.board.scores[holder][objective]
	if e == nil {
		e = &sbScore{}
		// Trigger-criteria scores start locked until
		// scoreboard players enable releases them.
		if o := s.board.objectives[objective]; o != nil && o.criteria == "trigger" {
			e.locked = true
		}
		s.board.scores[holder][objective] = e
		s.board.scoreOrder[holder] = append(s.board.scoreOrder[holder], objective)
	}
	e.value = value
}

// scoreValue reads a score entry's int, treating absent as 0 the way
// vanilla's getScore does for an untracked objective.
func scoreValue(e *sbScore) int32 {
	if e == nil {
		return 0
	}
	return e.value
}

// bumpCriteria adds delta to every objective with the named criteria
// for the holder and broadcasts each write — forAllObjectives with an
// increment consumer, which creates the score on objectives where the
// holder was untracked.
func (s *Server) bumpCriteria(holder, criteria string, delta int32) {
	type objWrite struct {
		obj string
		val int32
	}
	var writes []objWrite
	s.board.mu.Lock()
	for name, o := range s.board.objectives {
		if o.criteria != criteria {
			continue
		}
		v := scoreValue(s.board.scores[holder][name]) + delta
		s.setScoreLocked(holder, name, v)
		writes = append(writes, objWrite{name, v})
	}
	s.board.mu.Unlock()
	for _, w := range writes {
		s.broadcastScore(holder, w.obj, w.val)
	}
}

// teamColorOf is the criteria color name of the holder's team — the
// teamkill/killedByTeam objective suffix — empty when the holder has
// no team or the team has no color.
func (s *Server) teamColorOf(name string) string {
	s.board.mu.Lock()
	defer s.board.mu.Unlock()
	for _, t := range s.board.teams {
		if t.members[name] && t.color >= 0 && int(t.color) < len(vanillaTeamColors) {
			return vanillaTeamColors[t.color]
		}
	}
	return ""
}

// killCreditPlayer resolves LivingEntity.getKillCredit for a dead
// player: the last player to hurt them inside the 100-tick window, or
// nil when nobody did (mob kill, unattributed source, expired window,
// killer logged off).
func (s *Server) killCreditPlayer(sess *Session) *Session {
	if sess.lastHurtPlayerTicks <= 0 {
		return nil
	}
	return s.sessionByUUID(sess.lastHurtPlayerBy)
}

// awardKillScore runs ServerPlayer.awardKillScore on the credited
// player: totalKillCount, the kill stat (whose own stat criteria
// feed runs inside addStat), playerKillCount for a player victim,
// then teamkill.<victim's team color> on the killer and
// killedByTeam.<killer's team color> on the victim's holder.
// victim==killer earns nothing. Mob killers run Entity's empty
// override, so they never reach here.
func (s *Server) awardKillScore(killer *Session, victimHolder string, victimPlayer bool) {
	if victimHolder == killer.getName() {
		return
	}
	// Vanilla order: KILL_COUNT_ALL bumps first, then the kill stat
	// (which feeds its own stat criteria), KILL_COUNT_PLAYERS for a
	// player victim, then the two team maps.
	s.bumpCriteria(killer.getName(), "totalKillCount", 1)
	if victimPlayer {
		killer.addCustomStat("player_kills", 1)
		s.bumpCriteria(killer.getName(), "playerKillCount", 1)
	} else {
		killer.addCustomStat("mob_kills", 1)
	}
	if vc := s.teamColorOf(victimHolder); vc != "" {
		s.bumpCriteria(killer.getName(), "teamkill."+vc, 1)
	}
	if kc := s.teamColorOf(killer.getName()); kc != "" {
		s.bumpCriteria(victimHolder, "killedByTeam."+kc, 1)
	}
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
		o := &sbObjective{criteria: criteria, display: display}
		s.board.objectives[name] = o
		s.board.mu.Unlock()
		if criteriaReadOnly(criteria) {
			// Live criteria baseline at the objective's creation:
			// each online player snapshots its current value without
			// writing a score, so a later event writes only real
			// change (measured: fresh health/level objectives stay
			// unset until the value moves).
			s.board.mu.Lock()
			s.sessions.Range(func(_, v any) bool {
				other := v.(*Session)
				if lv, ok := s.liveCriteriaValue(other, criteria); ok {
					if other.criteriaLast == nil {
						other.criteriaLast = map[string]int32{}
					}
					other.criteriaLast[criteria] = lv
				}
				return true
			})
			s.board.mu.Unlock()
		}
		s.broadcastObjectiveCreate(name, o)
		return opsEchoReply(translatable("commands.scoreboard.objectives.add.success",
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
		for slot, n := range s.board.displaySlots {
			if n == name {
				s.board.displaySlots[slot] = ""
			}
		}
		s.board.mu.Unlock()
		s.broadcastObjectiveRemove(name)
		return opsEchoReply(translatable("commands.scoreboard.objectives.remove.success",
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
			return opsEchoReply(translatable(
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
		return opsEchoReply(translatable(
			"commands.scoreboard.objectives.display.set",
			args[2], s.objectiveDisplay(name)))
	case "modify":
		return s.runScoreboardModify(args)
	}
	return errToken("command.unknown.argument", 2)
}

// runScoreboardModify implements objectives modify <objective> <op>:
// displayname <component>, displayautoupdate <bool>, numberformat
// [blank|fixed <component>|styled <style>] and rendertype
// <integer|hearts>.
func (s *Server) runScoreboardModify(args []string) reply {
	if len(args) < 4 {
		return errTokenEnd("command.unknown.command", len(args))
	}
	name := args[2]
	s.board.mu.Lock()
	o, objOK := s.board.objectives[name]
	s.board.mu.Unlock()
	if !objOK {
		return componentReply(commandError("arguments.objective.notFound", name))
	}
	switch args[3] {
	case "displayname":
		if len(args) < 5 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		joined := strings.Join(args[4:], " ")
		v, end, perr := parseComponentArg(joined)
		if perr != nil {
			return errAbsCmd(perr.key, compAbsOff(len("scoreboard "), args, 4, perr.pos), perr.with...)
		}
		if extra := strings.TrimSpace(joined[end:]); extra != "" {
			return errToken("command.unknown.argument", 5+componentTokAfter(args, joined, end))
		}
		s.board.mu.Lock()
		o.display = v
		s.board.mu.Unlock()
		s.broadcastObjectiveChange(name, o)
		return opsEchoReply(translatable(
			"commands.scoreboard.objectives.modify.displayname",
			name, s.objectiveChatComponent(name)))
	case "displayautoupdate":
		if len(args) < 5 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		if len(args) > 5 {
			return errToken("command.unknown.argument", 6)
		}
		var flag bool
		switch args[4] {
		case "true":
			flag = true
		case "false":
		default:
			return errToken("parsing.bool.invalid", 5, args[4])
		}
		s.board.mu.Lock()
		o.autoUpdate = flag
		s.board.mu.Unlock()
		key := "commands.scoreboard.objectives.modify.displayAutoUpdate.disable"
		if flag {
			key = "commands.scoreboard.objectives.modify.displayAutoUpdate.enable"
		}
		return opsEchoReply(translatable(key, name, s.objectiveChatComponent(name)))
	case "numberformat":
		format, rep := parseNumberFormatArgs(args, 4)
		if rep != nil {
			return *rep
		}
		s.board.mu.Lock()
		o.numberFormat = format
		s.board.mu.Unlock()
		s.broadcastObjectiveChange(name, o)
		key := "commands.scoreboard.objectives.modify.objectiveFormat.set"
		if format == nil {
			key = "commands.scoreboard.objectives.modify.objectiveFormat.clear"
		}
		return opsEchoReply(translatable(key, name))
	case "rendertype":
		if len(args) < 5 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		if len(args) > 5 {
			return errToken("command.unknown.argument", 6)
		}
		rt := 0
		switch args[4] {
		case "integer":
		case "hearts":
			rt = 1
		default:
			return errToken("command.unknown.argument", 5)
		}
		s.board.mu.Lock()
		o.renderType = rt
		s.board.mu.Unlock()
		s.broadcastObjectiveChange(name, o)
		return opsEchoReply(translatable(
			"commands.scoreboard.objectives.modify.rendertype",
			s.objectiveChatComponent(name)))
	}
	return errToken("command.unknown.argument", 4)
}

// parseNumberFormatArgs parses the shared numberformat tail used by
// objectives modify and players display numberformat: nothing at all
// clears, blank is kind 0, fixed carries a component, styled an SNBT
// style map. start is the args index of the first format token.
func parseNumberFormatArgs(args []string, start int) (*sbNumberFormat, *reply) {
	if len(args) == start {
		return nil, nil // bare numberformat clears the format
	}
	switch args[start] {
	case "blank":
		if len(args) > start+1 {
			r := errToken("command.unknown.argument", start+2)
			return nil, &r
		}
		return &sbNumberFormat{kind: 0}, nil
	case "fixed":
		if len(args) == start+1 {
			r := errTokenEnd("command.unknown.command", start+1)
			return nil, &r
		}
		joined := strings.Join(args[start+1:], " ")
		v, end, perr := parseComponentArg(joined)
		if perr != nil {
			r := errAbsCmd(perr.key, compAbsOff(len("scoreboard "), args, start+1, perr.pos), perr.with...)
			return nil, &r
		}
		if extra := strings.TrimSpace(joined[end:]); extra != "" {
			r := errToken("command.unknown.argument", start+2+componentTokAfter(args, joined, end))
			return nil, &r
		}
		return &sbNumberFormat{kind: 2, fixed: v}, nil
	case "styled":
		if len(args) == start+1 {
			r := errTokenEnd("command.unknown.command", start+1)
			return nil, &r
		}
		joined := strings.Join(args[start+1:], " ")
		tag, end, serr := parseSNBTTag(joined, 0)
		if serr != nil {
			r := errAbsCmd(serr.key, compAbsOff(len("scoreboard "), args, start+1, serr.pos), serr.with...)
			return nil, &r
		}
		if extra := strings.TrimSpace(joined[end:]); extra != "" {
			r := errToken("command.unknown.argument", start+2+componentTokAfter(args, joined, end))
			return nil, &r
		}
		return &sbNumberFormat{kind: 1, style: snbtToValue(tag)}, nil
	}
	r := errToken("command.unknown.argument", start+1)
	return nil, &r
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
				s.objectiveChatComponent(obj), nestedCompound{key: "", value: scoreValue(scores[obj])}))
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
		e, ok := s.board.scores[h.name][objective]
		s.board.mu.Unlock()
		if !objOK {
			return componentReply(commandError("arguments.objective.notFound", objective))
		}
		if !ok {
			return componentReply(commandError("commands.scoreboard.players.get.null",
				objective, h.arg()))
		}
		value := e.value
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
		value, perr := sbParseInt(args[4], 5)
		if perr != nil {
			return *perr
		}
		holders, rep := s.resolveHoldersPlural(sess, args[2], 3)
		if rep != nil {
			return *rep
		}
		objective := args[3]
		s.board.mu.Lock()
		o, objOK := s.board.objectives[objective]
		s.board.mu.Unlock()
		if !objOK {
			return componentReply(commandError("arguments.objective.notFound", objective))
		}
		if criteriaReadOnly(o.criteria) {
			return componentReply(commandError("arguments.objective.readonly", objective))
		}
		s.board.mu.Lock()
		for _, h := range holders {
			v := int32(value)
			switch args[1] {
			case "add":
				v = scoreValue(s.board.scores[h.name][objective]) + v
			case "remove":
				v = scoreValue(s.board.scores[h.name][objective]) - v
			}
			s.setScoreLocked(h.name, objective, v)
		}
		s.board.mu.Unlock()
		for _, h := range holders {
			s.broadcastScore(h.name, objective, scoreValue(s.board.scores[h.name][objective]))
		}
		objComp := s.objectiveChatComponent(objective)
		if len(holders) == 1 {
			h := holders[0]
			total := scoreValue(s.board.scores[h.name][objective])
			switch args[1] {
			case "set":
				return opsEchoReply(translatable("commands.scoreboard.players.set.success.single",
					objComp, h.arg(), nestedCompound{key: "", value: int32(value)}))
			case "add":
				return opsEchoReply(translatable("commands.scoreboard.players.add.success.single",
					nestedCompound{key: "", value: int32(value)}, objComp, h.arg(),
					nestedCompound{key: "", value: total}))
			default:
				return opsEchoReply(translatable("commands.scoreboard.players.remove.success.single",
					nestedCompound{key: "", value: int32(value)}, objComp, h.arg(),
					nestedCompound{key: "", value: total}))
			}
		}
		n := len(holders)
		switch args[1] {
		case "set":
			return opsEchoReply(translatable("commands.scoreboard.players.set.success.multiple",
				objComp, nestedCompound{key: "", value: n}, nestedCompound{key: "", value: int32(value)}))
		case "add":
			return opsEchoReply(translatable("commands.scoreboard.players.add.success.multiple",
				nestedCompound{key: "", value: int32(value)}, objComp, nestedCompound{key: "", value: n}))
		default:
			return opsEchoReply(translatable("commands.scoreboard.players.remove.success.multiple",
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
				return opsEchoReply(translatable("commands.scoreboard.players.reset.all.single", h.arg()))
			}
			return opsEchoReply(translatable("commands.scoreboard.players.reset.specific.single",
				s.objectiveChatComponent(objective), h.arg()))
		}
		if objective == "" {
			return opsEchoReply(translatable("commands.scoreboard.players.reset.all.multiple",
				len(holders)))
		}
		return opsEchoReply(translatable("commands.scoreboard.players.reset.specific.multiple",
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
		tObj, tOK := s.board.objectives[targetObj]
		_, sOK := s.board.objectives[sourceObj]
		s.board.mu.Unlock()
		if !tOK {
			return componentReply(commandError("arguments.objective.notFound", targetObj))
		}
		if criteriaReadOnly(tObj.criteria) {
			return componentReply(commandError("arguments.objective.readonly", targetObj))
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
		srcVal := scoreValue(s.board.scores[src.name][sourceObj])
		s.board.mu.Lock()
		for _, h := range holders {
			cur := scoreValue(s.board.scores[h.name][targetObj])
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
		result := scoreValue(s.board.scores[holders[0].name][targetObj])
		s.board.mu.Unlock()
		for _, h := range holders {
			s.broadcastScore(h.name, targetObj, scoreValue(s.board.scores[h.name][targetObj]))
		}
		if len(holders) == 1 {
			return opsEchoReply(translatable("commands.scoreboard.players.operation.success.single",
				s.objectiveChatComponent(targetObj), holders[0].arg(),
				nestedCompound{key: "", value: result}))
		}
		return opsEchoReply(translatable("commands.scoreboard.players.operation.success.multiple",
			s.objectiveChatComponent(targetObj), nestedCompound{key: "", value: len(holders)}))
	case "enable":
		// enable <targets> <objective> — releases the locked flag on
		// trigger-criteria scores; the objective must be a trigger.
		if len(args) < 4 {
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
		objective := args[3]
		s.board.mu.Lock()
		o, objOK := s.board.objectives[objective]
		s.board.mu.Unlock()
		if !objOK {
			return componentReply(commandError("arguments.objective.notFound", objective))
		}
		if o.criteria != "trigger" {
			return componentReply(commandError(
				"commands.scoreboard.players.enable.invalid"))
		}
		var enabled []sbHolder
		s.board.mu.Lock()
		for _, h := range holders {
			s.setScoreLocked(h.name, objective, scoreValue(s.board.scores[h.name][objective]))
			e := s.board.scores[h.name][objective]
			if e.locked {
				e.locked = false
				enabled = append(enabled, h)
			}
		}
		s.board.mu.Unlock()
		for _, h := range holders {
			s.broadcastScore(h.name, objective, scoreValue(s.board.scores[h.name][objective]))
		}
		if len(enabled) == 0 {
			return componentReply(commandError("commands.scoreboard.players.enable.failed"))
		}
		if len(enabled) == 1 {
			return opsEchoReply(translatable("commands.scoreboard.players.enable.success.single",
				s.objectiveChatComponent(objective), enabled[0].arg()))
		}
		return opsEchoReply(translatable("commands.scoreboard.players.enable.success.multiple",
			s.objectiveChatComponent(objective), len(enabled)))
	case "display":
		// display name|numberformat <targets> <objective> [<value>] —
		// per-score overrides shipped on every Update Score.
		if len(args) < 3 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		sub := args[2]
		if sub != "name" && sub != "numberformat" {
			return errToken("command.unknown.argument", 3)
		}
		if len(args) < 5 {
			return errTokenEnd("command.unknown.command", len(args))
		}
		targetsTok, objective := args[3], args[4]
		if len(targetsTok) > 0 && targetsTok[0] == '@' {
			if _, serr := parseSelector(targetsTok, 4); serr != nil {
				return *serr
			}
		}
		holders, rep := s.resolveHoldersPlural(sess, targetsTok, 4)
		if rep != nil {
			return *rep
		}
		s.board.mu.Lock()
		_, objOK := s.board.objectives[objective]
		s.board.mu.Unlock()
		if !objOK {
			return componentReply(commandError("arguments.objective.notFound", objective))
		}
		if sub == "name" {
			var display any
			if len(args) > 5 {
				joined := strings.Join(args[5:], " ")
				v, end, perr := parseComponentArg(joined)
				if perr != nil {
					return errAbsCmd(perr.key, compAbsOff(len("scoreboard "), args, 5, perr.pos), perr.with...)
				}
				if extra := strings.TrimSpace(joined[end:]); extra != "" {
					return errToken("command.unknown.argument", 6+componentTokAfter(args, joined, end))
				}
				display = v
			}
			s.board.mu.Lock()
			for _, h := range holders {
				s.setScoreLocked(h.name, objective, scoreValue(s.board.scores[h.name][objective]))
				s.board.scores[h.name][objective].display = display
			}
			s.board.mu.Unlock()
			for _, h := range holders {
				s.broadcastScore(h.name, objective, scoreValue(s.board.scores[h.name][objective]))
			}
			objComp := s.objectiveChatComponent(objective)
			if len(holders) == 1 {
				if display == nil {
					return opsEchoReply(translatable(
						"commands.scoreboard.players.display.name.clear.success.single",
						holders[0].arg(), objComp))
				}
				return opsEchoReply(translatable(
					"commands.scoreboard.players.display.name.set.success.single",
					nestedCompound{key: "", value: flatWireArg(display)}, holders[0].arg(), objComp))
			}
			if display == nil {
				return opsEchoReply(translatable(
					"commands.scoreboard.players.display.name.clear.success.multiple",
					len(holders), objComp))
			}
			return opsEchoReply(translatable(
				"commands.scoreboard.players.display.name.set.success.multiple",
				nestedCompound{key: "", value: flatWireArg(display)}, len(holders), objComp))
		}
		format, rep := parseNumberFormatArgs(args, 5)
		if rep != nil {
			return *rep
		}
		s.board.mu.Lock()
		for _, h := range holders {
			s.setScoreLocked(h.name, objective, scoreValue(s.board.scores[h.name][objective]))
			s.board.scores[h.name][objective].format = format
		}
		s.board.mu.Unlock()
		for _, h := range holders {
			s.broadcastScore(h.name, objective, scoreValue(s.board.scores[h.name][objective]))
		}
		objComp := s.objectiveChatComponent(objective)
		kind := "set"
		if format == nil {
			kind = "clear"
		}
		if len(holders) == 1 {
			return opsEchoReply(translatable(
				"commands.scoreboard.players.display.numberFormat."+kind+".success.single",
				holders[0].arg(), objComp))
		}
		return opsEchoReply(translatable(
			"commands.scoreboard.players.display.numberFormat."+kind+".success.multiple",
			len(holders), objComp))
	}
	return errToken("command.unknown.argument", 2)
}

// sbParseInt parses a vanilla int argument: non-numeric input is
// parsing.int.expected, digits that overflow an int32 are
// parsing.int.invalid — both at the token's start.
func sbParseInt(tok string, tokIdx int) (int32, *reply) {
	if tok == "" {
		r := errToken("parsing.int.expected", tokIdx)
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
		r := errToken("parsing.int.expected", tokIdx)
		return 0, &r
	}
	v64, err := strconv.ParseInt(tok, 10, 64)
	if err != nil || v64 > 2147483647 || v64 < -2147483648 {
		r := errToken("parsing.int.invalid", tokIdx, tok)
		return 0, &r
	}
	return int32(v64), nil
}

// broadcastObjectiveCreate sends Update Objectives mode 0 (create).
func (s *Server) broadcastObjectiveCreate(name string, o *sbObjective) {
	s.sendObjective(name, 0, o)
}

// broadcastObjectiveChange sends Update Objectives mode 2 (change),
// re-shipping display name, render type and number format.
func (s *Server) broadcastObjectiveChange(name string, o *sbObjective) {
	s.sendObjective(name, 2, o)
}

// sendObjective writes an Update Objectives packet: the name and mode
// byte always go out; add and change modes carry the display name as
// an unconditional component, the render-type enum, and the optional
// number format.
func (s *Server) sendObjective(name string, mode byte, o *sbObjective) {
	var b protocol.Buffer
	b.String(name)
	b.Byte(mode)
	if mode != 1 {
		display := any(name)
		if o != nil && o.display != nil {
			display = o.display
		}
		b.Raw(componentAnyContent(display))
		rt := 0
		var format *sbNumberFormat
		if o != nil {
			rt = o.renderType
			format = o.numberFormat
		}
		b.VarInt(int32(rt))
		writeNumberFormat(&b, format)
	}
	s.broadcastToPlay(IDPlayObjectiveCB, b.Bytes())
}

// writeNumberFormat writes an Optional<NumberFormat>: absent or the
// type id (blank=0, styled=1, fixed=2) plus the payload — the style
// compound for styled, the component for fixed.
func writeNumberFormat(b *protocol.Buffer, f *sbNumberFormat) {
	if f == nil {
		b.Bool(false)
		return
	}
	b.Bool(true)
	b.VarInt(int32(f.kind))
	switch f.kind {
	case 1:
		b.Raw(componentAnyContent(f.style))
	case 2:
		b.Raw(componentAnyContent(f.fixed))
	}
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

// broadcastScore sends Update Score: value plus the per-score
// display-name and number-format overrides players display installs.
func (s *Server) broadcastScore(entity, objective string, value int32) {
	s.board.mu.Lock()
	e := s.board.scores[entity][objective]
	s.board.mu.Unlock()
	var b protocol.Buffer
	b.String(entity)
	b.String(objective)
	b.VarInt(value)
	if e != nil && e.display != nil {
		b.Bool(true)
		b.Raw(componentAnyContent(e.display))
	} else {
		b.Bool(false)
	}
	if e != nil {
		writeNumberFormat(&b, e.format)
	} else {
		b.Bool(false)
	}
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

// --- criteria feeds -------------------------------------------------
// Vanilla scores for tracked criteria are the stat/attribute values
// themselves: a stat award writes the new total into every objective
// whose criterion names it, and the live criteria (health, food, xp,
// level, air, armor) refresh on the tick. Manual players set/add
// writes land normally and are overwritten by the next stat award.

// statCriteriaName builds the criterion name a (category, id) stat
// feeds: minecraft.<type>:minecraft.<value>.
func statCriteriaName(category, id int32) string {
	var vname string
	var ok bool
	switch category {
	case statMined:
		vname, ok = blockIDName(id)
	case statCrafted, statUsed, statBroken, statPickedUp, statDropped:
		vname, ok = world.ItemName(id)
	case statKilled, statKilledBy:
		vname, ok = EntityTypeName(id)
	case statCustom:
		vname, ok = customStatName(id)
	}
	if !ok || category < 0 || int(category) >= len(statTypeNames) {
		return ""
	}
	return "minecraft." + statTypeNames[category] + ":minecraft." + vname
}

// feedStatCriteria is vanilla's awardStat scoreboard hook:
// forAllObjectives(stat, holder, access -> access.add(amount)) —
// every objective whose criterion IS this (type, value) stat gains
// the awarded DELTA, not the stat's new total (a stat objective
// created after awards only counts forward from creation). The
// simple kill/death criteria ride their own call sites instead
// (die's deathCount, awardKillScore's totalKillCount/
// playerKillCount).
func (s *Server) feedStatCriteria(sess *Session, category, id int32, delta int32) {
	criteria := statCriteriaName(category, id)
	if criteria == "" {
		return
	}
	s.bumpCriteria(sess.getName(), criteria, delta)
}

// resetStatCriteria is vanilla's resetStat scoreboard hook:
// forAllObjectives(stat, holder, access -> access.set(0)) — every
// objective tracking the stat drops to zero.
func (s *Server) resetStatCriteria(sess *Session, category, id int32) {
	criteria := statCriteriaName(category, id)
	if criteria == "" {
		return
	}
	name := sess.getName()
	var writes []struct {
		obj string
	}
	s.board.mu.Lock()
	for objName, o := range s.board.objectives {
		if o.criteria != criteria {
			continue
		}
		s.setScoreLocked(name, objName, 0)
		writes = append(writes, struct {
			obj string
		}{objName})
	}
	s.board.mu.Unlock()
	for _, w := range writes {
		s.broadcastScore(name, w.obj, 0)
	}
}

// liveCriteriaValue is the value a live (read-only) criterion reads
// for a player right now: health/xp/level/food/air/armor — armor is
// the armor-defense sum, air the breath meter's current supply.
func (s *Server) liveCriteriaValue(sess *Session, criteria string) (int32, bool) {
	switch criteria {
	case "health":
		return int32(math.Ceil(float64(sess.health))), true
	case "xp":
		return sess.xpTotal, true
	case "level":
		return int32(sess.xpLevel), true
	case "food":
		return int32(sess.food), true
	case "air":
		return sess.airSupply, true
	case "armor":
		return int32(sess.armorDefense()), true
	}
	return 0, false
}

// criteriaReadOnly is vanilla's ReadOnlyCriterion set — the live
// criteria reject /scoreboard players set/add/remove/operation.
func criteriaReadOnly(criteria string) bool {
	switch criteria {
	case "health", "xp", "level", "food", "air", "armor":
		return true
	}
	return false
}

// criteriaFeed refreshes this session's scores on live-criteria
// objectives, restricted to the named criteria and writing only on
// change. Vanilla feeds these from the events that mutate them —
// health/food on SetHealth, xp/level on SetExperience, armor on
// equipment writes — so they are event-driven rather than ticked.
func (s *Server) criteriaFeed(sess *Session, criteria ...string) {
	name := sess.getName()
	type write struct {
		obj string
		val int32
	}
	var writes []write
	s.board.mu.Lock()
	for _, c := range criteria {
		v, ok := s.liveCriteriaValue(sess, c)
		if !ok {
			continue
		}
		last, seen := sess.criteriaLast[c]
		if seen && last == v {
			continue
		}
		if sess.criteriaLast == nil {
			sess.criteriaLast = map[string]int32{}
		}
		sess.criteriaLast[c] = v
		for objName, o := range s.board.objectives {
			if o.criteria != c {
				continue
			}
			cur := s.board.scores[name][objName]
			if cur != nil && cur.value == v {
				continue
			}
			s.setScoreLocked(name, objName, v)
			writes = append(writes, write{objName, v})
		}
	}
	s.board.mu.Unlock()
	for _, w := range writes {
		s.broadcastScore(name, w.obj, w.val)
	}
}
