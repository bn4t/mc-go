package server

// The /advancement command, measured against vanilla 26.2
// (net.minecraft.server.commands.AdvancementCommands +
// net.minecraft.server.PlayerAdvancements):
//
//   advancement (grant|revoke) <targets> everything
//   advancement (grant|revoke) <targets> only <adv> [<criterion>]
//   advancement (grant|revoke) <targets> from <adv>     — adv + descendants
//   advancement (grant|revoke) <targets> until <adv>    — adv + ancestors
//   advancement (grant|revoke) <targets> through <adv>  — ancestors + descendants
//
// Replies are the commands.advancement.<action>.<kind>.to.<one|many>
// translatables (or their .failure twins when nothing changed) sent to
// the issuer and relayed to the other operators as chat.type.admin.
// Granting announces each completed advancement in chat exactly like a
// gameplay grant; revoking pushes the lost-visibility set into the
// packet's removed list.

import (
	"strconv"
	"strings"
)

// registerAdvancementCommand installs /advancement.
func (s *Server) registerAdvancementCommand() {
	s.registerComponentCommand("advancement", func(player string, args []string) reply {
		return s.runAdvancement(player, args)
	})
}

// resolveTargets maps a selector or name to sessions: @s/@p resolve to
// the issuer, @r to a player, @a/@e to every player, a name to that
// player.
func (s *Server) resolveTargets(player, tok string) []*Session {
	self := s.findSessionByName(player)
	switch tok {
	case "@s", "@p":
		if self == nil {
			return nil
		}
		return []*Session{self}
	case "@a", "@e":
		var out []*Session
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() == StatePlay {
				out = append(out, sess)
			}
			return true
		})
		return out
	case "@r":
		var out []*Session
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() == StatePlay {
				out = append(out, sess)
				return false
			}
			return true
		})
		return out
	}
	if sess := s.findSessionByName(tok); sess != nil {
		return []*Session{sess}
	}
	return nil
}

// advDescendants lists the advancement plus every descendant in the
// tree (vanilla's FROM mode).
func advDescendants(id string) []*advMeta {
	var out []*advMeta
	var walk func(string)
	walk = func(cur string) {
		if m := gameAdvs[cur]; m != nil {
			out = append(out, m)
		}
		for _, ch := range gameAdvChild[cur] {
			walk(ch)
		}
	}
	walk(id)
	return out
}

// advAncestors lists the advancement plus its parent chain to the root
// (vanilla's UNTIL mode).
func advAncestors(id string) []*advMeta {
	var out []*advMeta
	for cur := id; cur != ""; {
		m := gameAdvs[cur]
		if m == nil {
			break
		}
		out = append(out, m)
		cur = m.parent
	}
	return out
}

// grantAdvancement obtains every criterion of the advancement — the
// command path's "grant all criteria", which is what the FROM/UNTIL/
// THROUGH/EVERYTHING and bare-ONLY modes do. Returns the count of
// criteria that changed.
func (s *Server) grantAdvancement(sess *Session, meta *advMeta) int {
	n := 0
	for _, c := range meta.criteria {
		if t2 := sess.grantCriterion(meta, c, false); t2 {
			n++
		}
	}
	return n
}

// revokeAdvancement clears every criterion of the advancement, dropping
// it out of done. Returns the count of criteria that were obtained.
func (s *Server) revokeAdvancement(sess *Session, meta *advMeta) int {
	n := 0
	for _, c := range meta.criteria {
		if s.revokeGameCriterion(sess, meta, c) {
			n++
		}
	}
	return n
}

// revokeGameCriterion clears one obtained criterion; a completed
// advancement drops out of done. Returns whether the criterion was
// held.
func (s *Server) revokeGameCriterion(sess *Session, meta *advMeta, crit string) bool {
	sess.advMu.Lock()
	defer sess.advMu.Unlock()
	crits := sess.advCrits[meta.id]
	if _, has := crits[crit]; !has {
		return false
	}
	delete(crits, crit)
	// done is recomputed, not cleared: AdvancementProgress.isDone asks
	// each requirements group for one held criterion, so revoking one
	// name from an OR-group (e.g. kill_a_mob's 41-mob union) leaves the
	// advancement done — vanilla stays visible and never re-announces.
	if sess.advDone != nil {
		still := len(meta.reqs) > 0
		for _, g := range meta.reqs {
			met := false
			for _, c := range g {
				if _, ok := crits[c]; ok {
					met = true
					break
				}
			}
			if !met {
				still = false
				break
			}
		}
		if still {
			sess.advDone[meta.id] = true
		} else {
			delete(sess.advDone, meta.id)
		}
	}
	if sess.advDirty == nil {
		sess.advDirty = map[string]bool{}
	}
	if !sess.advDirty[meta.id] {
		sess.advDirty[meta.id] = true
		sess.advDirtyOrder = append(sess.advDirtyOrder, meta.id)
	}
	return true
}

// normalizeAdvID accepts "adventure/kill_a_mob" or the namespaced form.
func normalizeAdvID(id string) string {
	if !strings.Contains(id, ":") {
		return "minecraft:" + id
	}
	return id
}

