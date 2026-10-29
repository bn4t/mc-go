package server

// Message arguments — /msg's <message>, /say's, /me's — carry embedded
// entity selectors, which vanilla's MessageArgument.resolve expands
// server-side before the chat packet goes out: each selector becomes
// the matched entities' display-name components, comma-joined in gray
// when more than one matched, an empty literal when none did. The
// literal chunks between selectors move into the extra list.

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// messageSelector is one embedded selector — the letter plus the
// bracketed arguments mc-go evaluates against its player roster.
// Non-player entity types resolve to nothing: mc-go has no display
// name components for mobs.
type messageSelector struct {
	letter   byte   // a e p r s
	typeName string // the type= argument; "!name" inverts, "" unset
	limit    int    // 0 until hasLimit
	hasLimit bool
	sortBy   string // "" | "nearest" | "furthest" | "random" | "arbitrary"
	minDist  float64
	maxDist  float64 // -1 = no cap
}

// messageText renders a message argument the way vanilla's
// MessageArgument.resolve does: text before the first selector stays
// the root literal, and resolved selectors plus the chunks between
// them land in extra. A message with no selectors stays a bare
// literal.
func (s *Server) messageText(player, text string) component {
	sender := s.findSessionByName(player)
	var extra []component
	out := component{text: ""}
	// pos is the offset the next literal chunk starts at.
	pos := 0
	found := false
	for i := 0; i < len(text); i++ {
		if text[i] != '@' {
			continue
		}
		spec, end, ok := parseMessageSelector(text, i)
		if !ok {
			continue
		}
		if !found {
			out.text = text[pos:i]
			found = true
		} else if i > pos {
			extra = append(extra, literal(text[pos:i]))
		}
		extra = append(extra, s.selectorResult(sender, spec, text[i:end]))
		pos = end
		i = end - 1
	}
	if !found {
		return literal(text)
	}
	if pos < len(text) {
		extra = append(extra, literal(text[pos:]))
	}
	out.extra = extra
	return out
}

// parseMessageSelector reads the selector that begins at text[i]=='@'
// — @a/@e/@p/@r/@s plus a bracketed argument list — returning the spec
// and the index one past its end. Anything else is not a selector.
func parseMessageSelector(text string, i int) (messageSelector, int, bool) {
	spec := messageSelector{maxDist: -1}
	if i+1 >= len(text) {
		return spec, i, false
	}
	switch text[i+1] {
	case 'a', 'e', 'p', 'r', 's':
		spec.letter = text[i+1]
	default:
		return spec, i, false
	}
	end := i + 2
	if end >= len(text) || text[end] != '[' {
		return spec, end, true
	}
	close := strings.IndexByte(text[end:], ']')
	if close < 0 {
		return spec, i, false
	}
	args := text[end+1 : end+close]
	end += close + 1
	for _, kv := range strings.Split(args, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(kv), "=")
		switch k {
		case "type":
			spec.typeName = v
		case "limit":
			if n, err := strconv.Atoi(v); err == nil {
				spec.limit, spec.hasLimit = n, true
			}
		case "sort":
			spec.sortBy = v
		case "distance":
			spec.minDist, spec.maxDist = parseDistanceRange(v)
		default:
			// An argument mc-go cannot evaluate makes the selector
			// unresolved — vanilla's parser error path.
			return spec, i, false
		}
	}
	return spec, end, true
}

// parseDistanceRange reads the distance= forms: "5", "5..", "..10",
// "2..8".
func parseDistanceRange(v string) (float64, float64) {
	if lo, hi, ok := strings.Cut(v, ".."); ok {
		var loF, hiF float64 = 0, -1
		if lo != "" {
			loF, _ = strconv.ParseFloat(lo, 64)
		}
		if hi != "" {
			hiF, _ = strconv.ParseFloat(hi, 64)
		}
		return loF, hiF
	}
	f, _ := strconv.ParseFloat(v, 64)
	return f, f
}

