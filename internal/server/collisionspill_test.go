package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestPositionedCollisionSpillScans(t *testing.T) {
	for _, tc := range []struct {
		facing  string
		pos     world.Pos
		x, y, z float64
	}{
		{"east", world.Pos{X: 1, Y: 160}, .8, 160.45, .5},
		{"south", world.Pos{Y: 160, Z: 1}, .5, 160.45, .8},
		{"up", world.Pos{Y: 161}, .5, 160.8, .5},
	} {
		t.Run(tc.facing, func(t *testing.T) {
			s := New("unused")
			sess := placeSession(t, s, "Spill", tc.x, tc.y, tc.z)
			head := mkBlockState(t, "piston_head", map[string]string{"facing": tc.facing, "short": "false"})
			sess.world().SetBlock(tc.pos, head)
			const half, height = .01875, .1125
			if !playerBoxCollidesWithDims(sess.world(), 8, 160, 8, tc.x, tc.y, tc.z, half, height) {
				t.Fatal("packet scan omitted next-cell rod")
			}
			box := aabb{tc.x - half, tc.y, tc.z - half, tc.x + half, tc.y + height, tc.z + half}
			if !playerPoseBlockCollision(sess, box) {
				t.Fatal("pose scan omitted next-cell rod")
			}
			if len(worldShapes(sess.dimension(), box)) == 0 {
				t.Fatal("world scan omitted next-cell rod")
			}
		})
	}
}
