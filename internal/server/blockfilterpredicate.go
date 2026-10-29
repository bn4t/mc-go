package server

import (
	"mc-go/internal/world"
	"strings"
)

// Fill replace and clone filtered use the same block predicate argument as
// execute: native tag types and member-specific tag properties are retained.
func parseBlockFilterPredicate(token string, tokenIndex int) (func(*Server, *dimension, world.Pos) bool, *reply) {
	if strings.HasPrefix(token, "#") {
		predicate, err := parseBlockTagPredicate(token, tokenIndex)
		if err != nil {
			return nil, err
		}
		return func(s *Server, d *dimension, p world.Pos) bool { return predicate.matches(s, d, p) }, nil
	}
	block, props, pattern, err := parseBlockPredicateNBT(token, tokenIndex)
	if err != nil {
		return nil, err
	}
	return func(s *Server, d *dimension, p world.Pos) bool {
		return s.matchesBlockPredicate(d, p, block, props) && (pattern == nil || s.blockPredicateNBTMatches(d, p, pattern))
	}, nil
}
