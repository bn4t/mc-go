package worldgen

import (
	"testing"
	"time"
)

func TestZZDecoTiming(t *testing.T) {
	g, _ := NewGenerator(Vanilla(), "overworld", "overworld", 12345)
	chunks := map[[2]int32]*ProtoChunk{}
	st := time.Now()
	for x := int32(0); x < 8; x++ {
		for z := int32(-6); z < 2; z++ {
			chunks[[2]int32{x, z}] = g.Terrain(x, z)
		}
	}
	t.Logf("terrain 64: %v", time.Since(st)/64)
	st = time.Now()
	n := 0
	for x := int32(1); x < 7; x++ {
		for z := int32(-5); z < 1; z++ {
			g.Decorate(g.NewRegion(chunks[[2]int32{x, z}], chunks))
			n++
		}
	}
	t.Logf("decorate %d: %v each", n, time.Since(st)/time.Duration(n))
}
