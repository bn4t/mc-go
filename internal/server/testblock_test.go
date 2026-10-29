package server

import (
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"testing"
)

func testBlockPacket(mode int32, message string) []byte {
	var b protocol.Buffer
	b.Position(4, 0, 4)
	b.VarInt(mode)
	b.String(message)
	return b.Bytes()
}
func TestTestBlockDecodeAndNativeDefaults(t *testing.T) {
	data := testBlockPacket(-1, "  λ raw:[]  ")
	edit, ok := readTestBlockEdit(data)
	if !ok || edit.mode != "start" || edit.message != "  λ raw:[]  " || edit.pos != (world.Pos{X: 4, Y: 0, Z: 4}) {
		t.Fatal("packet", edit, ok)
	}
	for n := 0; n < len(data); n++ {
		if _, ok := readTestBlockEdit(data[:n]); ok {
			t.Fatal("truncated packet accepted", n)
		}
	}
	fields := testBlockSavedFields(map[string]any{"mode": "START", "message": int32(42), "powered": "bad", "unknown": int32(1)})
	if fields["mode"] != "fail" || fields["message"] != "" || fields["powered"] != int8(0) || len(fields) != 3 {
		t.Fatal("native defaults", fields)
	}
}
func TestTestBlockEditorStateAndPermission(t *testing.T) {
	s := New("unused")
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 4, Y: 0, Z: 4}
	state := mkBlockState(t, "test_block", map[string]string{"mode": "log"})
	d.w.SetBlock(pos, state)
	g := s.testBlockAt(d, pos)
	if g == nil || g.fields["mode"] != "log" {
		t.Fatal("constructor mode", g)
	}
	g.fields["powered"] = int8(1)
	sess := placeSession(t, s, "TestEditor", .5, .5, .5)
	sess.setMode(GameModeCreative)
	s.handleSetTestBlock(sess, protocol.Packet{Data: testBlockPacket(3, " edited λ ")})
	mode, _ := world.StateProperty(d.w.GetBlock(pos), "mode")
	if mode != "accept" || g.fields["mode"] != "accept" || g.fields["message"] != " edited λ " || g.fields["powered"] != int8(1) {
		t.Fatal("editor mutation", mode, g.fields)
	}
	sess.setMode(GameModeSurvival)
	s.handleSetTestBlock(sess, protocol.Packet{Data: testBlockPacket(0, "denied")})
	if g.fields["mode"] != "accept" {
		t.Fatal("survival edit accepted")
	}
	sess.setMode(GameModeCreative)
	s.OpsRequired = true
	s.handleSetTestBlock(sess, protocol.Packet{Data: testBlockPacket(0, "denied")})
	if g.fields["mode"] != "accept" {
		t.Fatal("non-op edit accepted")
	}
	s.OpsRequired = false
	d.w.SetBlock(pos, world.Air)
	delete(d.genericBE, pos)
	s.handleSetTestBlock(sess, protocol.Packet{Data: testBlockPacket(0, "wrong target")})
	if d.genericBE[pos] != nil {
		t.Fatal("missing target created BE")
	}
}
func TestTestBlockNativePersistence(t *testing.T) {
	s := nativeSaveTestServer(t)
	pos := world.Pos{X: 4, Y: 0, Z: 4}
	s.runSetblock("P", []string{"4", "0", "4", `test_block[mode=log]{mode:"accept",message:"saved λ",powered:1b,unknown:1}`})
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	restored := nativeSaveTestServer(t)
	restored.WorldDir = s.WorldDir
	if err := restored.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	d := restored.dims[dimOverworld]
	mode, _ := world.StateProperty(d.w.GetBlock(pos), "mode")
	fields, ok := restored.blockEntitySaveMap(d, pos)
	if !ok || mode != "log" || fields["mode"] != "accept" || fields["message"] != "saved λ" || fields["powered"] != int8(1) {
		t.Fatal("native restore", mode, fields, ok)
	}
	if _, ok := fields["unknown"]; ok {
		t.Fatal("unknown native field retained")
	}
	g := restored.testBlockAt(d, pos)
	g.fields["message"] = "saved again"
	if err := restored.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	again := nativeSaveTestServer(t)
	again.WorldDir = s.WorldDir
	if err := again.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	fields, ok = again.blockEntitySaveMap(again.dims[dimOverworld], pos)
	if !ok || fields["message"] != "saved again" {
		t.Fatal("field-only persistence", fields, ok)
	}
}
