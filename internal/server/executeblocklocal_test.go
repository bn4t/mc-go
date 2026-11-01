package server

import (
	"mc-go/internal/world"
	"strings"
	"testing"
)

func TestExecuteBlockLocalPositionReadsBoundDimension(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	pos := world.Pos{X: 8, Y: 70, Z: 11}
	diamond, _ := world.BlockState("diamond_block")
	emerald, _ := world.BlockState("emerald_block")
	s.dims[dimOverworld].w.SetBlock(pos, diamond)
	s.dims[dimNether].w.SetBlock(pos, emerald)
	for _, row := range []struct {
		dim       string
		predicate string
		want      bool
	}{{dimOverworld, "diamond_block", true}, {dimOverworld, "#minecraft:mineable/pickaxe", true}, {dimOverworld, "emerald_block", false}, {dimNether, "emerald_block", true}, {dimNether, "diamond_block", false}} {
		ctx := &executeContext{dim: s.dims[row.dim], alive: true, pos: [3]float64{24.5, 70.5, 11.5}, rotation: [2]float32{90, 0}}
		_, got, _, _, err := s.executeCondition(ctx, "", []string{"block", "^", "^", "^16", row.predicate}, 24, 70, 11, 2)
		if err != nil || got != row.want {
			t.Fatalf("%s %s result%v error%+v", row.dim, row.predicate, got, err)
		}
	}
}

func TestExecuteBlockLocalPositionPreservesUnavailableGuard(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	p := placeSession(t, s, "LocalReader", 8.5, -60, 5.5)
	p.active.Store(true)
	p.spawned.Store(true)
	d := s.dims[dimOverworld]
	old := d.w
	t.Cleanup(old.Stop)
	provider := &scheduledPolicyProvider{missing: true}
	d.w = world.NewProvided(nil, 1, provider)
	s.simulationAction(func() {
		for _, prefix := range []string{"if", "unless"} {
			leaf := s.runExecute("LocalReader", strings.Fields(prefix+" block ^ ^ ^-1 air"))
			if len(leaf.comp.extra) != 1 || leaf.comp.extra[0].translate != "argument.pos.unloaded" || leaf.fail != nil {
				t.Fatalf("local unavailable %s leaf%#v", prefix, leaf)
			}
			run := s.runExecute("LocalReader", strings.Fields(prefix+" block ^ ^ ^-1 air run say MUST_NOT_RUN"))
			if run.text != "" || run.comp.text != "" || run.comp.translate != "" || len(run.comp.extra) != 0 {
				t.Fatalf("local unavailable %s ran%#v", prefix, run)
			}
		}
	})
	if calls := provider.calls.Load(); calls != 0 {
		t.Fatalf("local condition generated unavailable column%d times", calls)
	}
}
