package server

import (
	"sort"
	"strconv"
	"strings"
)

func (s *Server) suggestTimeArguments(sess *Session, prior []string, word string, start int) (int, int, []string) {
	if sess != nil && sess.opLevel() < commandLevel("time") {
		return 0, 0, nil
	}
	id, hasClock := int32(0), true
	if sess != nil {
		id, hasClock = worldClockID(sess.dimName())
	}
	root := true
	if len(prior) > 0 && prior[0] == "of" {
		if len(prior) == 1 {
			return timeSuggestionRange(start, word, timeResourceSuggestions(word, []string{"minecraft:overworld", "minecraft:the_end"}))
		}
		name, valid := jigsawIdentifier(prior[1])
		var known bool
		id, known = worldClockID(name)
		if !valid || !known {
			return 0, 0, nil
		}
		hasClock = true
		root = false
		prior = prior[2:]
	}
	if len(prior) == 0 {
		actions := []string{"add", "pause", "query", "rate", "resume", "set"}
		if root {
			actions = append(actions, "of")
		}
		return timeSuggestionRange(start, word, timeLiteralSuggestions(word, actions))
	}
	if len(prior) == 1 {
		switch prior[0] {
		case "set", "add":
			n, units := timeUnitSuggestions(word)
			if len(units) > 0 {
				return timeSuggestionRange(start+n, word[n:], units)
			}
			if prior[0] == "set" && hasClock && id == 0 {
				var names []string
				for name := range timeMarkers {
					names = append(names, "minecraft:"+name)
				}
				return timeSuggestionRange(start, word, timeResourceSuggestions(word, names))
			}
		case "query":
			literals := []string{"time"}
			if root {
				literals = append(literals, "gametime")
			}
			matches := timeLiteralSuggestions(word, literals)
			if hasClock && id == 0 {
				matches = append(matches, timeResourceSuggestions(word, []string{"minecraft:day", "minecraft:early_game", "minecraft:moon", "minecraft:villager_schedule"})...)
			}
			return timeSuggestionRange(start, word, matches)
		}
	}
	if len(prior) == 2 && prior[0] == "query" {
		name, valid := jigsawIdentifier(prior[1])
		_, known := builtinTimelinePeriod(name)
		if valid && known {
			return timeSuggestionRange(start, word, timeLiteralSuggestions(word, []string{"repetition"}))
		}
	}
	return 0, 0, nil
}

func timeSuggestionRange(start int, word string, matches []string) (int, int, []string) {
	if len(matches) == 0 {
		return 0, 0, nil
	}
	sort.Strings(matches)
	return start, len(word), matches
}

func timeLiteralSuggestions(word string, candidates []string) []string {
	var matches []string
	for _, candidate := range candidates {
		if candidate != word && strings.HasPrefix(candidate, strings.ToLower(word)) {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func timeResourceSuggestions(word string, candidates []string) []string {
	var matches []string
	prefix := strings.ToLower(word)
	for _, candidate := range candidates {
		if candidate == word {
			continue
		}
		matched := false
		if strings.Contains(prefix, ":") {
			matched = timeResourceWordPrefix(candidate, prefix)
		} else {
			namespace, path, _ := strings.Cut(candidate, ":")
			matched = timeResourceWordPrefix(namespace, prefix) || timeResourceWordPrefix(path, prefix)
		}
		if matched {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func timeResourceWordPrefix(value, prefix string) bool {
	if strings.HasPrefix(value, prefix) {
		return true
	}
	for i, c := range value {
		if strings.ContainsRune("._/", c) && strings.HasPrefix(value[i+1:], prefix) {
			return true
		}
	}
	return false
}

func timeUnitSuggestions(word string) (int, []string) {
	n := 0
	for n < len(word) && (word[n] >= '0' && word[n] <= '9' || word[n] == '.' || word[n] == '-') {
		n++
	}
	if n == 0 {
		return n, nil
	}
	if _, err := strconv.ParseFloat(word[:n], 32); err != nil {
		if num, ok := err.(*strconv.NumError); !ok || num.Err != strconv.ErrRange {
			return n, nil
		}
	}
	return n, timeLiteralSuggestions(word[n:], []string{"d", "s", "t"})
}
