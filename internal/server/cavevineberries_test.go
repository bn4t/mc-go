package server

import (
	"testing"

	"mc-go/internal/world"
)

func TestCaveVineOffspringBerriesIndependentOfParent(t *testing.T) {
	for _, parent := range []string{"false", "true"} {
		t.Run(parent, func(t *testing.T) {
			s, d := randSim(t)
			s.rng = newRandState(731)
			pos := world.Pos{X: 40, Y: -56, Z: 40}
			randPut(t, d, 40, -55, 40, "stone", nil)
			berries := 0
			for i := 0; i < 128; i++ {
				randPut(t, d, 40, -57, 40, "air", nil)
				randPut(t, d, 40, -56, 40, "cave_vines", map[string]string{"age": "24", "berries": parent})
				if !randRoll(s, d, pos, "cave_vines", 1000, func() bool { return randName(d, 40, -57, 40) == "cave_vines" }) {
					t.Fatal("vine failed to grow")
				}
				tip := randGet(t, d, 40, -57, 40)
				if age, _ := intProp(tip, "age"); age != 25 {
					t.Fatalf("tip age %d, want 25", age)
				}
				fruit, _ := world.StateProperty(tip, "berries")
				if fruit == "true" {
					berries++
				}
				old, _ := world.StateProperty(randGet(t, d, 40, -56, 40), "berries")
				if old != parent {
					t.Fatalf("parent fruit changed: %s", old)
				}
			}
			if berries == 0 || berries == 128 {
				t.Fatalf("offspring berries=%d/128; want independent berry and berry-free tips", berries)
			}
		})
	}
}
