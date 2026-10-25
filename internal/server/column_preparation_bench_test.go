package server

import (
	"testing"

	"mc-go/internal/world"
)

// This fixture and its ordering are shared by before/after measurements. The
// world workers are live, but no ticking, sockets, or background simulation run.
// Cold means fresh dimension and section caches over the same immutable world;
// it does not include constructing workers or generating a terrain save.
func columnPreparationFixture(b *testing.B) (*Server, *world.World) {
	b.Helper()
	s := New("unused")
	b.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	s.ViewDistance = 2
	w := s.dims[dimOverworld].w
	// A small mixed canopy exercises a non-flat skyline, transparent blocks,
	// water opacity and random-tick flags without changing the base generator.
	palette := []world.Block{world.Stone, world.Grass, world.BlockStateMust("glass"), world.BlockStateMust("water")}
	for z := 4; z < 12; z++ {
		for x := 4; x < 12; x++ {
			w.SetBlock(world.Pos{X: x, Y: -48, Z: z}, palette[(x+z)%len(palette)])
		}
	}
	return s, w
}

func resetColumnPreparation(s *Server, w *world.World) *dimension {
	d := testOverworld(w)
	s.randSections = map[randSectionKey]uint8{}
	s.dims[dimOverworld] = d
	return d
}

func prepareBenchmarkColumnFacts(s *Server, d *dimension) {
	s.warmSectionFlags(d, 0, 0)
	nonEmptyCnts(d, 0, 0)
	topsFor(d, 0, 0)
}

func BenchmarkColumnPreparation(b *testing.B) {
	for _, stage := range []struct {
		name string
		run  func(*Server, *dimension)
	}{
		{"Facts", prepareBenchmarkColumnFacts},
		{"SkyTops", func(_ *Server, d *dimension) { topsFor(d, 0, 0) }},
		{"NonEmptyCounts", func(_ *Server, d *dimension) { nonEmptyCnts(d, 0, 0) }},
	} {
		for _, cold := range []bool{true, false} {
			state := "Warm"
			if cold {
				state = "Cold"
			}
			b.Run(stage.name+"/"+state, func(b *testing.B) {
				s, w := columnPreparationFixture(b)
				d := resetColumnPreparation(s, w)
				stage.run(s, d)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if cold {
						b.StopTimer()
						d = resetColumnPreparation(s, w)
						b.StartTimer()
					}
					stage.run(s, d)
				}
			})
		}
	}
	b.Run("Facts/PartialInvalidated", func(b *testing.B) {
		s, w := columnPreparationFixture(b)
		d := resetColumnPreparation(s, w)
		prepareBenchmarkColumnFacts(s, d)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			s.invalidateRandomTickSection(d, world.Pos{X: 4, Y: -48, Z: 4})
			b.StartTimer()
			prepareBenchmarkColumnFacts(s, d)
		}
	})
	b.Run("SkyMaterialization/FactsWarm", func(b *testing.B) {
		s, w := columnPreparationFixture(b)
		facts := resetColumnPreparation(s, w)
		// These immutable facts can be shared by fresh materialization caches.
		// Prepare them once so benchmark calibration does not repeatedly pay
		// for excluded full-column reads.
		for cx := -2; cx <= 2; cx++ {
			for cz := -2; cz <= 2; cz++ {
				nonEmptyCnts(facts, cx, cz)
				topsFor(facts, cx, cz)
			}
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			d := resetColumnPreparation(s, w)
			d.nonEmptyCnt, d.skyTops = facts.nonEmptyCnt, facts.skyTops
			b.StartTimer()
			ensureSkyChunk(d, 0, 0)
		}
	})
}

// sendWindow includes fresh-window enumeration, all owner-side warmup, and the
// synchronous nine-column opening batch's capture, encoding and queue admission.
// It excludes network delivery and uses no encoder workers, so comparisons
// measure completed work rather than just worker submission. Warm still uses a
// fresh session: it measures a new viewer of already materialized columns.
func BenchmarkSendWindowPreparation(b *testing.B) {
	for _, skylight := range []bool{true, false} {
		name := "Skylight"
		if !skylight {
			name = "NoSkylight"
		}
		for _, cold := range []bool{true, false} {
			state := "Warm"
			if cold {
				state = "Cold"
			}
			b.Run(name+"/"+state+"/SynchronousOpeningBatch", func(b *testing.B) {
				s, w := columnPreparationFixture(b)
				d := resetColumnPreparation(s, w)
				d.skylight = skylight
				// Initialize package-level codec tables before measuring either
				// path. Cold iterations still replace every dimension cache.
				{
					sess := &Session{server: s, ver: LatestVersion(), done: make(chan struct{})}
					sess.dim.Store(d)
					sess.initOutbound()
					if err := s.sendWindow(sess, 0, 0); err != nil {
						b.Fatal(err)
					}
					for len(sess.sendCh) != 0 {
						sess.retireOutbound(<-sess.sendCh)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					if cold {
						d = resetColumnPreparation(s, w)
						d.skylight = skylight
					}
					sess := &Session{server: s, ver: LatestVersion(), done: make(chan struct{})}
					sess.dim.Store(d)
					sess.initOutbound()
					b.StartTimer()
					if err := s.sendWindow(sess, 0, 0); err != nil {
						b.Fatal(err)
					}
					b.StopTimer()
					chunks, bytes := 0, 0
					for len(sess.sendCh) != 0 {
						pkt := <-sess.sendCh
						if pkt.id == sess.ids().PlayChunkData {
							chunks++
							bytes += len(pkt.data)
						}
						sess.retireOutbound(pkt)
					}
					if chunks != initialChunkBatch || len(sess.queued) != 49 {
						b.Fatalf("fixture emitted %d chunks for %d queued columns", chunks, len(sess.queued))
					}
					b.ReportMetric(float64(chunks), "packet-columns/op")
					b.ReportMetric(float64(bytes), "packet-bytes/op")
				}
			})
		}
	}
}
