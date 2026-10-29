package server

import (
	"mc-go/internal/data"
	"mc-go/internal/world"
	"strings"
)

type blockTagPredicate struct {
	tag     string
	props   map[string]string
	pattern map[string]any
}

func blockPredicateResourceChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("_./-:", rune(c))
}

// Vague tag properties are strings: unknown names/values parse successfully
// and simply fail on a member that lacks that exact property/value.
func parseBlockTagPredicate(token string, tokenIndex int) (*blockTagPredicate, *reply) {
	fail := func(key string, pos int, args ...any) (*blockTagPredicate, *reply) {
		r := errTokenOff(key, tokenIndex, pos, args...)
		return nil, &r
	}
	cursor := 1
	for cursor < len(token) && blockPredicateResourceChar(token[cursor]) {
		cursor++
	}
	id := structureName(token[1:cursor])
	if cursor == 1 {
		id = "minecraft:"
	}
	if id == "" {
		return fail("argument.id.invalid", 1)
	}
	tag := strings.TrimPrefix(id, "minecraft:")
	if _, known := data.BlockTags[tag]; !strings.HasPrefix(id, "minecraft:") || !known {
		return fail("arguments.block.tag.unknown", 1, id)
	}
	out := &blockTagPredicate{tag: tag, props: map[string]string{}}
	if cursor < len(token) && token[cursor] == '[' {
		cursor = snbtSkipWhitespace(token, cursor+1)
		lastValue := -1
		for cursor < len(token) && token[cursor] != ']' {
			cursor = snbtSkipWhitespace(token, cursor)
			propertyStart := cursor
			name, next, err := readBlockPropertyString(token, cursor)
			if err != nil {
				return fail(err.key, err.pos, err.with...)
			}
			cursor = snbtSkipWhitespace(token, next)
			if _, duplicate := out.props[name]; duplicate {
				return fail("argument.block.property.duplicate", propertyStart, name, id)
			}
			if cursor >= len(token) || token[cursor] != '=' {
				return fail("argument.block.property.novalue", propertyStart, name, id)
			}
			cursor = snbtSkipWhitespace(token, cursor+1)
			lastValue = cursor
			value, next, err := readBlockPropertyString(token, cursor)
			if err != nil {
				return fail(err.key, err.pos, err.with...)
			}
			out.props[name] = value
			cursor = snbtSkipWhitespace(token, next)
			if cursor < len(token) {
				lastValue = -1
			}
			if cursor < len(token) && token[cursor] == ',' {
				cursor++
				continue
			}
			if cursor < len(token) && token[cursor] != ']' {
				return fail("argument.block.property.unclosed", cursor)
			}
			break
		}
		if cursor >= len(token) {
			if lastValue >= 0 {
				cursor = lastValue
			}
			return fail("argument.block.property.unclosed", cursor)
		}
		cursor++
	}
	if cursor < len(token) && token[cursor] == '{' {
		value, next, err := parseSNBTCompound(token, cursor)
		if err != nil {
			return fail(err.key, err.pos, err.with...)
		}
		out.pattern = snbtToValue(value).(map[string]any)
		cursor = next
	}
	if cursor != len(token) {
		return fail("command.unknown.argument", cursor)
	}
	return out, nil
}

// Block-state strings use Brigadier quoting, whose escapes are only the
// current delimiter and backslash, rather than SNBT's richer escape syntax.
func readBlockPropertyString(text string, start int) (string, int, *snbtError) {
	if start >= len(text) || text[start] != '"' && text[start] != '\'' {
		end := start
		for end < len(text) && isBareChar(text[end]) {
			end++
		}
		return text[start:end], end, nil
	}
	delimiter := text[start]
	var value strings.Builder
	for cursor := start + 1; cursor < len(text); cursor++ {
		c := text[cursor]
		if c == delimiter {
			return value.String(), cursor + 1, nil
		}
		if c == '\\' {
			cursor++
			if cursor >= len(text) {
				break
			}
			c = text[cursor]
			if c != delimiter && c != '\\' {
				return "", cursor, &snbtError{key: "parsing.quote.escape", with: []any{string(c)}, pos: cursor}
			}
		}
		value.WriteByte(c)
	}
	return "", len(text), &snbtError{key: "parsing.quote.expected.end", pos: len(text)}
}

func (p *blockTagPredicate) matches(s *Server, d *dimension, pos world.Pos) bool {
	if d == nil {
		return false
	}
	state := d.w.GetBlock(pos)
	name, ok := world.StateOwner(state)
	if !ok || !blockTagHas(p.tag, strings.TrimPrefix(name, "minecraft:")) {
		return false
	}
	for key, want := range p.props {
		if got, ok := world.StateProperty(state, key); !ok || got != want {
			return false
		}
	}
	return p.pattern == nil || s.blockPredicateNBTMatches(d, pos, p.pattern)
}
