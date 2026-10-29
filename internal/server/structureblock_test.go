package server

import (
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"testing"
)

func TestStructureBlockNativeFieldsAndScan(t *testing.T) {
	s := simulationTestServer(t)
	d := s.dims[dimOverworld]
	p := world.Pos{X: 4, Y: -60, Z: 4}
	s.simulationAction(func() {
		state, _ := world.BlockStateOf("structure_block", map[string]string{"mode": "save"})
		s.setSilentBlock(d, p, state)
		g := s.structureBlockAt(d, p)
		if g.fields["mode"] != "SAVE" || g.fields["posY"] != int32(1) || g.fields["showboundingbox"] != int8(1) {
			t.Fatalf("fresh%+v", g.fields)
		}
		native := structureBlockSavedFields(map[string]any{"name": "native", "posX": int32(100), "sizeY": int32(-7), "integrity": float32(2), "mode": "invalid"})
		if native["name"] != "minecraft:native" || native["posX"] != int32(48) || native["sizeY"] != int32(0) || native["integrity"] != float32(2) || native["mode"] != "DATA" {
			t.Fatalf("native%+v", native)
		}
		g.fields["name"] = "minecraft:scan"
		corner := world.Pos{X: 9, Y: -55, Z: 10}
		s.setSilentBlock(d, corner, state)
		c := s.structureBlockAt(d, corner)
		c.fields["mode"], c.fields["name"] = "CORNER", "minecraft:scan"
		if !s.structureBlockScan(d, p, g) || g.fields["sizeX"] != int32(4) || g.fields["sizeY"] != int32(4) || g.fields["sizeZ"] != int32(5) {
			t.Fatalf("single marker%+v", g.fields)
		}
		c.fields["name"] = "minecraft:other"
		if s.structureBlockScan(d, p, g) {
			t.Fatal("nonmatching corner accepted")
		}
	})
}
func TestStructureBlockPacketClampsAndUTF16Limit(t *testing.T) {
	encode := func(data string) []byte {
		var b protocol.Buffer
		b.Position(4, -60, 4)
		b.VarInt(0)
		b.VarInt(2)
		b.String("demo")
		for _, v := range []byte{127, 128, 49, 127, 128, 49} {
			b.Byte(v)
		}
		b.VarInt(2)
		b.VarInt(3)
		b.String(data)
		b.Float(2)
		b.VarLong(-123456789012345)
		b.Byte(15)
		return b.Bytes()
	}
	e, ok := readStructureBlockEdit(encode("metadata"))
	if !ok || e.offset != [3]int32{48, -48, 48} || e.size != [3]int32{48, 0, 48} || e.seed != -123456789012345 || e.integrity != 1 || e.flags != 15 {
		t.Fatalf("packet%+v valid%v", e, ok)
	}
	text := ""
	for range 65 {
		text += "😀"
	}
	if _, ok := readStructureBlockEdit(encode(text)); ok {
		t.Fatal("metadata exceeding128 UTF16 units accepted")
	}
}
