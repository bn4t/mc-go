package worldgen

import (
	"mc-go/internal/world"
	"testing"
)

func TestSwampHutResidentDescriptorsOnce(t *testing.T) {
	g, err := NewGenerator(Vanilla(), "overworld", "overworld", 12345)
	if err != nil {
		t.Fatal(err)
	}
	tag := map[string]any{"BB": []int32{-2384, 64, -1392, -2378, 70, -1384}, "GD": int32(0), "O": int32(0), "Width": int32(7), "Height": int32(7), "Depth": int32(9), "HPos": int32(64), "Witch": int8(0), "Cat": int8(0)}
	piece, err := pieceLoaders["tesh"](g, tag)
	if err != nil {
		t.Fatal(err)
	}
	chunk := &ProtoChunk{CX: -149, CZ: -87, MinY: -64, Height: 384, Blocks: make([]world.Block, 16*16*384)}
	region := g.NewRegion(chunk, map[[2]int32]*ProtoChunk{{-149, -87}: chunk})
	context := &PieceContext{G: g, Region: region, Random: NewLegacy(0), Chunk: [2]int32{-149, -87}, ChunkBox: BoundingBox{-2368, -64, -1392, -2353, 319, -1377}}
	piece.PostProcess(context)
	if len(chunk.Entities) != 0 {
		t.Fatal("resident emitted outside decorating cell")
	}
	context.ChunkBox = BoundingBox{-2384, -64, -1392, -2369, 319, -1377}
	piece.PostProcess(context)
	if len(chunk.Entities) != 2 {
		t.Fatal("resident count", len(chunk.Entities))
	}
	for i, typ := range []string{"witch", "cat"} {
		entity := chunk.Entities[i]
		if entity.Structure != "swamp_hut" || entity.Type != typ || entity.X != -2381.5 || entity.Y != 66 || entity.Z != -1386.5 || entity.Yaw != 0 {
			t.Fatal("native resident descriptor", entity)
		}
	}
	piece.PostProcess(context)
	if len(chunk.Entities) != 2 {
		t.Fatal("resident duplicated on repeated decoration")
	}
	saved := map[string]any{}
	for key, value := range tag {
		saved[key] = value
	}
	piece.(interface{ SaveData(map[string]any) }).SaveData(saved)
	if !tagBool(saved, "Witch") || !tagBool(saved, "Cat") {
		t.Fatal("resident flags", saved)
	}
	loaded, err := pieceLoaders["tesh"](g, saved)
	if err != nil {
		t.Fatal(err)
	}
	loaded.PostProcess(context)
	if len(chunk.Entities) != 2 {
		t.Fatal("saved resident flags were not respected")
	}
}
