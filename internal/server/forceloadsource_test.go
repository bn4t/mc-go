package server

import "testing"

func TestForceLoadColumnsUseSourceOrigin(t *testing.T) {
	origin := [3]float64{127.75, -58, -.25}
	for _, tc := range []struct {
		args []string
		x, z int
	}{{[]string{"~", "~"}, 127, -1}, {[]string{"~1", "~-16"}, 128, -17}, {[]string{"80", "96"}, 80, 96}} {
		x, z, err := parseColumnPos(origin, tc.args, 0, 0)
		if err != nil || x != tc.x || z != tc.z {
			t.Fatalf("%v=(%d,%d,%v) want%d,%d", tc.args, x, z, err, tc.x, tc.z)
		}
	}
}
func TestForceLoadNeutralFrameDimensionAndOrigin(t *testing.T) {
	s := simulationTestServer(t)
	s.simulationAction(func() {
		frame := &commandSourceFrame{actor: neutralCommandActor, dim: s.dims[dimNether], pos: [3]float64{127.75, -58, -.25}}
		s.withCommandFrame(frame, func() reply { return s.runForceLoad(neutralCommandActor, []string{"add", "~", "~"}) })
		if !s.forcedChunks[dimNether][chunkLong(7, -1)] || len(s.forcedChunks[dimOverworld]) != 0 {
			t.Fatalf("wrong source force set%v", s.forcedChunks)
		}
	})
}
func TestExecuteDimensionScalesOnlyHorizontalSource(t *testing.T) {
	overworld := &dimension{coordScale: 1}
	nether := &dimension{coordScale: 8}
	end := &dimension{coordScale: 1}
	ctx := &executeContext{dim: overworld, pos: [3]float64{128.5, 70.25, 256.5}, rotation: [2]float32{35, 20}, anchorEyes: true}
	setExecuteDimension(ctx, nether)
	if ctx.pos != [3]float64{16.0625, 70.25, 32.0625} || !ctx.anchorEyes || ctx.rotation != [2]float32{35, 20} {
		t.Fatalf("nether source%+v", ctx)
	}
	setExecuteDimension(ctx, end)
	if ctx.pos != [3]float64{128.5, 70.25, 256.5} {
		t.Fatalf("end source%v", ctx.pos)
	}
	setExecuteDimension(ctx, end)
	if ctx.pos != [3]float64{128.5, 70.25, 256.5} {
		t.Fatalf("same-level source%v", ctx.pos)
	}
}
