package server

import "math"

// AttributeCommand getters multiply in double precision, then use Java's
// narrowing int conversion: truncate toward zero, saturate, NaN becomes zero.
func attributeScaledResult(value, scale float64) int {
	value *= scale
	switch {
	case math.IsNaN(value):
		return 0
	case value >= math.MaxInt32:
		return math.MaxInt32
	case value <= math.MinInt32:
		return math.MinInt32
	default:
		return int(value)
	}
}

func attributeQueryReply(message component, value, scale float64) reply {
	out := componentReply(message)
	out.result, out.hasResult = attributeScaledResult(value, scale), true
	return out
}

// Executed attribute mutators return one; runtime exceptions complete with
// zero. Parser failures retain their existing no-result marker. Getters set
// their scaled numeric result explicitly, including successful zero.
func finishAttributeReply(out reply) reply {
	if out.fail != nil {
		return out
	}
	switch out.outcome {
	case commandSucceeded:
		if !out.hasResult {
			out.result, out.hasResult = 1, true
		}
	case commandFailed:
		out.result, out.hasResult = 0, true
	}
	return out
}