// runAdvancement implements /advancement grant|revoke, operator-only.
func (s *Server) runAdvancement(player string, args []string) reply {
	sess := s.findSessionByName(player)
	if sess == nil || sess.opLevel() < 2 {
		// Hidden command, exactly as an unknown one reads.
		return reply{comp: unknownCommand(), styled: true, fail: &failMark{}}
	}
	if len(args) < 3 {
		return errTokenEnd("command.unknown.command", 1)
	}
	action := args[0]
	if action != "grant" && action != "revoke" {
		return errToken("command.unknown.argument", 1, literalStr(args[0]))
	}
	targets := s.resolveTargets(player, args[1])
	if len(targets) == 0 {
		return componentReply(commandError("argument.entity.notfound.player"))
	}
	mode := args[2]

	var set []*advMeta // advs to touch
	var onlyMeta *advMeta
	var onlyCrit string
	hasCriterion := false
	switch mode {
	case "everything":
		for _, m := range gameAdvs {
			set = append(set, m)
		}
	case "only", "from", "through", "until":
		if len(args) < 4 {
			return errTokenEnd("command.unknown.command", 3)
		}
		meta := gameAdvs[normalizeAdvID(args[3])]
		if meta == nil {
			return componentReply(commandError("advancement.advancementNotFound", literalStr(args[3])))
		}
		onlyMeta = meta
		switch mode {
		case "only":
			set = []*advMeta{meta}
			if len(args) > 4 {
				hasCriterion = true
				onlyCrit = args[4]
				found := false
				for _, c := range meta.criteria {
					if c == onlyCrit {
						found = true
					}
				}
				if !found {
					return componentReply(commandError("commands.advancement.criterionNotFound",
						advChatComponent(meta), literalStr(onlyCrit)))
				}
			}
		case "from":
			set = advDescendants(meta.id)
		case "until":
			set = advAncestors(meta.id)
		case "through":
			seen := map[string]bool{}
			for _, m := range append(advAncestors(meta.id), advDescendants(meta.id)...) {
				if !seen[m.id] {
					seen[m.id] = true
					set = append(set, m)
				}
			}
		}
	default:
		return errToken("command.unknown.argument", 3, literalStr(mode))
	}
	if len(args) > 4 && mode != "only" || len(args) > 5 || (mode == "everything" && len(args) > 3) {
		return errToken("command.unknown.argument", 4)
	}

	// vanilla's reply-key shape: .one (bare only), .criterion
	// (only+crit), .many (the set modes), x .to.one / .to.many.
	kind := "many"
	if mode == "only" {
		kind = "one"
		if hasCriterion {
			kind = "criterion"
		}
	}
	toMany := len(targets) > 1
	to := "one"
	if toMany {
		to = "many"
	}
	key := "commands.advancement." + action + "." + kind + ".to." + to

	// Apply to every target; a target counts when anything changed.
	targetsChanged := 0
	advsChangedPerTarget := map[*Session]int{}
	for _, t := range targets {
		n := 0
		for _, meta := range set {
			if hasCriterion {
				var changed bool
				if action == "grant" {
					changed = t.grantCriterion(meta, onlyCrit, false)
				} else {
					changed = s.revokeGameCriterion(t, meta, onlyCrit)
				}
				if changed {
					n++
				}
				continue
			}
			if action == "grant" {
				n += s.grantAdvancement(t, meta)
			} else {
				n += s.revokeAdvancement(t, meta)
			}
		}
		// per-target "changed" counts advancements, not criteria —
		// recompute as advs that moved.
		advsChangedPerTarget[t] = n
		if n > 0 {
			targetsChanged++
		}
	}
	ok := targetsChanged > 0
	if ok {
		key += ".success"
	} else {
		key += ".failure"
	}

	var msg component
	switch {
	case kind == "criterion":
		if toMany {
			msg = translatable(key, literalStr(onlyCrit), advChatComponent(onlyMeta), strconv.Itoa(targetsChanged))
		} else {
			msg = translatable(key, literalStr(onlyCrit), advChatComponent(onlyMeta), playerChatComponent(targets[0]))
		}
	case kind == "one":
		if toMany {
			msg = translatable(key, advChatComponent(onlyMeta), strconv.Itoa(targetsChanged))
		} else {
			msg = translatable(key, advChatComponent(onlyMeta), playerChatComponent(targets[0]))
		}
	default: // many
		// Arg 1 is the advancement count — for success it's the number
		// granted on the (single) target, for failure the attempted
		// count, matching vanilla's "Couldn't grant %s advancements".
		count := len(set)
		if ok && !toMany {
			count = advsChangedPerTarget[targets[0]]
		}
		if toMany {
			msg = translatable(key, strconv.Itoa(count), strconv.Itoa(targetsChanged))
		} else {
			msg = translatable(key, strconv.Itoa(count), playerChatComponent(targets[0]))
		}
	}
	if !ok {
		return componentReply(component{color: "red", extra: []component{msg}, text: ""})
	}
	s.broadcastAdmin(sess, msg)
	return componentReply(msg)
}

// literalStr wraps a command argument that vanilla passes as a plain
// string into the translatable's with list.
func literalStr(s string) string { return s }
