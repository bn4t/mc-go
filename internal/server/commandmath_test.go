package server

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

func commandMathFixture(t *testing.T) [][]string {
	t.Helper()
	file, err := os.Open("../../tools/probecommandmath/public-math.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var rows [][]string
	scan := bufio.NewScanner(file)
	for scan.Scan() {
		line := scan.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}
func commandMathFloat(t *testing.T, text string) float64 {
	t.Helper()
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func commandMathBits(t *testing.T, text string) uint32 {
	t.Helper()
	value, err := strconv.ParseUint(text, 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	return uint32(value)
}
func TestCommandMathPublicSamples(t *testing.T) {
	sine, trig := 0, 0
	var sineSeen [commandSinePeriod]bool
	constantSeen := map[string]bool{}
	for _, row := range commandMathFixture(t) {
		switch row[0] {
		case "constant":
			if constantSeen[row[1]] {
				t.Fatalf("duplicate constant %s", row[1])
			}
			constantSeen[row[1]] = true
			if row[1] == "DEG_TO_RAD" && math.Float32bits(commandRadiansPerDegree) != math.Float32bits(float32(commandMathFloat(t, row[2]))) {
				t.Fatal("angular unit changed")
			}
		case "sin":
			index, err := strconv.Atoi(row[1])
			if err != nil || index < 0 || index >= commandSinePeriod {
				t.Fatal("invalid sample index")
			}
			if sineSeen[index] {
				t.Fatalf("duplicate sine index%d", index)
			}
			sineSeen[index] = true
			if got := math.Float32bits(commandSineSamples[index]); got != commandMathBits(t, row[2]) {
				t.Fatalf("sine%d=%x want%s", index, got, row[2])
			}
			sine++
		case "trig":
			angle := commandMathFloat(t, row[1])
			if got := math.Float32bits(commandSinRadians(angle)); got != commandMathBits(t, row[2]) {
				t.Fatalf("sin(%s)=%x want%s", row[1], got, row[2])
			}
			if got := math.Float32bits(commandCosRadians(angle)); got != commandMathBits(t, row[3]) {
				t.Fatalf("cos(%s)=%x want%s", row[1], got, row[3])
			}
			trig++
		}
	}
	if !constantSeen["DEG_TO_RAD"] {
		t.Fatal("missing required angular unit")
	}
	for index, seen := range sineSeen {
		if !seen {
			t.Fatalf("missing sine index%d", index)
		}
	}
	if sine != 65536 || trig != 60 {
		t.Fatalf("incomplete census%d/%d", sine, trig)
	}
}
func TestCommandLocalBasisPublicSamples(t *testing.T) {
	count := 0
	seen := map[[2]uint32]uint8{}
	for _, row := range commandMathFixture(t) {
		if row[0] != "basis" {
			continue
		}
		rotation := [2]float32{float32(commandMathFloat(t, row[1])), float32(commandMathFloat(t, row[2]))}
		axis, err := strconv.Atoi(row[3])
		if err != nil || axis < 0 || axis > 2 {
			t.Fatal("invalid basis axis")
		}
		key := [2]uint32{math.Float32bits(rotation[0]), math.Float32bits(rotation[1])}
		bit := uint8(1) << axis
		if seen[key]&bit != 0 {
			t.Fatalf("duplicate rotation%v axis%d", rotation, axis)
		}
		seen[key] |= bit
		tokens := []string{"^0", "^0", "^0"}
		tokens[axis] = "^1"
		x, y, z := tpResolveSourceCoords(tokens, 0, 0, 0, rotation)
		for i, got := range []float64{x, y, z} {
			want := commandMathFloat(t, row[4+i])
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("basis%v axis%d component%d=%x want%x", rotation, axis, i, math.Float64bits(got), math.Float64bits(want))
			}
		}
		count++
	}
	for key, axes := range seen {
		if axes != 7 {
			t.Fatalf("missing rotation-axis key%v mask%b", key, axes)
		}
	}
	if len(seen) != 263 {
		t.Fatalf("incomplete rotation domain%d", len(seen))
	}
	if count != 789 {
		t.Fatalf("incomplete basis census%d", count)
	}
}
