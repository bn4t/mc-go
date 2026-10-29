package server

import (
	"mc-go/internal/world"
	"testing"
)

// An immune block encountered by one dragon part must not suppress the
// destruction performed by later parts in the same tick.
func TestDragonWallChecksAllParts(t *testing.T) {
	for _, grief := range []bool{true, false} {
		s := New("unused")
		d := s.dims[dimOverworld]
		if !grief {
			s.gameRules["minecraft:mob_griefing"] = "false"
		}
		head := world.Pos{X: 0, Y: 160, Z: -6}
		neck := world.Pos{X: 0, Y: 160, Z: -4}
		body := world.Pos{X: 0, Y: 160, Z: 0}
		d.w.SetBlock(head, world.BlockStateMust("bedrock"))
		d.w.SetBlock(neck, world.BlockStateMust("stone"))
		d.w.SetBlock(body, world.BlockStateMust("dragon_egg"))
		e := s.newEnderDragon(d, 0.5, 160, 0.5)
		s.dragonPlaceParts(e, d)
		if d.w.GetBlock(head) != world.BlockStateMust("bedrock") || !e.dragon.inWall {
			t.Fatalf("grief=%t immune head block did not retain wall state", grief)
		}
		for _, p := range []world.Pos{neck, body} {
			if air := d.w.GetBlock(p) == world.Air; air != grief {
				t.Errorf("grief=%t block%v air=%t", grief, p, air)
			}
		}
	}
}