// selectorResult expands a selector to the components vanilla puts in
// its place: one entity's decorated name, a gray-comma-joined list of
// them, or an empty literal when nothing matched.
func (s *Server) selectorResult(sender *Session, spec messageSelector, tokens ...string) component {
	if sender == nil && s.activeCommandSource != nil && len(tokens) != 0 {
		entities, err := s.selectDataEntities(nil, tokens[0], 0, false, s.activeCommandSource.dim)
		if err != nil || len(entities) == 0 {
			return literal("")
		}
		if len(entities) == 1 {
			return entities[0].nameComp()
		}
		var extra []component
		for i, entity := range entities {
			if i != 0 {
				extra = append(extra, component{color: "gray", text: ", "})
			}
			extra = append(extra, entity.nameComp())
		}
		return component{text: "", extra: extra}
	}
	sessions := s.selectorSessions(sender, spec)
	switch len(sessions) {
	case 0:
		return literal("")
	case 1:
		return playerChatName(sessions[0])
	}
	var extras []component
	for i, sess := range sessions {
		if i > 0 {
			extras = append(extras, component{color: "gray", text: ", "})
		}
		extras = append(extras, playerChatName(sess))
	}
	return component{text: "", extra: extras}
}

// selectorSessions evaluates spec against the play-state sessions.
// mc-go resolves only players: @a stays all players, @e honours
// type=player, and a non-player type matches nothing.
func (s *Server) selectorSessions(sender *Session, spec messageSelector) []*Session {
	switch spec.letter {
	case 's':
		if sender == nil {
			return nil
		}
		return []*Session{sender}
	}
	if spec.letter == 'e' && spec.typeName != "" {
		name := strings.TrimPrefix(spec.typeName, "minecraft:")
		if strings.HasPrefix(name, "!") || name != "player" {
			return nil
		}
	}
	list := s.playSessionsOrdered()
	// @p is the nearest player, @r a random one; both stop at one.
	switch spec.letter {
	case 'p':
		if len(list) == 0 {
			return nil
		}
		sortByDistance(list, sender)
		return list[:1]
	case 'r':
		if len(list) == 0 {
			return nil
		}
		return []*Session{list[s.rng.intn(len(list))]}
	}
	if sender != nil && (spec.minDist != 0 || spec.maxDist >= 0) {
		sx, sy, sz, _, _, _ := sender.Pos()
		var kept []*Session
		for _, sess := range list {
			x, y, z, _, _, _ := sess.Pos()
			d := math.Sqrt((x-sx)*(x-sx) + (y-sy)*(y-sy) + (z-sz)*(z-sz))
			if d >= spec.minDist && (spec.maxDist < 0 || d <= spec.maxDist) {
				kept = append(kept, sess)
			}
		}
		list = kept
	}
	switch spec.sortBy {
	case "nearest":
		sortByDistance(list, sender)
	case "furthest":
		sortByDistance(list, sender)
		for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
			list[i], list[j] = list[j], list[i]
		}
	}
	if spec.hasLimit && spec.limit >= 0 && spec.limit < len(list) {
		list = list[:spec.limit]
	}
	return list
}

// playSessionsOrdered is the player roster in join order — sessions
// keys off a sequence number, so a key sort is join order, matching
// vanilla's player list order in selector results.
func (s *Server) playSessionsOrdered() []*Session {
	var out []*Session
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() == StatePlay {
			out = append(out, sess)
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// sortByDistance orders sessions nearest-first to the sender; a
// missing sender leaves the order alone.
func sortByDistance(list []*Session, sender *Session) {
	if sender == nil {
		return
	}
	sx, sy, sz, _, _, _ := sender.Pos()
	sort.SliceStable(list, func(i, j int) bool {
		xi, yi, zi, _, _, _ := list[i].Pos()
		xj, yj, zj, _, _, _ := list[j].Pos()
		di := (xi-sx)*(xi-sx) + (yi-sy)*(yi-sy) + (zi-sz)*(zi-sz)
		dj := (xj-sx)*(xj-sx) + (yj-sy)*(yj-sy) + (zj-sz)*(zj-sz)
		return di < dj
	})
}
