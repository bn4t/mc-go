package server

import (
	"strings"

	"mc-go/internal/world"
)

// Block predicates retain the case and numeric tag types of their optional
// SNBT compound; only the preceding block-state spelling is normalized.
func parseBlockPredicateNBT(token string, tokenIndex int) (world.Block, map[string]string, map[string]any, *reply) {
	spec := token
	var pattern map[string]any
	if start := strings.IndexByte(token, '{'); start >= 0 {
		tag, next, err := parseSNBTCompound(token, start)
		if err != nil {
			r := errTokenOff(err.key, tokenIndex, err.pos, err.with...)
			return world.Air, nil, nil, &r
		}
		if next != len(token) {
			r := errTokenOff("command.unknown.argument", tokenIndex, next)
			return world.Air, nil, nil, &r
		}
		pattern = snbtToValue(tag).(map[string]any)
		spec = token[:start]
	}
	block, props, ok := parseBlockSpec(spec)
	if !ok {
		r := errTokenEnd("argument.block.id.invalid", tokenIndex)
		return world.Air, nil, nil, &r
	}
	return block, props, pattern, nil
}

func (s *Server) blockPredicateNBTMatches(d *dimension, pos world.Pos, pattern map[string]any) bool {
	actual, ok := s.blockEntitySaveMap(d, pos)
	if !ok {
		return false
	} // Even {} requires a block entity.
	// Predicates use saveWithFullMetadata, not the chunk-storage wrapper.
	delete(actual, "keepPacked")
	return nbtSubset(actual, pattern)
}
