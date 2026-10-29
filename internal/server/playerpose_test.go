package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestPlayerPoseCollisionShapes(t *testing.T) {
	for _, tc := range []struct {
		name                string
		props               map[string]string
		standing, crouching bool
	}{
		{"stone", nil, false, false},
		{"stone_slab", map[string]string{"type": "top"}, false, true},
		{"stone_slab", map[string]string{"type": "bottom"}, false, false},
		{"oak_trapdoor", map[string]string{"half": "top", "open": "false"}, true, true},
		{"oak_trapdoor", map[string]string{"half": "bottom", "open": "false"}, false, false},
	} {
		t.Run(tc.name+tc.props["type"]+tc.props["half"], func(t *testing.T) {
			s := New("unused")
			sess := placeSession(t, s, "Pose", 0.5, 160, 0.5)
			sess.dimension().w.SetBlock(world.Pos{X: 0, Y: 161, Z: 0}, mkBlockState(t, tc.name, tc.props))
			for _, p := range []struct {
				pose int32
				want bool
			}{{0, tc.standing}, {5, tc.crouching}, {3, true}} {
				if got := s.playerCanFit(sess, p.pose); got != p.want {
					t.Errorf("pose%d fit=%v want%v", p.pose, got, p.want)
				}
			}
		})
	}
}
