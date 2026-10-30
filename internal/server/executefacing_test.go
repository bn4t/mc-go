package server

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestFacingPublicNumericModel(t *testing.T) {
	file, err := os.Open("../../tools/probefacingmath/public-facing.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	inv, angles, faces := 0, 0, 0
	seen := make(map[string]string)
	scan := bufio.NewScanner(file)
	number := func(text string) float64 {
		v, err := strconv.ParseFloat(text, 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	uint32hex := func(text string) uint32 {
		v, err := strconv.ParseUint(text, 16, 32)
		if err != nil {
			t.Fatal(err)
		}
		return uint32(v)
	}
	for scan.Scan() {
		a := strings.Split(scan.Text(), "\t")
		if len(a) == 0 || strings.HasPrefix(a[0], "#") {
			continue
		}
		// Repeated cardinal inputs are intentional; inconsistent duplicates are not.
		width := map[string]int{"inv": 3, "atan": 4, "face": 6}[a[0]]
		if width == 0 || len(a) != width {
			t.Fatalf("malformed observation %q", scan.Text())
		}
		inputWidth := width - 1
		if a[0] == "face" {
			inputWidth--
		}
		key := strings.Join(a[:inputWidth], "\t")
		value := strings.Join(a[inputWidth:], "\t")
		if prior, ok := seen[key]; ok && prior != value {
			t.Fatalf("inconsistent repeated input %s", key)
		}
		seen[key] = value
		switch a[0] {
		case "inv":
			x, want := number(a[1]), number(a[2])
			if got := commandReciprocalRoot(x); math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("inverse%s=%x want%x", a[1], math.Float64bits(got), math.Float64bits(want))
			}
			inv++
		case "atan":
			y, x, want := number(a[1]), number(a[2]), number(a[3])
			got := commandFacingRadians(y, x)
			if math.Float32bits(float32(got*float64(commandDegreesPerRadian))) != math.Float32bits(float32(want*float64(commandDegreesPerRadian))) {
				t.Fatalf("angle%s/%s=%g want%g", a[1], a[2], got, want)
			}
			angles++
		case "face":
			target := [3]float64{number(a[1]), number(a[2]), number(a[3])}
			got := commandFacingRotation([3]float64{}, target)
			for i := range got {
				if bits := math.Float32bits(got[i]); bits != uint32hex(a[4+i]) {
					t.Fatalf("face%v axis%d=%x want%s", target, i, bits, a[4+i])
				}
			}
			faces++
		default:
			t.Fatalf("unknown observation%s", a[0])
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 10549 {
		t.Fatalf("incomplete unique input domain %d", len(seen))
	}
	if inv != 12 || angles != 10257 || faces != 283 {
		t.Fatalf("incomplete observation domain%d/%d/%d", inv, angles, faces)
	}
}
