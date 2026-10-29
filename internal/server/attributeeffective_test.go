package server

import (
	"math"
	"testing"
)

func TestMeasuredAttributeEffectiveRanges(t *testing.T) {
	for _, tc := range []struct {
		name              string
		low, high, normal float64
	}{{"attack_damage", 0, 2048, 1.5}, {"attack_speed", 0, 1024, 4}, {"scale", .0625, 16, 1}, {"luck", -1024, 1024, -2.5}} {
		t.Run(tc.name, func(t *testing.T) {
			for _, c := range []struct{ value, want float64 }{{tc.normal, tc.normal}, {tc.low, tc.low}, {tc.high, tc.high}, {tc.low - 1, tc.low}, {tc.high + 1, tc.high}, {math.Inf(-1), tc.low}, {math.Inf(1), tc.high}, {math.NaN(), tc.low}} {
				if got := sanitizeAttributeValue(tc.name, c.value); got != c.want {
					t.Fatalf("sanitize %v = %v, want %v", c.value, got, c.want)
				}
			}
		})
	}
	if got := sanitizeAttributeValue("unmeasured", 12345); got != 12345 {
		t.Fatal("unmeasured attribute changed")
	}
}

func TestEffectiveSanitizationPrecedesQueryScaling(t *testing.T) {
	value := sanitizeAttributeValue("scale", 32)
	out := attributeQueryReply(translatable("commands.attribute.value.get.success"), value, 2)
	if out.result != 32 || !out.hasResult || out.outcome != commandSucceeded {
		t.Fatal("query scaled the unsanitized value", out)
	}
	low := attributeQueryReply(translatable("commands.attribute.value.get.success"), sanitizeAttributeValue("scale", math.NaN()), 1)
	if low.result != 0 || low.outcome != commandSucceeded {
		t.Fatal("successful fractional minimum lost", low)
	}
}
