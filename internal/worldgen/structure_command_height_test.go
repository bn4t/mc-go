package worldgen

import "testing"

func TestCommandFullChunkWGHeightmap(t *testing.T) {
	c := NewProtoChunk(0, 0, -64, 384)
	stone := blockNamed("stone")
	c.Set(0, 60, 0, stone)
	c.primeHeightmaps()
	chunks := map[[2]int32]*ProtoChunk{{0, 0}: c}
	cache := CommandWGHeightmaps{}
	height := commandChunkHeight(chunks, -64, cache)
	c.Set(0, 65, 0, stone)
	c.Set(1, 65, 0, stone)
	if got := height(HMOceanFloorWG, 0, 0); got != 66 {
		t.Fatalf("first read=%d want66", got)
	}
	c.Set(0, 70, 0, stone)
	c.Set(1, 75, 0, stone)
	if got := height(HMOceanFloorWG, 1, 0); got != 66 {
		t.Fatalf("whole-chunk priming=%d want66", got)
	}
	if got := height(HMOceanFloorWG, 0, 0); got != 66 {
		t.Fatalf("frozen read=%d want66", got)
	}
	if got := height(HMWorldSurfaceWG, 0, 0); got != 71 {
		t.Fatalf("independent first map=%d want71", got)
	}
	repeated := commandChunkHeight(chunks, -64, cache)
	if got := repeated(HMOceanFloorWG, 0, 0); got != 66 {
		t.Fatalf("repeated command=%d want66", got)
	}
	fresh := commandChunkHeight(chunks, -64, nil)
	if got := fresh(HMOceanFloorWG, 0, 0); got != 71 {
		t.Fatalf("fresh resident=%d want71", got)
	}
	if got := height(HMOceanFloor, 0, 0); got != 71 {
		t.Fatalf("final map=%d want71", got)
	}
	if got := c.OceanFloorWG(0, 0); got != 60 {
		t.Fatalf("natural WG changed=%d", got)
	}
	if got := height(HMOceanFloorWG, 16, 0); got != -64 {
		t.Fatalf("absent chunk=%d", got)
	}
}
