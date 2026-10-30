package server

import (
	"testing"
)

func TestExecuteAnchorUsesCachedPoseScale(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "AnchorEyes")
	s.simulationAction(func() {
		entity := s.entities.Spawn(&Entity{Type: mustEntityType("player")})
		p.entityID.Store(entity.ID)
		if entity == nil {
			t.Fatal("missing player")
		}
		p.collisionScale = 2
		entity.Pose = 5
		if entity.AttrBase == nil {
			entity.AttrBase = map[string]float64{}
		}
		entity.AttrBase["scale"] = 10
		if got, want := s.commandSourceEyeHeight(&dataEntity{sess: p}), float64(float32(1.27)*2); got != want {
			t.Fatalf("cached crouch eye%g want%g", got, want)
		}
		entity.Pose = 2
		if got, want := s.commandSourceEyeHeight(&dataEntity{sess: p}), float64(float32(.2)); got != want {
			t.Fatalf("fixed sleeping eye%g want%g", got, want)
		}
	})
}
func TestExecuteAnchorVariantEyes(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		name                string
		baby, small, marker bool
		size                int32
		want                float32
	}{{"armor_stand", false, false, true, 0, 0}, {"armor_stand", false, true, false, 0, .9875}, {"cow", true, false, false, 0, .69}, {"zombie", true, false, false, 0, .775}, {"slime", false, false, false, 4, float32(.325) * 4}, {"bee", true, false, false, 0, .15}, {"donkey", true, false, false, 0, .7125}, {"glow_squid", true, false, false, 0, .37}, {"trader_llama", true, false, false, 0, .88825}, {"camel", true, false, false, 0, 1.38}} {
		e := &Entity{Type: mustEntityType(tc.name), Baby: tc.baby, Small: tc.small, ArmorStandMarker: tc.marker, CubeSize: tc.size}
		if got := s.commandSourceEyeHeight(&dataEntity{mob: e}); got != float64(tc.want) {
			t.Fatalf("%s eye%g want%g", tc.name, got, tc.want)
		}
	}
}
func TestExecuteAnchorFrameCopiesValue(t *testing.T) {
	parent := &commandSourceFrame{anchorEyes: false}
	ctx := &executeContext{anchorEyes: true}
	child := executeSourceFrame(parent, ctx)
	ctx.anchorEyes = false
	if !child.anchorEyes || parent.anchorEyes {
		t.Fatal("eye anchor did not snapshot by value")
	}
}
