package server

import (
	"math"
	"testing"
)

func TestMaceWaveNativeMotion(t *testing.T) {
	for _, tc := range []struct {
		name           string
		offset, motion [3]float64
		resistance     float64
		heavy          bool
		want           [3]float64
		apply          bool
	}{
		{"default", [3]float64{2, 0, 0}, [3]float64{}, 0, false, [3]float64{1.0499999821186066, .699999988079071, 0}, true},
		{"half", [3]float64{2, 0, 0}, [3]float64{}, .5, false, [3]float64{.5249999910593033, .699999988079071, 0}, true},
		{"upper", [3]float64{2, 0, 0}, [3]float64{.1, .2, .3}, 1, false, [3]float64{.1, .2, .3}, false},
		{"lower", [3]float64{2, 0, 0}, [3]float64{}, -2, false, [3]float64{3.1499999463558197, .699999988079071, 0}, true},
		{"heavy", [3]float64{2, 0, 0}, [3]float64{}, .5, true, [3]float64{1.0499999821186066, .699999988079071, 0}, true},
		{"vertical offset", [3]float64{2, 1, 0}, [3]float64{}, .5, false, [3]float64{.3956733022366206, .699999988079071, 0}, true},
		{"coincident", [3]float64{}, [3]float64{}, .5, false, [3]float64{0, .699999988079071, 0}, true},
		{"radius", [3]float64{3.5, 0, 0}, [3]float64{.1, .2, .3}, .5, false, [3]float64{.1, .2, .3}, false},
		{"existing velocity", [3]float64{2, 0, 0}, [3]float64{.1, .2, .3}, .5, false, [3]float64{.6249999910593033, .899999988079071, .3}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, apply := maceWaveMotion(tc.motion, tc.offset, tc.resistance, tc.heavy)
			if apply != tc.apply {
				t.Fatal("application", apply, tc.apply)
			}
			for i, want := range tc.want {
				if math.IsNaN(got[i]) || math.IsInf(got[i], 0) || math.Abs(got[i]-want) > 1e-12 {
					t.Fatalf("motion=%v want=%v", got, tc.want)
				}
			}
		})
	}
}

func TestMaceWaveDirectionThreshold(t *testing.T) {
	const threshold = 9.999999747378752e-6
	if maceWaveDirectionEpsilon != threshold {
		t.Fatal("promoted threshold", maceWaveDirectionEpsilon)
	}
	for _, tc := range []struct {
		name     string
		distance float64
		directed bool
	}{
		{"below", math.Nextafter(threshold, 0), false},
		{"exact", threshold, true},
		{"above", math.Nextafter(threshold, math.Inf(1)), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			motion, apply := maceWaveMotion([3]float64{}, [3]float64{tc.distance, 0, 0}, .5, false)
			if !apply || (motion[0] > 0) != tc.directed || motion[1] != maceWaveStrength {
				t.Fatal("threshold motion", motion, apply)
			}
		})
	}
}
