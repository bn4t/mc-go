package server

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"mc-go/internal/world"
)

func columnFactsDimension(t *testing.T, name string, minY, height int, gen func(world.Pos) world.Block) *dimension {
	t.Helper()
	w := world.NewGen(nil, 8, gen)
	t.Cleanup(w.Stop)
	return &dimension{name: name, w: w, minY: minY, height: height, skylight: true}
}

func columnFactsServer(ds ...*dimension) *Server {
	s := &Server{randSections: map[randSectionKey]uint8{}, dims: map[string]*dimension{}}
	for _, d := range ds {
		s.dims[d.name] = d
	}
	return s
}

func TestAnalyzeColumnFactsSemanticEdges(t *testing.T) {
	const minY, height = -64, 384
	states := make([]world.Block, 256*height)
	want := analyzedColumnFacts{sectionFlags: make([]uint8, height/16), nonEmpty: make([]int, height/16)}
	for i := range want.skyTops {
		want.skyTops[i] = minY - 1
	}
	wetPane, ok := world.BlockStateOf("glass_pane", map[string]string{"waterlogged": "true"})
	if !ok {
		t.Fatal("missing waterlogged glass pane")
	}
	// Expectations are explicit behavioral facts. In particular, the crop
	// shade flag, non-air count and sky obstruction are different predicates.
	for _, cell := range []struct {
		x, y, z int
		block   world.Block
		flags   uint8
		blocks  bool
	}{
		{0, 0, 0, world.Stone, secOpaque, true},
		{1, 0, 0, world.BlockStateMust("wheat"), secTickable, false},
		// Both flag bits already saturated; later facts must still be read.
		{10, 7, 0, world.Stone, secOpaque, true},
		{0, 15, 0, world.Stone, secOpaque, true},
		{1, 15, 0, world.BlockStateMust("wheat"), secTickable, false},
		{2, 50, 0, world.BlockStateMust("glass"), 0, false},
		{3, 63, 0, world.BlockStateMust("glass_pane"), 0, false},
		{4, 64, 0, wetPane, 0, true},
		{5, 80, 0, world.BlockStateMust("water"), 0, true},
		{6, 96, 0, world.BlockStateMust("oak_leaves"), secTickable, true},
		{7, 112, 0, world.BlockStateMust("cave_air"), secOpaque, false},
		{8, 128, 0, world.Block(^uint32(0)), 0, false},
		{9, 144, 0, world.BlockStateMust("torch"), 0, false},
		{15, height - 1, 15, wetPane, 0, true},
	} {
		states[cell.y*256+cell.z*16+cell.x] = cell.block
		want.nonEmpty[cell.y/16]++
		want.sectionFlags[cell.y/16] |= cell.flags
		if cell.blocks {
			want.skyTops[cell.z*16+cell.x] = int32(minY + cell.y)
		}
	}
	before := append([]world.Block(nil), states...)
	if got := analyzeColumnFacts(states, minY); !reflect.DeepEqual(got, want) {
		t.Fatalf("column facts differ:\ngot %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(states, before) {
		t.Fatal("analysis mutated detached canonical states")
	}
}

func TestAnalyzeColumnFactsMatchesIndependentExistingScans(t *testing.T) {
	choices := []world.Block{world.Air, world.Stone, world.BlockStateMust("wheat"), world.BlockStateMust("glass"), world.BlockStateMust("water"), world.BlockStateMust("cave_air"), world.Block(^uint32(0))}
	for _, fixture := range []struct{ minY, height, cx, cz int }{{-64, 384, -2, -1}, {0, 32, -1, 3}} {
		t.Run(fmt.Sprintf("minY%d_height%d", fixture.minY, fixture.height), func(t *testing.T) {
			d := columnFactsDimension(t, dimOverworld, fixture.minY, fixture.height, func(p world.Pos) world.Block {
				index := (uint64(p.X)*37 ^ uint64(p.Y)*13 ^ uint64(p.Z)*19) % uint64(len(choices))
				return choices[index]
			})
			s := columnFactsServer(d)
			states := make([]world.Block, 256*d.height)
			d.w.FillBox(world.Pos{X: fixture.cx * 16, Y: d.minY, Z: fixture.cz * 16}, world.Pos{X: fixture.cx*16 + 15, Y: d.minY + d.height - 1, Z: fixture.cz*16 + 15}, states)
			got := analyzeColumnFacts(states, d.minY)
			// These existing scans retain their independent traversal and
			// early-exit behavior; none calls the combined analyzer.
			for sec := 0; sec < d.sections(); sec++ {
				key := randSectionKey{dim: d.name, x: fixture.cx, y: sec, z: fixture.cz}
				if want := s.sectionFlags(d, key); got.sectionFlags[sec] != want {
					t.Fatalf("section%d flags=%d want%d", sec, got.sectionFlags[sec], want)
				}
				if want := nonEmptyCnts(d, fixture.cx, fixture.cz)[sec]; got.nonEmpty[sec] != want {
					t.Fatalf("section%d count=%d want%d", sec, got.nonEmpty[sec], want)
				}
			}
			if want := chunkTops(d, fixture.cx, fixture.cz); got.skyTops != want {
				t.Fatalf("sky tops=%v want%v", got.skyTops, want)
			}
		})
	}
}

func TestColumnFactsPreservePartialCachesAndDimensionIsolation(t *testing.T) {
	air := func(world.Pos) world.Block { return world.Air }
	a := columnFactsDimension(t, dimOverworld, -16, 48, air)
	b := columnFactsDimension(t, dimEnd, 0, 32, func(world.Pos) world.Block { return world.Stone })
	s := columnFactsServer(a, b)
	const cx, cz = -2, -1
	ck := [2]int{cx, cz}
	// A partially warmed flags cache must retain both its populated entry
	// and zero-valued entries when the missing facts are installed.
	key := randSectionKey{dim: a.name, x: cx, y: 0, z: cz}
	s.sectionFlags(a, key)
	counts := nonEmptyCnts(a, cx, cz)
	s.warmSectionFlags(a, cx, cz)
	counts[99] = 123 // Verify the existing map identity is retained.
	if a.nonEmptyCnt[ck][99] != 123 {
		t.Fatal("existing non-air cache was replaced")
	}
	delete(counts, 99)
	tops := a.skyTops[ck]
	delete(a.nonEmptyCnt, ck)
	delete(s.randSections, randSectionKey{dim: a.name, x: cx, y: 2, z: cz})
	s.warmSectionFlags(a, cx, cz)
	if a.skyTops[ck] != tops {
		t.Fatal("existing sky-top cache was replaced")
	}
	s.warmSectionFlags(b, cx, cz)
	for sec := 0; sec < a.sections(); sec++ {
		if flags, ok := s.sectionFlagsWarm(randSectionKey{dim: a.name, x: cx, y: sec, z: cz}); !ok || flags != 0 || a.nonEmptyCnt[ck][sec] != 0 {
			t.Fatalf("air dimension section%d flags=%d warm=%v count=%d", sec, flags, ok, a.nonEmptyCnt[ck][sec])
		}
	}
	for sec := 0; sec < b.sections(); sec++ {
		if flags, ok := s.sectionFlagsWarm(randSectionKey{dim: b.name, x: cx, y: sec, z: cz}); !ok || flags != secOpaque || b.nonEmptyCnt[ck][sec] != 4096 {
			t.Fatalf("solid dimension section%d flags=%d warm=%v count=%d", sec, flags, ok, b.nonEmptyCnt[ck][sec])
		}
	}
	if a.skyTops[ck][0] != -17 || b.skyTops[ck][0] != 31 {
		t.Fatal("dimension-local geometry crossed caches")
	}
}

func TestColumnFactsPreservePopulatedFlags(t *testing.T) {
	d := columnFactsDimension(t, dimOverworld, -16, 48, func(world.Pos) world.Block { return world.Air })
	s := columnFactsServer(d)
	key := randSectionKey{dim: d.name, x: -2, y: 1, z: -1}
	// Existing entries are authoritative to the cache lifecycle, including
	// when the detached scan would produce a different classification.
	s.randSections[key] = secTickable | secOpaque
	s.warmSectionFlags(d, key.x, key.z)
	if got, warm := s.sectionFlagsWarm(key); !warm || got != secTickable|secOpaque {
		t.Fatalf("existing flags=%d warm=%v", got, warm)
	}
}

func TestColumnFactsSkylessWarmupKeepsLightCachesCold(t *testing.T) {
	d := columnFactsDimension(t, dimNether, 0, 32, func(world.Pos) world.Block { return world.Stone })
	d.skylight = false
	s := columnFactsServer(d)
	s.warmSectionFlags(d, -2, -1)
	if d.nonEmptyCnt != nil || d.skyTops != nil {
		t.Fatal("skyless warmup populated unused light facts")
	}
	for sec := 0; sec < d.sections(); sec++ {
		if flags, warm := s.sectionFlagsWarm(randSectionKey{dim: d.name, x: -2, y: sec, z: -1}); !warm || flags != secOpaque {
			t.Fatalf("skyless section %d flags=%d warm=%v", sec, flags, warm)
		}
	}
}

func TestColumnFactsFreshAfterRestore(t *testing.T) {
	d := columnFactsDimension(t, dimOverworld, -16, 48, func(world.Pos) world.Block { return world.Air })
	s := columnFactsServer(d)
	removed := world.Pos{X: -17, Y: -16, Z: -1}
	kept := world.Pos{X: -18, Y: 31, Z: -2}
	d.w.SetBlock(removed, world.Stone)
	d.w.Restore(map[world.Pos]world.Block{kept: world.Stone})
	// Restore precedes the first derived-cache population. Raw Restore of
	// already cached server facts has no new invalidation contract here.
	s.warmSectionFlags(d, -2, -1)
	ck := [2]int{-2, -1}
	if d.nonEmptyCnt[ck][0] != 0 || d.nonEmptyCnt[ck][2] != 1 {
		t.Fatalf("restored counts=%v", d.nonEmptyCnt[ck])
	}
	if got := skyTop(d, removed.X, removed.Z); got != -17 {
		t.Fatalf("deleted override top=%d", got)
	}
	if got := skyTop(d, kept.X, kept.Z); got != 31 {
		t.Fatalf("restored override top=%d", got)
	}
}

func TestColumnFactsOwnedEditsMatchUnpreparedLighting(t *testing.T) {
	gen := func(p world.Pos) world.Block {
		if p.Y == -16 {
			return world.Stone
		}
		return world.Air
	}
	a := columnFactsDimension(t, dimOverworld, -16, 48, gen)
	b := columnFactsDimension(t, dimOverworld, -16, 48, gen)
	optimized, reference := columnFactsServer(a), columnFactsServer(b)
	const cx, cz = -2, -1
	optimized.warmSectionFlags(a, cx, cz)
	// Preserve the prior owner preparation behavior as an independent
	// comparison: flags only, then ordinary lazy light initialization.
	for sec := 0; sec < b.sections(); sec++ {
		reference.sectionFlags(b, randSectionKey{dim: b.name, x: cx, y: sec, z: cz})
	}
	checkLight := func(stage string) {
		t.Helper()
		if got, want := encodeLightDataFor(a, cx, cz), encodeLightDataFor(b, cx, cz); !bytes.Equal(got, want) {
			t.Fatalf("%s: prepared and unprepared light bytes differ", stage)
		}
	}
	checkLight("initial")
	pos := world.Pos{X: -17, Y: 0, Z: -1}
	key := randSectionKey{dim: a.name, x: cx, y: 1, z: cz}
	for _, block := range []world.Block{world.Stone, world.Air, world.Stone, world.Air} {
		// The real command-style pipeline updates the canonical world,
		// invalidates random flags and runs both light change hooks.
		optimized.setBlockAndBroadcastInOwned(a, "", pos, block, true)
		reference.setBlockAndBroadcastInOwned(b, "", pos, block, true)
		if _, warm := optimized.sectionFlagsWarm(key); warm {
			t.Fatal("edited section flags remained cached")
		}
		wantCount, wantTop, wantFlags := 0, int32(-16), uint8(0)
		if block != world.Air {
			wantCount, wantTop, wantFlags = 1, 0, secOpaque
		}
		if count, top := a.nonEmptyCnt[[2]int{cx, cz}][1], skyTop(a, pos.X, pos.Z); count != wantCount || top != wantTop {
			t.Fatalf("after state%d count=%d top=%d want%d/%d", block, count, top, wantCount, wantTop)
		}
		optimized.warmSectionFlags(a, cx, cz)
		if flags, warm := optimized.sectionFlagsWarm(key); !warm || flags != wantFlags {
			t.Fatalf("rewarmed flags=%d warm=%v want%d", flags, warm, wantFlags)
		}
		checkLight(fmt.Sprintf("state%d", block))
	}
}
