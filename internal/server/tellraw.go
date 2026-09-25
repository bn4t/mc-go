package server

// /tellraw and /title — the component-argument commands. Vanilla's
// component argument is SNBT (a bare word is a string, a list is
// first-element-plus-extra), sent on the wire as NBT verbatim, and a
// trailing token after a complete component is unknown.argument. Both
// commands take a players-only entity selector and reply .single or
// .multiple variants; /tellraw itself sends no feedback at all.

import (
	"bytes"
	"math"
	"strconv"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

func (s *Server) registerTextCommands() {
	s.registerComponentCommand("tellraw", func(player string, args []string) reply {
		return s.runTellraw(player, args)
	})
	s.registerComponentCommand("title", func(player string, args []string) reply {
		return s.runTitle(player, args)
	})
}

// playerTargets resolves the first arg against the players-only
// selector contract both commands share: a bare name resolves like
// vanilla's player argument, and an @e-family selector is rejected at
// parse time with argument.player.entities since it could match
// non-players.
func (s *Server) playerTargets(player string, args []string, tokIdx int) ([]*Session, *reply) {
	if len(args) < 1 {
		r := errToken("command.unknown.argument", tokIdx)
		return nil, &r
	}
	if args[0][0] != '@' {
		sess := s.findSessionByName(args[0])
		if sess == nil {
			r := componentReply(commandError("argument.entity.notfound.player"))
			return nil, &r
		}
		return []*Session{sess}, nil
	}
	if len(args[0]) > 1 && args[0][1] == 'e' {
		r := errToken("argument.player.entities", 0)
		return nil, &r
	}
	cands, rep := s.selectDataEntities(s.findSessionByName(player), args[0], tokIdx, false)
	if rep != nil {
		return nil, rep
	}
	if len(cands) == 0 {
		r := componentReply(commandError("argument.entity.notfound.player"))
		return nil, &r
	}
	out := make([]*Session, 0, len(cands))
	for _, c := range cands {
		if c.sess == nil {
			r := componentReply(commandError("argument.entity.onlyplayers.allowed", c.typeName()))
			return nil, &r
		}
		out = append(out, c.sess)
	}
	return out, nil
}

// errAbsCmd marks a parse failure at an absolute byte offset in the
// input — needed for errors inside a greedy component argument, which
// can span several whitespace-separated tokens.
func errAbsCmd(key string, pos int, with ...any) reply {
	return reply{
		comp:   commandError(key, with...),
		styled: true,
		fail:   &failMark{abs: pos, hasAbs: true},
	}
}

// parseComponentArg parses the joined remainder of the args as SNBT and
// reports the byte offset inside it where parsing stopped — so a
// trailing word lands the unknown.argument cursor on the next token.
func parseComponentArg(joined string) (any, int, *snbtError) {
	tag, next, err := parseSNBTTag(joined, 0)
	if err != nil {
		return nil, next, err
	}
	return compWire(tag), next, nil
}

// resolveComp evaluates the lazily-resolved component kinds —
// score={name,objective} reads the scoreboard, selector= resolves the
// entity to its name component — inside every map and list value of a
// wire component tree, like vanilla's Component resolution on send.
func (s *Server) resolveComp(v any, sender *Session) any {
	switch t := v.(type) {
	case map[string]any:
		if sc, ok := t["score"].(map[string]any); ok {
			name, _ := sc["name"].(string)
			obj, _ := sc["objective"].(string)
			if scv, ok := s.board.scores[name][obj]; ok {
				return strconv.Itoa(int(scv.value))
			}
			return ""
		}
		if sel, ok := t["selector"].(string); ok {
			return s.selectorComponent(sender, sel)
		}
		out := map[string]any{}
		for k, e := range t {
			out[k] = s.resolveComp(e, sender)
		}
		return out
	case nbt.List:
		l := nbt.List{Elem: t.Elem}
		for _, it := range t.Items {
			l.Items = append(l.Items, s.resolveComp(it, sender))
		}
		return l
	}
	return v
}

// selectorComponent resolves a selector string inside a component to
// the entity-name wire form vanilla sends — the decorated name for a
// match, an empty string for none, comma-siblings for many.
func (s *Server) selectorComponent(sender *Session, sel string) any {
	cands, rep := s.selectDataEntities(sender, sel, 0, false)
	if rep != nil || len(cands) == 0 {
		return ""
	}
	one := func(d *dataEntity) map[string]any {
		if d.sess != nil {
			return playerChatName(d.sess).nested()
		}
		return entityDisplayName(d.mob).nested()
	}
	if len(cands) == 1 {
		return one(cands[0])
	}
	list := make([]any, 0, len(cands))
	for _, d := range cands {
		list = append(list, one(d))
	}
	return map[string]any{
		"text":  "",
		"extra": nbt.List{Elem: nbt.TagCompound, Items: list},
	}
}

// compWire converts the parsed SNBT component to its wire value:
// compounds pass through verbatim, strings stay strings, and a list
// becomes its first element with the rest appended to extra.
func compWire(t *snbtTag) any {
	v := snbtToValue(t)
	l, ok := v.(nbt.List)
	if !ok {
		return v
	}
	if len(l.Items) == 0 {
		return map[string]any{}
	}
	base := compValue(l.Items[0])
	rest := make([]any, 0, len(l.Items)-1)
	for _, it := range l.Items[1:] {
		rest = append(rest, compValue(it))
	}
	m, isMap := base.(map[string]any)
	if !isMap {
		m = map[string]any{"text": base}
	}
	if len(rest) == 0 {
		return m
	}
	ex, _ := m["extra"].(nbt.List)
	ex.Items = append(ex.Items, rest...)
	ex.Elem = nbt.TagCompound
	if ex.Elem == 0 || len(ex.Items) == 0 {
		// fresh extra list — element type from the first item
		if len(ex.Items) > 0 {
			ex.Elem = nbtTagOfValue(ex.Items[0])
		}
	}
	m["extra"] = ex
	return m
}

// compValue normalizes a list element that is itself a component.
func compValue(v any) any {
	if l, ok := v.(nbt.List); ok && len(l.Items) > 0 {
		// Nested list form: same first-element-plus-extra rule.
		m := map[string]any{"text": compValue(l.Items[0])}
		if len(l.Items) > 1 {
			rest := make([]any, 0, len(l.Items)-1)
			for _, it := range l.Items[1:] {
				rest = append(rest, compValue(it))
			}
			m["extra"] = nbt.List{Elem: nbtTagOfValue(rest[0]), Items: rest}
		}
		return m
	}
	return v
}

// nbtTagOfValue reports the NBT tag id for a Go value, matching the
// codec's mapping.
func nbtTagOfValue(v any) byte {
	switch v.(type) {
	case map[string]any:
		return nbt.TagCompound
	case string:
		return nbt.TagString
	case int8:
		return nbt.TagByte
	case int16:
		return nbt.TagShort
	case int32, int:
		return nbt.TagInt
	case int64:
		return nbt.TagLong
	case float32:
		return nbt.TagFloat
	case float64:
		return nbt.TagDouble
	case nbt.List:
		return nbt.TagList
	}
	return nbt.TagString
}

// compNormalize collapses a component map carrying only a text key to
// the bare string — the wire codec's canonical form — recursively
// through extra and with.
func compNormalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 1 {
			if txt, ok := t["text"].(string); ok {
				return txt
			}
		}
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = compNormalize(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = compNormalize(e)
		}
		return out
	case nbt.List:
		out := nbt.List{Elem: t.Elem}
		for _, it := range t.Items {
			out.Items = append(out.Items, compNormalize(it))
		}
		return out
	}
	return v
}

