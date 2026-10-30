package server

import (
	"math"
	"testing"
)

func TestLocalCoordinatesSourceRotation(t *testing.T) {
	for _, tc := range []struct {
		rot  [2]float32
		want [3]float64
	}{{[2]float32{0, 0}, [3]float64{11.5, -56, 23.5}}, {[2]float32{90, 0}, [3]float64{7.5, -56, 21.5}}, {[2]float32{0, 90}, [3]float64{11.5, -61, 22.5}}} {
		x, y, z := tpResolveSourceCoords([]string{"^1", "^2", "^3"}, 10.5, -58, 20.5, tc.rot)
		for i, v := range []float64{x, y, z} {
			if math.Abs(v-tc.want[i]) > 1e-6 {
				t.Fatalf("rotation%v axis%d=%g want%g", tc.rot, i, v, tc.want[i])
			}
		}
	}
}
func TestExecuteSourceRotationSnapshot(t *testing.T) {
	parent := &commandSourceFrame{rotation: [2]float32{5, 6}}
	ctx := &executeContext{rotation: [2]float32{90, 30}}
	child := executeSourceFrame(parent, ctx)
	ctx.rotation = [2]float32{0, 0}
	if child.rotation != [2]float32{90, 30} || parent.rotation != [2]float32{5, 6} {
		t.Fatalf("source snapshot child%v parent%v", child.rotation, parent.rotation)
	}
}
