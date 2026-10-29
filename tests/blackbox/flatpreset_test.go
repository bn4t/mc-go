//go:build blackbox

package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/server"
	"mc-go/internal/world"
)

// TestFlatPresetChunkParity joins a custom flat world (generator-settings
// layers and biome) on both servers and compares the world spawn and the
// streamed chunks: blocks, heightmaps, counts, biome cells, light and
// light masks.
func TestFlatPresetChunkParity(t *testing.T) {
	props := map[string]string{
		"level-type":          "minecraft:flat",
		"generator-settings":  `{"layers":[{"block":"minecraft:bedrock","height":1},{"block":"minecraft:stone","height":3},{"block":"minecraft:sand","height":2}],"biome":"minecraft:desert"}`,
		"generate-structures": "false",
	}
	tg := requireBlackboxFiles(t, props, nil, func(s *server.Server) {
		s.LevelType = server.LevelTypeFlat
		s.FlatPreset = &world.FlatPreset{Biome: "minecraft:desert", Layers: []world.FlatLayer{
			{Block: "minecraft:bedrock", Height: 1}, {Block: "minecraft:stone", Height: 3}, {Block: "minecraft:sand", Height: 2},
		}}
	})
	defer tg.cleanup()
	fetch := func(tag, addr string) (map[[2]int32][]byte, [3]int32) {
		c, pr, pw := joinPlayBlackbox(t, addr, "FlatProbe")
		defer c.Close()
		return collectJoin(t, tag, c, pr, pw)
	}
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = time.Minute
	vChunks, vSpawn := fetch("vanilla", tg.vanilla)
	mChunks, mSpawn := fetch("mc-go", tg.mcgo)
	if vSpawn != mSpawn {
		t.Errorf("world spawn: vanilla %v mc-go %v", vSpawn, mSpawn)
	}
	compareChunkStreams(t, vChunks, mChunks, 24, false)
}