// componentBytes encodes a wire component value: strings use the
// TAG_String compact root, everything else is an unnamed compound.
func componentBytes(v any) []byte {
	v = compNormalize(v)
	if str, ok := v.(string); ok {
		return textComponentContent(str)
	}
	var buf bytes.Buffer
	if err := nbt.WriteUnnamed(&buf, v); err != nil {
		return textComponentContent("")
	}
	return buf.Bytes()
}

// componentTokAfter converts a byte offset in the joined component
// string to the number of tokens the component consumed — the token
// index of a trailing word is the component token plus this count.
func componentTokAfter(args []string, joined string, off int) int {
	prefix := strings.TrimRight(joined[:off], " ")
	return len(strings.Fields(prefix))
}

// compAbsOff is the absolute input offset of a byte position inside
// the joined component string: the component starts at args[compArg]'s
// token, so its offsets shift by that token's start, and the join's
// spaces collapse to one separator per gap.
func compAbsOff(nameLen int, args []string, compArg, pos int) int {
	base := nameLen
	for i := 0; i < compArg; i++ {
		base += len(args[i]) + 1
	}
	return base + pos
}

// runTellraw implements `tellraw <targets> <component>`: the component
// goes to each player as a non-overlay System Chat packet and no reply
// comes back — vanilla emits none.
func (s *Server) runTellraw(player string, args []string) reply {
	targets, rep := s.playerTargets(player, args, 1)
	if rep != nil {
		return *rep
	}
	if len(args) < 2 {
		return errTokenEnd("command.unknown.argument", len(args))
	}
	joined := strings.Join(args[1:], " ")
	v, end, perr := parseComponentArg(joined)
	if perr != nil {
		return errAbsCmd(perr.key, compAbsOff(len("tellraw "), args, 1, perr.pos), perr.with...)
	}
	if extra := strings.TrimSpace(joined[end:]); extra != "" {
		return errToken("command.unknown.argument", 2+componentTokAfter(args, joined, end))
	}
	body := componentBytes(s.resolveComp(v, s.findSessionByName(player)))
	var b protocol.Buffer
	b.Raw(body)
	b.Bool(false) // overlay
	pkt := b.Bytes()
	for _, sess := range targets {
		_ = sess.write(sess.ids().PlaySystemChatCB, pkt)
	}
	return reply{}
}

