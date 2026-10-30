package server

import "testing"

func TestTeleportSourceRotationComposition(t *testing.T) {
	cases := []struct {
		source, current [2]float32
		tokens          [2]string
		want            [2]float32
	}{
		{[2]float32{35, 20}, [2]float32{40, 50}, [2]string{"~7", "~-10"}, [2]float32{42, 10}},
		{[2]float32{90, 30}, [2]float32{40, 50}, [2]string{"~5", "~-10"}, [2]float32{95, 20}},
		{[2]float32{180, 0}, [2]float32{}, [2]string{"~", "~"}, [2]float32{-180, 0}},
		{[2]float32{}, [2]float32{-180, 0}, [2]string{"~", "~"}, [2]float32{-360, 0}},
		{[2]float32{90, 0}, [2]float32{-90, 0}, [2]string{"~", "~"}, [2]float32{-270, 0}},
		{[2]float32{-170, 0}, [2]float32{170, 0}, [2]string{"~", "~"}, [2]float32{190, 0}},
		{[2]float32{170, 0}, [2]float32{-170, 0}, [2]string{"~", "~"}, [2]float32{-190, 0}},
		{[2]float32{90, 30}, [2]float32{-90, 50}, [2]string{"~5", "-15"}, [2]float32{-265, -15}},
		{[2]float32{90, 30}, [2]float32{40, 50}, [2]string{"-45", "~5"}, [2]float32{-45, 35}},
		{[2]float32{}, [2]float32{}, [2]string{"0", "100"}, [2]float32{0, 90}},
		{[2]float32{}, [2]float32{}, [2]string{"0", "-100"}, [2]float32{0, -90}},
		{[2]float32{0, 80}, [2]float32{0, -80}, [2]string{"~", "~30"}, [2]float32{0, -90}},
		{[2]float32{}, [2]float32{}, [2]string{"0", "270"}, [2]float32{0, -90}},
	}
	for i, tc := range cases {
		if got := teleportSourceRotation(tc.source, tc.current, tc.tokens[:]); got != tc.want {
			t.Fatalf("case%d got%v want%v", i, got, tc.want)
		}
	}
}
