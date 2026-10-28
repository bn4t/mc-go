package server

import (
	"math"
	"testing"

	"mc-go/internal/world"
)

// TestNormalLevelTypeGeneratesNoiseTerrain starts a server with the
// normal level type and checks the overworld serves generated terrain:
// a dry spawn on solid ground above sea level's floor, more than one
// biome around it, and chunk snapshots carrying those quart biomes.
func TestNormalLevelTypeGeneratesNoiseTerrain(t *testing.T) {
	s := New("unused")
	s.WorldPath = ""
	s.LevelType = "normal"
	s.WorldSeed = 12345
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	d := s.dims[dimOverworld]
	if d.flat || d.seaLevel != 63 {
		t.Fatalf("overworld still flat: flat=%v sea=%d", d.flat, d.seaLevel)
	}
	fx, fy, fz := s.WorldSpawn()
	x, y, z := int(math.Floor(fx)), int(fy), int(math.Floor(fz))
	below := d.w.GetBlock(world.Pos{X: x, Y: y - 1, Z: z})
	if below == world.Air {
		t.Fatalf("spawn %v,%v,%v stands on air", x, y, z)
	}
	if name, _ := world.StateOwner(below); name == "water" {
		t.Fatalf("spawn %v,%v,%v stands in water", x, y, z)
	}
	biomes := map[string]bool{}
	for qx := -64; qx <= 64; qx += 8 {
		for qz := -64; qz <= 64; qz += 8 {
			biomes[s.biomeAt(d, world.Pos{X: qx * 4, Y: 64, Z: qz * 4})] = true
		}
	}
	if len(biomes) < 3 {
		t.Fatalf("only biomes %v around spawn", biomes)
	}
	snap := captureChunkSnapshot(d, x>>4, z>>4, nil, nil)
	if snap.projectionErr != nil {
		t.Fatal(snap.projectionErr)
	}
	if len(snap.biomes) != d.height/16*64 {
		t.Fatalf("snapshot carries %d quart biomes", len(snap.biomes))
	}
}

// TestNormalLevelTypeGeneratesNetherAndEnd checks the normal level type
// also gives the nether and the End their vanilla generators: a bedrock
// floor and nether biomes below, the central End island of end stone and
// the outer End biomes beyond it.
func TestNormalLevelTypeGeneratesNetherAndEnd(t *testing.T) {
	s := New("unused")
	s.WorldPath = ""
	s.LevelType = "normal"
	s.WorldSeed = 12345
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	nether, end := s.dims[dimNether], s.dims[dimEnd]
	if nether.flat || nether.seaLevel != 32 || end.flat || end.seaLevel != 0 {
		t.Fatalf("nether flat=%v sea=%d, end flat=%v sea=%d", nether.flat, nether.seaLevel, end.flat, end.seaLevel)
	}
	if b := nether.w.GetBlock(world.Pos{X: 0, Y: 0, Z: 0}); b != world.BlockStateMust("bedrock") {
		t.Fatalf("nether floor is %v", b)
	}
	if name := s.biomeAt(nether, world.Pos{X: 0, Y: 64, Z: 0}); name == "minecraft:plains" || name == "" {
		t.Fatalf("nether biome %q", name)
	}
	stone := 0
	for y := 0; y < 128; y++ {
		if end.w.GetBlock(world.Pos{X: 0, Y: y, Z: 0}) == world.BlockStateMust("end_stone") {
			stone++
		}
	}
	if stone == 0 {
		t.Fatal("no end stone under the End's origin")
	}
	if name := s.biomeAt(end, world.Pos{X: 0, Y: 64, Z: 0}); name != "minecraft:the_end" {
		t.Fatalf("End origin biome %q", name)
	}
	outer := map[string]bool{}
	for x := 1100; x <= 3000; x += 100 {
		outer[s.biomeAt(end, world.Pos{X: x, Y: 64, Z: 0})] = true
	}
	if outer["minecraft:the_end"] || len(outer) < 2 {
		t.Fatalf("outer End biomes %v", outer)
	}
}
