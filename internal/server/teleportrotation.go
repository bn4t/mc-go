package server

import (
	"strconv"
	"strings"
)

// teleportSourceRotation resolves command arguments against a frozen source,
// then retains the victim's whole-turn history for relative axes. Pitch is
// bounded after that composition, including when a relative turn crosses180.
func teleportSourceRotation(source, current [2]float32, tokens []string) [2]float32 {
	var result [2]float32
	for i := range result {
		value, _ := strconv.ParseFloat(strings.TrimPrefix(tokens[i], "~"), 32)
		angle := float32(value)
		if strings.HasPrefix(tokens[i], "~") {
			desired := source[i] + angle
			result[i] = current[i] + tpWrapDeg(desired-current[i])
		} else {
			result[i] = tpWrapDeg(angle)
		}
	}
	result[1] = max(float32(-90), min(float32(90), result[1]))
	return result
}
