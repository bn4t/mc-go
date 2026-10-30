package server

import (
	"strings"
	"testing"

	"mc-go/internal/world"
)

func TestExecuteBlockReadinessDoesNotGenerateAndCannotBeNegated(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	p := placeSession(t, s, "Reader", 8.5, -60, 5.5)
	p.active.Store(true)
	p.spawned.Store(true)
	d := s.dims[dimOverworld]
	original := d.w
	t.Cleanup(original.Stop)
	provider := &scheduledPolicyProvider{missing: true}
	d.w = world.NewProvided(nil, 1, provider)
	s.simulationAction(func() {
		for _, prefix := range []string{"if", "unless"} {
			for _, predicate := range []string{"air", "#minecraft:logs"} {
				leaf := s.runExecute("Reader", strings.Fields(prefix+" block 8 -60 4 "+predicate))
				if len(leaf.comp.extra) != 1 || leaf.comp.extra[0].translate != "argument.pos.unloaded" || leaf.fail != nil {
					t.Fatalf("%s %s leaf %#v", prefix, predicate, leaf)
				}
				run := s.runExecute("Reader", strings.Fields(prefix+" block 8 -60 4 "+predicate+" run say MUST_NOT_RUN"))
				if run.text != "" || run.comp.text != "" || run.comp.translate != "" || len(run.comp.extra) != 0 {
					t.Fatalf("unavailable %s %s ran or emitted error: %#v", prefix, predicate, run)
				}
			}
		}
		for _, predicate := range []string{"minecraft:not_a_block", "oak_log[axis=bogus]", "#minecraft:not_a_tag"} {
			ctx := &executeContext{dim: d, alive: true}
			_, _, _, _, err := s.executeCondition(ctx, "Reader", []string{"block", "8", "-60", "4", predicate}, 0, 0, 0, 2)
			if err == nil || ctx.condErr != nil || !ctx.alive {
				t.Fatalf("readiness preempted parser error for %s", predicate)
			}
		}
	})
	if calls := provider.calls.Load(); calls != 0 {
		t.Fatalf("predicate queried unmaterialized provider %d times", calls)
	}
}

func TestExecuteBlockReadinessHeightAndManualEmbedding(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 4008, Y: -59, Z: 4}
	d.w.SetBlock(pos, world.Stone)
	ctx := &executeContext{dim: d, alive: true}
	_, matched, _, _, err := s.executeCondition(ctx, "", []string{"block", "4008", "-59", "4", "stone"}, 0, 0, 0, 2)
	if err != nil || !matched || !ctx.alive {
		t.Fatal("manual no-controller command embedding lost its existing world access")
	}
	p := placeSession(t, s, "Reader", 8.5, -60, 5.5)
	p.active.Store(true)
	p.spawned.Store(true)
	for _, tc := range []struct{ x, key string }{{"8", "argument.pos.outofworld"}, {"4008", "argument.pos.unloaded"}} {
		for _, prefix := range []string{"if", "unless"} {
			r := s.runExecute("Reader", strings.Fields(prefix+" block "+tc.x+" 1000 4 air"))
			if len(r.comp.extra) != 1 || r.comp.extra[0].translate != tc.key || r.fail != nil {
				t.Fatalf("%s %s height precedence %#v", prefix, tc.x, r)
			}
		}
	}
}
