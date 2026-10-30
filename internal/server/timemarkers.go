package server

// All builtin markers belong to the periodic Overworld day timeline.
// Hidden markers are valid command arguments although absent from suggestions.
func builtinTimeMarker(name string) (int64, bool) {
	switch name {
	case "minecraft:day":
		return 1000, true
	case "minecraft:noon":
		return 6000, true
	case "minecraft:night":
		return 13000, true
	case "minecraft:midnight", "minecraft:roll_village_siege":
		return 18000, true
	case "minecraft:wake_up_from_sleep":
		return 0, true
	}
	return 0, false
}

func nextBuiltinTimeMarker(total, ticks int64) int64 {
	until := ticks - total%24000
	if until <= 0 {
		until += 24000
	}
	return total + until
}

func timeMarkerPrefix(raw string) int {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == ':' || c == '/' || c == '.' || c == '-') {
			return i
		}
	}
	return len(raw)
}
