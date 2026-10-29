package server

import (
	"math"
	"testing"
)

func TestAttributeScaledQueryResult(t *testing.T) {
	cases := []struct {
		name         string
		value, scale float64
		want         int
	}{
		{"default truncation", 2.5, 1, 2},
		{"scaled", 2.5, 2, 5},
		{"negative truncation", -.75, 2, -1},
		{"negative scale", 2.5, -.5, -1},
		{"zero value", 0, 2, 0},
		{"zero scale", 2.5, 0, 0},
		{"upper saturation", 2.5, 1e20, math.MaxInt32},
		{"lower saturation", 2.5, -1e20, math.MinInt32},
		{"positive infinity", math.Inf(1), 1, math.MaxInt32},
		{"negative infinity", math.Inf(-1), 1, math.MinInt32},
		{"nan", math.NaN(), 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := attributeScaledResult(tc.value, tc.scale); got != tc.want {
				t.Fatalf("result %d, want %d", got, tc.want)
			}
		})
	}
	out := attributeQueryReply(translatable("commands.attribute.value.get.success"), 0, 1)
	if !out.hasResult || out.result != 0 || out.outcome != commandSucceeded {
		t.Fatal("successful zero lost its execution status", out)
	}
}

func TestAttributeTypedCompletion(t *testing.T) {
	mutation := finishAttributeReply(opsEchoReply(translatable("commands.attribute.base_value.set.success")))
	if !mutation.hasResult || mutation.result != 1 || mutation.outcome != commandSucceeded || !mutation.opsEcho {
		t.Fatal("mutation result", mutation)
	}
	zero := finishAttributeReply(attributeQueryReply(translatable("commands.attribute.value.get.success"), 0, 1))
	if !zero.hasResult || zero.result != 0 || zero.outcome != commandSucceeded {
		t.Fatal("successful zero", zero)
	}
	failed := finishAttributeReply(componentReply(commandError("commands.attribute.failed.no_modifier")))
	if !failed.hasResult || failed.result != 0 || failed.outcome != commandFailed {
		t.Fatal("runtime failure", failed)
	}
	parsed := finishAttributeReply(errToken("parsing.double.expected", 4))
	if parsed.hasResult || parsed.fail == nil {
		t.Fatal("parse failure acquired a result", parsed)
	}
	unknown := finishAttributeReply(reply{})
	if unknown.hasResult {
		t.Fatal("unknown outcome acquired a result")
	}
}
