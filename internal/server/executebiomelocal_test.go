package server

import (
	"mc-go/internal/world"
	"strings"
	"testing"
)

func TestExecuteBiomeLocalBlockCoordinates(t *testing.T) {
	s := New("unused")
	for _, row := range []struct {
		name     string
		origin   [3]float64
		rotation [2]float32
		coords   string
		want     world.Pos
	}{
		{"left", [3]float64{-7.5, -57.5, 11.5}, [2]float32{0, 0}, "^16 ^ ^", world.Pos{X: 8, Y: -58, Z: 11}},
		{"yaw90", [3]float64{24.5, -57.5, 11.5}, [2]float32{90, 0}, "^ ^ ^16", world.Pos{X: 8, Y: -58, Z: 11}},
		{"yaw180", [3]float64{8.5, -57.5, 27.5}, [2]float32{180, 0}, "^ ^ ^16", world.Pos{X: 8, Y: -58, Z: 11}},
		{"pitch90", [3]float64{8.5, -41.5, 11.5}, [2]float32{0, 90}, "^ ^ ^16", world.Pos{X: 8, Y: -58, Z: 11}},
		{"pitchNegative", [3]float64{8.5, -61.5, 11.5}, [2]float32{0, -90}, "^ ^ ^4", world.Pos{X: 8, Y: -58, Z: 11}},
		{"fractionalBasis", [3]float64{10.25, -59.2, 4.65}, [2]float32{33, -27}, "^2.25 ^-1.5 ^6.75", world.Pos{X: 8, Y: -58, Z: 11}},
		{"retainFractionBeforeFloor", [3]float64{8.5, -57.5, 11.5}, [2]float32{}, "^0.75 ^0.75 ^0.75", world.Pos{X: 9, Y: -57, Z: 12}},
		{"negativeFloor", [3]float64{-.25, -.25, -.25}, [2]float32{}, "^ ^ ^", world.Pos{X: -1, Y: -1, Z: -1}},
	} {
		t.Run(row.name, func(t *testing.T) {
			ctx := &executeContext{pos: row.origin, rotation: row.rotation}
			args := append([]string{"biome"}, strings.Fields(row.coords)...)
			got, r := s.executeSourceBlockPosition(ctx, args, 0, 0, 0, 0)
			if r != nil || got != row.want {
				t.Fatalf("position%+v reply%+v want%+v", got, r, row.want)
			}
		})
	}
	ctx := &executeContext{pos: [3]float64{8.5, -59.12, 11.5}, anchorEyes: true, ent: &dataEntity{sess: &Session{collisionScale: 1}}}
	got, r := s.executeSourceBlockPosition(ctx, []string{"biome", "^", "^", "^"}, 8, -60, 11, 0)
	if r != nil || got != (world.Pos{X: 8, Y: -58, Z: 11}) {
		t.Fatalf("eye anchor%+v reply%+v", got, r)
	}
	got, r = s.executeSourceBlockPosition(ctx, []string{"biome", "8", "-60", "11"}, 8, -60, 11, 0)
	if r != nil || got != (world.Pos{X: 8, Y: -60, Z: 11}) {
		t.Fatalf("literal anchor%+v reply%+v", got, r)
	}
	for _, coords := range []string{"^ ~ ^", "~ ^ ~", "^no ^ ^", "^NaN ^ ^", "^ ^Inf ^", "^ ^ ^-Inf", "^1e2 ^ ^", "^ ^ ^1.5e-1", "^+1 ^ ^"} {
		_, r := s.executeSourceBlockPosition(ctx, append([]string{"biome"}, strings.Fields(coords)...), 8, -60, 11, 0)
		if r == nil {
			t.Fatalf("invalid local triple accepted%s", coords)
		}
	}
}
