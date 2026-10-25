package server

import (
	"reflect"
	"testing"

	"mc-go/api/registry"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestChunkProjectedBlockCountMatchesFallbackPalette(t *testing.T) {
	catalog := world.BlockCatalog()
	air, _ := catalog.State(world.Air)
	stone, _ := catalog.State(world.Stone)
	for _, fixture := range []struct {
		name     string
		fallback registry.StateKey
		want     int16
		second   int32
	}{
		{"air", air, 1, 17},
		{"solid", stone, 2, 9},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			mapping, err := registry.NewBlockMapping(900, catalog, []registry.BlockWireEntry{
				{State: air, WireID: 17}, {State: stone, WireID: 9},
			}, registry.MissingBlockPolicy{Mode: registry.VisualFallbackBlocks, Fallback: fixture.fallback})
			if err != nil {
				t.Fatal(err)
			}
			w := world.New(nil, 2)
			t.Cleanup(w.Stop)
			w.SetBlock(world.Pos{X: 0, Y: 0, Z: 0}, world.Stone)
			w.SetBlock(world.Pos{X: 1, Y: 0, Z: 0}, world.Dirt)
			states := make([]world.Block, 4096)
			w.FillBox(world.Pos{}, world.Pos{X: 15, Y: 15, Z: 15}, states)
			before := append([]world.Block(nil), states...)
			snapshot := &chunkSnapshot{height: 16, states: states, catalog: catalog, mapping: mapping, light: encodeLightData()}
			body, err := encodeChunkSnapshot(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			r := protocol.NewPacketReader(body)
			if _, err = r.Int(); err != nil {
				t.Fatal(err)
			}
			if _, err = r.Int(); err != nil {
				t.Fatal(err)
			}
			maps, err := r.VarInt()
			if err != nil {
				t.Fatal(err)
			}
			for i := int32(0); i < maps; i++ {
				if _, err = r.VarInt(); err != nil {
					t.Fatal(err)
				}
				longs, err := r.VarInt()
				if err != nil {
					t.Fatal(err)
				}
				if _, err = r.Read(int(longs) * 8); err != nil {
					t.Fatal(err)
				}
			}
			length, err := r.VarInt()
			if err != nil {
				t.Fatal(err)
			}
			data, err := r.Read(int(length))
			if err != nil {
				t.Fatal(err)
			}
			section := protocol.NewPacketReader(data)
			count, err := section.Short()
			if err != nil || count != fixture.want {
				t.Fatalf("section non-air count=%d, want %d (error %v)", count, fixture.want, err)
			}
			if _, err = section.Short(); err != nil {
				t.Fatal(err)
			}
			values := decodeContainer(t, section, 4096, 4)
			for i, value := range values {
				want := int32(17)
				if i == 0 {
					want = 9
				} else if i == 1 {
					want = fixture.second
				}
				if value != want {
					t.Fatalf("presented block %d=%d, want %d", i, value, want)
				}
			}
			if !reflect.DeepEqual(states, before) || w.GetBlock(world.Pos{X: 1, Y: 0, Z: 0}) != world.Dirt {
				t.Fatal("visual fallback changed authoritative state")
			}
		})
	}
}
