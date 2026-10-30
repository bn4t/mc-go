package server

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// These public-API truth rows cover every command amplifier plus signed
// shift boundaries; fixture extraction is reproducible in tools/probeeffectcadence.
func TestPeriodicEffectCadencePublicFacts(t *testing.T) {
	data, err := os.ReadFile("testdata/vanilla-26.2/effect_cadence.tsv")
	if err != nil {
		t.Fatal(err)
	}
	periods := map[string]int32{"minecraft:regeneration": 50, "minecraft:poison": 25, "minecraft:wither": 40}
	rows := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || len(fields[2]) != 201 {
			t.Fatal("invalid fact row")
		}
		period, ok := periods[fields[0]]
		if !ok {
			t.Fatal(fields[0])
		}
		amp, err := strconv.ParseInt(fields[1], 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		for duration, want := range fields[2] {
			if got := effectPeriodicTickDue(period, int32(amp), int64(duration)); got != (want == '1') {
				t.Fatalf("%s amp%d duration%d got%v want%v", fields[0], amp, duration, got, want == '1')
			}
		}
		rows++
	}
	if rows != 789 {
		t.Fatalf("fact rows%d want789", rows)
	}
}
