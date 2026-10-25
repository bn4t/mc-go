package world

import (
	"encoding/binary"
	"sync/atomic"
	"testing"
)

// These fixtures stay unchanged when routing changes, so before/after runs
// compare the same block volume, override density and worker count.
func benchmarkRoutingWorld(b *testing.B) *World {
	b.Helper()
	w := New(nil, 8)
	b.Cleanup(w.Stop)
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				w.SetBlock(Pos{X: x, Y: y, Z: z}, Stone)
			}
		}
	}
	return w
}

func BenchmarkChunkRoutingFillBox(b *testing.B) {
	for _, fixture := range []struct {
		name string
		min  Pos
		max  Pos
	}{
		{"section", Pos{0, 0, 0}, Pos{15, 15, 15}},
		{"chunk", Pos{0, -64, 0}, Pos{15, 319, 15}},
		{"four-chunks", Pos{-16, -64, -16}, Pos{15, 319, 15}},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			w := benchmarkRoutingWorld(b)
			count := (fixture.max.X - fixture.min.X + 1) * (fixture.max.Y - fixture.min.Y + 1) * (fixture.max.Z - fixture.min.Z + 1)
			out := make([]Block, count)
			b.ReportAllocs()
			b.SetBytes(int64(count * binary.Size(Block(0))))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				w.FillBox(fixture.min, fixture.max, out)
			}
			b.StopTimer()
			b.ReportMetric(float64(binary.Size(Block(0))), "state-bytes")
		})
	}
}

func BenchmarkChunkRoutingParallelChunks(b *testing.B) {
	w := benchmarkRoutingWorld(b)
	var next atomic.Int32
	b.SetParallelism(2)
	b.ReportAllocs()
	b.SetBytes(int64(16 * 16 * 384 * binary.Size(Block(0))))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		id := int(next.Add(1) - 1)
		cx, cz := id%4, (id/4)%4
		out := make([]Block, 16*16*384)
		min, max := Pos{cx * 16, -64, cz * 16}, Pos{cx*16 + 15, 319, cz*16 + 15}
		for pb.Next() {
			w.FillBox(min, max, out)
		}
	})
	b.StopTimer()
	b.ReportMetric(float64(binary.Size(Block(0))), "state-bytes")
}
