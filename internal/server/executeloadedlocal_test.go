package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestExecuteLoadedLocalDoesNotGenerate(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	d := s.dims[dimOverworld]
	old := d.w
	t.Cleanup(old.Stop)
	provider := &scheduledPolicyProvider{missing: true}
	d.w = world.NewProvided(nil, 1, provider)
	ctx := &executeContext{dim: d, alive: true, pos: [3]float64{4000.5, 70, 0.5}, rotation: [2]float32{90, 0}}
	_, got, _, _, err := s.executeCondition(ctx, "", []string{"loaded", "^", "^", "^4000"}, 4000, 70, 0, 0)
	if err != nil || got {
		t.Fatalf("missing local target: got %v error %+v", got, err)
	}
	if calls := provider.calls.Load(); calls != 0 {
		t.Fatalf("local loaded query generated %d columns", calls)
	}
}
