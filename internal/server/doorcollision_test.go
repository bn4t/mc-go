package server

import "testing"

// Facts from the runtime 26.2 BlockState collision-shape query, for both
// halves of the same state (DoorBlock itself uses the same local plate).
func TestDoorDirectionalCollisionFacts(t *testing.T) {
	faces := []struct{ facing, closed, left, right string }{
		{"north", "south", "west", "east"}, {"south", "north", "east", "west"},
		{"west", "east", "south", "north"}, {"east", "west", "north", "south"},
	}
	for _, face := range faces {
		for _, open := range []string{"false", "true"} {
			for _, hinge := range []string{"left", "right"} {
				for _, half := range []string{"lower", "upper"} {
					side := face.closed
					if open == "true" {
						side = face.left
						if hinge == "right" {
							side = face.right
						}
					}
					want := map[string]cellBox{"north": {0, 0, 0, 1, 1, .1875}, "south": {0, 0, .8125, 1, 1, 1}, "west": {0, 0, 0, .1875, 1, 1}, "east": {.8125, 0, 0, 1, 1, 1}}[side]
					block := mkBlockState(t, "oak_door", map[string]string{"facing": face.facing, "open": open, "hinge": hinge, "half": half})
					got, ok := collisionBoxesOf(block)
					if !ok || len(got) != 1 || got[0] != want {
						t.Errorf("%s open%s hinge%s half%s got%v want%v", face.facing, open, hinge, half, got, want)
					}
				}
			}
		}
	}
}
