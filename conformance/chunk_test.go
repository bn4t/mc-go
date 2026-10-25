package conformance

import (
	"fmt"
	"os"
	"testing"
)

func TestDecodeOfficialChunksAndNegativeControls(t *testing.T) {
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			version := "26.1"
			states, biomes := 29873, 65
			if number == 776 {
				version = "26.2"
				states, biomes = 32366, 66
			}
			body, err := os.ReadFile("testdata/chunk-" + version + ".bin")
			if err != nil {
				t.Fatal(err)
			}
			shape := ChunkShape{24, states, biomes}
			chunk, err := DecodeChunk(number, body, shape)
			if err != nil {
				t.Fatal(err)
			}
			if len(chunk.Blocks) != 24*4096 {
				t.Fatal("incomplete chunk")
			}
			for _, bad := range [][]byte{body[:len(body)-1], append(append([]byte(nil), body...), 0), body[:10]} {
				if _, err := DecodeChunk(number, bad, shape); err == nil {
					t.Fatal("malformed chunk accepted")
				}
			}
			if _, err := DecodeChunk(number, body, ChunkShape{24, 1, biomes}); err == nil {
				t.Fatal("out of range block accepted")
			}
		})
	}
}
