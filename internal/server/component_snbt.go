package server

import (
	"mc-go/internal/nbt"
	"strings"
)

// componentSNBT is an internal, lossless bridge from modeled component NBT to
// its parser input. A bare string is safe only if parsing it yields that same
// string; numbers, booleans and namespace identifiers require quotes. Public
// /data presentation retains its existing formatting independently.
func componentSNBT(value any) string {
	var out strings.Builder
	var quoted func(string)
	quoted = func(text string) {
		out.WriteByte('"')
		for i := 0; i < len(text); i++ {
			if text[i] == '"' || text[i] == '\\' {
				out.WriteByte('\\')
			}
			out.WriteByte(text[i])
		}
		out.WriteByte('"')
	}
	var write func(any)
	write = func(value any) {
		switch v := value.(type) {
		case string:
			parsed, end, err := parseSNBTTag(v, 0)
			if err == nil && end == len(v) && parsed.kind == snbtString && parsed.str == v {
				out.WriteString(v)
			} else {
				quoted(v)
			}
		case map[string]any:
			out.WriteByte('{')
			for i, key := range nbt.OrderedKeys(v) {
				if i > 0 {
					out.WriteByte(',')
				}
				if strings.Contains(key, ":") {
					quoted(key)
				} else {
					writeSNBTString(&out, key)
				}
				out.WriteByte(':')
				write(v[key])
			}
			out.WriteByte('}')
		case nbt.List:
			out.WriteByte('[')
			for i, child := range v.Items {
				if i > 0 {
					out.WriteByte(',')
				}
				write(child)
			}
			out.WriteByte(']')
		default:
			writeSNBTCompact(&out, value)
		}
	}
	write(value)
	return out.String()
}
