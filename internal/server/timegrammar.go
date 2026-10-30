package server

// Numeric time arguments read a decimal number followed by an unquoted unit.
// Punctuation outside the unit alphabet belongs to the enclosing command.
func timeArgumentTokenEnd(raw string) int {
	end := 0
	for end < len(raw) && (raw[end] >= '0' && raw[end] <= '9' || raw[end] == '.' || raw[end] == '-') {
		end++
	}
	for end < len(raw) {
		c := raw[end]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-' || c == '.' || c == '+') {
			break
		}
		end++
	}
	return end
}
