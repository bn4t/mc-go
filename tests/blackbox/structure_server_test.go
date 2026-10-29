//go:build blackbox

package blackbox

import (
	"path/filepath"
	"strings"
	"testing"

	"mc-go/internal/server"
	"mc-go/internal/world"
)

// TestStructureServerSaveParity pins structures through the server: a
// generated normal world with generate-structures on places the plains
// village and saves each chunk's "structures" compound (starts in the
// start chunk, references in every chunk a start reaches) as vanilla's
// region files hold them, next to the same blocks. The real server's
// saved chunks carry fluid flow from the generation-marked fluids
// ticking while neighbours load; mc-go's frozen world does not run
// them, so cells where one side is air or fluid and the other fluid
// are accepted (the flow itself is pinned by
// TestNormalWorldSettledParity).
func TestStructureServerSaveParity(t *testing.T) {
	const seed = 12345
	areas := []terrainArea{{-14, -58, -7, -48}}
	ov := structureOverrides(nil, "villages")
	region := vanillaStructureTerrain(t, seed, stageDatapack(ov), "overworld", areas)

	worldDir := filepath.Join(t.TempDir(), "world")
	port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	var srv *server.Server
	startMcGoCfg(t, port, func(s *server.Server) {
		s.WorldDir = worldDir
		s.LevelType = server.LevelTypeNormal
		s.WorldSeed = seed
		s.WorldgenOverlay = ov
		s.GenerateStructures = true
		srv = s
	})
	srv.SetFrozen(true)
	for _, a := range areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				srv.World().GetBlock(world.Pos{X: int(cx) * 16, Y: 0, Z: int(cz) * 16})
			}
		}
	}
	if err := srv.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	mRegion := filepath.Join(world.DimensionDir(worldDir, "overworld"), "region")

	starts, refs, blocks, bad, flow := 0, 0, 0, 0, 0
	for _, a := range areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				v := readSavedChunk(t, region, cx, cz, -64, 384)
				m := readSavedChunk(t, mRegion, cx, cz, -64, 384)
				vs, _ := v.raw["structures"].(map[string]any)
				ms, _ := m.raw["structures"].(map[string]any)
				if d := nbtDiff("", ms, vs); d != "" {
					t.Errorf("chunk %d,%d structures differ at %s", cx, cz, d)
				}
				if ss, ok := vs["starts"].(map[string]any); ok {
					starts += len(ss)
				}
				if rs, ok := vs["References"].(map[string]any); ok {
					refs += len(rs)
				}
				for i := range v.blocks {
					blocks++
					if v.blocks[i] != m.blocks[i] && fluidFlowCell(v.blocks[i], m.blocks[i]) {
						flow++
						continue
					}
					if v.blocks[i] != m.blocks[i] {
						if bad < 5 {
							x, z, y := i&15, (i>>4)&15, i>>8-64
							t.Errorf("chunk %d,%d block %d,%d,%d: vanilla %s mc-go %s", cx, cz, int(cx)*16+x, y, int(cz)*16+z, v.blocks[i], m.blocks[i])
						}
						bad++
					}
				}
			}
		}
	}
	t.Logf("%d starts, %d chunk reference sets, %d blocks compared (%d differ, %d fluid-flow cells accepted)", starts, refs, blocks, bad, flow)
	if starts == 0 {
		t.Error("no structure starts in the compared area")
	}
}

// fluidFlowCell reports a cell that differs only by fluid flow: one
// side water or lava, the other air or fluid.
func fluidFlowCell(a, b string) bool {
	fluid := func(s string) bool { return strings.HasPrefix(s, "water") || strings.HasPrefix(s, "lava") }
	return (fluid(a) && (fluid(b) || b == "air")) || (fluid(b) && a == "air")
}
