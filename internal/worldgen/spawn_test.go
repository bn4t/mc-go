package worldgen

import (
	"fmt"
	"testing"
)

// TestFindSpawnMatchesVanilla checks the climate spawn search against
// the official server's findSpawnPosition for twelve seeds.
func TestFindSpawnMatchesVanilla(t *testing.T) {
	settings, _ := Settings("minecraft:overworld")
	n := 0
	for _, l := range loadProbe(t) {
		if l.kind != "spawn" {
			continue
		}
		seed := atoi64(t, l.fields[0])
		x, z := NewRandomState(settings, seed).FindSpawn()
		if got := fmt.Sprintf("%d,%d", x, z); got != l.value {
			t.Errorf("seed %d: spawn %s, want %s", seed, got, l.value)
		}
		n++
	}
	if n != 12 {
		t.Fatalf("checked %d seeds", n)
	}
	nether, _ := Settings("minecraft:nether")
	if x, z := NewRandomState(nether, 1).FindSpawn(); x != 0 || z != 0 {
		t.Fatalf("no spawn target should search nothing, got %d,%d", x, z)
	}
}
