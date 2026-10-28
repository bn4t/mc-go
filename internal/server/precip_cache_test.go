package server

import (
	"testing"

	"mc-go/internal/world"
)

func TestTopFaceFullTableSpotChecks(t *testing.T) {
	state := func(name string, props map[string]string) world.Block {
		t.Helper()
		b, ok := world.BlockStateOf(name, props)
		if !ok {
			t.Fatalf("unknown state %s %v", name, props)
		}
		return b
	}
	for _, c := range []struct {
		b    world.Block
		full bool
	}{
		{state("stone", nil), true},
		{state("grass_block", nil), true},
		{state("cauldron", nil), false},
		{state("oak_slab", map[string]string{"type": "bottom", "waterlogged": "false"}), false},
		{state("oak_slab", map[string]string{"type": "top", "waterlogged": "false"}), true},
		{state("snow", map[string]string{"layers": "8"}), false},
		{world.Air, false},
	} {
		if got := topFaceFull(c.b); got != c.full {
			name, _ := world.StateOwner(c.b)
			t.Errorf("%s: top face full = %v, want %v", name, got, c.full)
		}
	}
}

func TestSnowNeedsASupportItCanSurviveOn(t *testing.T) {
	s := New("unused")
	defer s.Stop()
	d := s.dims[dimOverworld]
	cauldron, _ := world.BlockStateOf("cauldron", nil)
	ice, _ := world.BlockStateOf("ice", nil)
	soul, _ := world.BlockStateOf("soul_sand", nil)
	for _, c := range []struct {
		support world.Block
		want    bool
	}{
		{world.Stone, true},
		{cauldron, false},
		{ice, false},
		{soul, true},
	} {
		pos := world.Pos{X: 40, Y: -55, Z: 40}
		d.w.SetBlock(posBelow(pos, 1), c.support)
		if got := snowLayerCanSurvive(s, d, pos, world.Air); got != c.want {
			name, _ := world.StateOwner(c.support)
			t.Errorf("snow on %s: %v want %v", name, got, c.want)
		}
	}
}

func TestOpaqueTopFollowsRawWrites(t *testing.T) {
	s := New("unused")
	defer s.Stop()
	d := s.dims[dimOverworld]
	crop := world.Pos{X: 8, Y: -60, Z: 8}
	if !s.cropLit(d, crop) {
		t.Fatal("open sky must read lit")
	}
	// A raw world write — no section hook — must still darken it.
	d.w.SetBlock(world.Pos{X: 8, Y: -40, Z: 8}, world.Stone)
	if s.cropLit(d, crop) {
		t.Fatal("a stone roof placed by a raw write left the cache stale")
	}
	d.w.SetBlock(world.Pos{X: 8, Y: -40, Z: 8}, world.Air)
	if !s.cropLit(d, crop) {
		t.Fatal("removing the roof left the cache stale")
	}
}

func TestChunkLoadedFollowsServerViewDistance(t *testing.T) {
	s := New("unused")
	defer s.Stop()
	d := s.dims[dimOverworld]
	if s.chunkLoaded(d, 0, 0) {
		t.Fatal("nothing is loaded without a player")
	}
	sess, _ := enforceSession(t, s, "Viewer")
	sess.setPos(8, -60, 8, 0, 0, true)
	sess.placeTicket()
	r := s.ViewDistance + 2
	for _, c := range []struct {
		cx, cz int
		want   bool
	}{
		{r, 0, true}, {r, r, true}, {-r, r, true}, {r + 1, 0, false}, {0, -r - 1, false},
	} {
		if got := s.chunkLoaded(d, c.cx, c.cz); got != c.want {
			t.Errorf("chunk (%d,%d): loaded=%v want %v", c.cx, c.cz, got, c.want)
		}
	}
}

func TestTicketsStayBehindATeleport(t *testing.T) {
	s := New("unused")
	defer s.Stop()
	d := s.dims[dimOverworld]
	sess, _ := enforceSession(t, s, "Mover")
	sess.setPos(8, -60, 8, 0, 0, true)
	sess.placeTicket()
	// A server-side teleport moves the player, not the tickets.
	sess.setPos(16*40+8, -60, 8, 0, 0, true)
	if !s.chunkLoaded(d, 0, 0) || s.chunkLoaded(d, 40, 0) {
		t.Fatal("loaded ring followed the teleport instead of the ticket")
	}
	sim := s.simulationDistance()
	if !s.chunkBlockTicking(d, sim+1, 0) || s.chunkBlockTicking(d, sim+2, 0) {
		t.Fatalf("block ticking ring is not simulation distance + 1 around the ticket")
	}
	sess.placeTicket()
	if !s.chunkLoaded(d, 40, 0) {
		t.Fatal("an accepted move re-centres the tickets")
	}
}