// runTitle implements `title <targets> clear|reset|times|title|
// subtitle|actionbar`.
func (s *Server) runTitle(player string, args []string) reply {
	targets, rep := s.playerTargets(player, args, 1)
	if rep != nil {
		return *rep
	}
	if len(args) < 2 {
		return errTokenEnd("command.unknown.argument", len(args))
	}
	single := len(targets) == 1
	suffix := ".single"
	if !single {
		suffix = ".multiple"
	}
	multi := func() any {
		if single {
			return playerChatName(targets[0])
		}
		return int32(len(targets))
	}
	switch args[1] {
	case "clear", "reset":
		reset := args[1] == "reset"
		var b protocol.Buffer
		b.Bool(reset)
		pkt := b.Bytes()
		for _, sess := range targets {
			_ = sess.write(sess.ids().PlayClearTitles, pkt)
		}
		if reset {
			return opsEchoReply(translatable("commands.title.reset"+suffix, multi()))
		}
		return opsEchoReply(translatable("commands.title.cleared"+suffix, multi()))
	case "times":
		if len(args) < 5 {
			return errToken("command.unknown.argument", len(args))
		}
		var ticks [3]int32
		for i, a := range args[2:5] {
			t, bad, ok := parseTimeArg(a)
			if !ok {
				return errToken("parsing.float.expected", 3+i)
			}
			if bad < 0 {
				return errTokenEnd("argument.time.tick_count_too_low", 3+i, int32(0), bad)
			}
			ticks[i] = t
		}
		if len(args) > 5 {
			return errToken("command.unknown.argument", 6)
		}
		var b protocol.Buffer
		b.Int(ticks[0])
		b.Int(ticks[1])
		b.Int(ticks[2])
		pkt := b.Bytes()
		for _, sess := range targets {
			_ = sess.write(sess.ids().PlaySetTitlesAnimation, pkt)
		}
		return opsEchoReply(translatable("commands.title.times"+suffix, multi()))
	case "title", "subtitle", "actionbar":
		if len(args) < 3 {
			return errTokenEnd("command.unknown.argument", len(args))
		}
		joined := strings.Join(args[2:], " ")
		v, end, perr := parseComponentArg(joined)
		if perr != nil {
			return errAbsCmd(perr.key, compAbsOff(len("title "), args, 2, perr.pos), perr.with...)
		}
		if extra := strings.TrimSpace(joined[end:]); extra != "" {
			return errToken("command.unknown.argument", 3+componentTokAfter(args, joined, end))
		}
		var key string
		pkt := componentBytes(s.resolveComp(v, s.findSessionByName(player)))
		for _, sess := range targets {
			var pid int32
			switch args[1] {
			case "title":
				pid = sess.ids().PlaySetTitleText
				key = "commands.title.show.title"
			case "subtitle":
				pid = sess.ids().PlaySetSubtitleText
				key = "commands.title.show.subtitle"
			case "actionbar":
				pid = sess.ids().PlayActionBarText
				key = "commands.title.show.actionbar"
			}
			_ = sess.write(pid, pkt)
		}
		return opsEchoReply(translatable(key+suffix, multi()))
	default:
		return errToken("command.unknown.argument", 2)
	}
}

// parseTimeArg reads a title-times argument: a float count in ticks
// unless suffixed s|t|d — seconds ×20, ticks, days ×24000 — rounded to
// the nearest int32. A result under vanilla's zero floor comes back in
// bad for the tick_count_too_low error.
func parseTimeArg(a string) (int32, int32, bool) {
	f := 1.0
	if len(a) > 0 {
		switch a[len(a)-1] {
		case 't':
			a = a[:len(a)-1]
		case 's':
			f = 20
			a = a[:len(a)-1]
		case 'd':
			f = 24000
			a = a[:len(a)-1]
		}
	}
	v, err := strconv.ParseFloat(a, 64)
	if err != nil {
		return 0, 0, false
	}
	t := int32(math.Round(v * f))
	return t, t, true
}
