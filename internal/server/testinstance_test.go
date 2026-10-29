package server

import (
	"compress/gzip"
	"math"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"reflect"
	"testing"
)

func testInstancePacketForUnit(action, rotation, status int32) []byte {
	var b protocol.Buffer
	b.Position(4, 0, 4)
	b.VarInt(action)
	b.Bool(true)
	b.String("test:saved")
	b.VarInt(3)
	b.VarInt(-2)
	b.VarInt(math.MaxInt32)
	b.VarInt(rotation)
	b.Bool(true)
	b.VarInt(status)
	b.Bool(true)
	b.Raw(componentBytes(" λ error "))
	return b.Bytes()
}
func TestTestInstancePacketCodecBoundaries(t *testing.T) {
	raw := testInstancePacketForUnit(-1, -1, 99)
	edit, ok := readTestInstanceEdit(raw)
	if !ok || edit.action != 0 || edit.data.rotation != 3 || edit.data.status != 0 || edit.data.test != "test:saved" || edit.data.size != ([3]int32{3, -2, math.MaxInt32}) || edit.data.errorMessage != " λ error " {
		t.Fatal("packet", edit, ok)
	}
	for i := 0; i < len(raw); i++ {
		if _, ok := readTestInstanceEdit(raw[:i]); ok {
			t.Fatal("truncated packet", i)
		}
	}
}
func TestTestInstanceNativeRecordRetainsOnInvalid(t *testing.T) {
	initial := testInstanceData{test: "test:saved", size: [3]int32{3, 2, 4}, rotation: 1, status: 2, ignoreEntities: true, errorMessage: " λ "}
	current := &genericBlockEntity{id: testInstanceBlockEntityID, fields: map[string]any{"data": initial.native()}}
	in := initial.native()
	in["rotation"] = "BAD"
	got := testInstanceSavedFields(map[string]any{"data": in, "unknown": int32(1)}, current)
	record, ok := testInstanceDataFromNative(got["data"])
	if !ok || record.test != initial.test || record.rotation != 1 || record.errorMessage != " λ " {
		t.Fatal("invalid load discarded current record", record, ok)
	}
	if _, ok := got["unknown"]; ok {
		t.Fatal("unknown top-level field retained")
	}
	in = initial.native()
	in["size"] = nbt.List{Elem: nbt.TagFloat, Items: []any{float32(1.9), float32(-.9), float32(3.9)}}
	in["ignore_entities"] = int32(256)
	record, ok = testInstanceDataFromNative(in)
	if !ok || record.size != ([3]int32{1, 0, 3}) || !record.ignoreEntities {
		t.Fatal("native numeric codecs", record, ok)
	}
	for value, want := range map[float64]int32{math.Inf(1): math.MaxInt32, math.Inf(-1): math.MinInt32, 1.9: 1, -.9: 0} {
		if got := testInstanceFloatInt(value); got != want {
			t.Fatal("codec integer", value, got, want)
		}
	}
	if testInstanceFloatInt(math.NaN()) != 0 {
		t.Fatal("NaN codec integer")
	}
	if b, ok := testInstanceCodecBool(math.NaN()); !ok || !b {
		t.Fatal("NaN numeric bool", b, ok)
	}
}
func TestTestInstanceSaveIgnoresAirAndWritesNative(t *testing.T) {
	s := New("unused")
	s.WorldDir = t.TempDir()
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 4, Y: 0, Z: 4}
	gold, _ := world.BlockState("gold_block")
	d.w.SetBlock(world.Pos{X: 4, Y: 1, Z: 5}, gold)
	d.w.SetBlock(world.Pos{X: 5, Y: 1, Z: 6}, world.Stone)
	data := testInstanceData{test: "test:saved", size: [3]int32{2, 1, 2}, ignoreEntities: true}
	if name := s.testInstanceSave(d, pos, data); name != data.test {
		t.Fatal("save name", name)
	}
	native := s.structureTemplates[data.test]
	if len(native["palette"].(nbt.List).Items) != 2 || len(native["blocks"].(nbt.List).Items) != 2 {
		t.Fatal("capture retained air", native)
	}
	f, err := s.structureTemplateFile(data.test, false)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	_, value, err := nbt.ReadNamed(zr)
	if err != nil {
		t.Fatal(err)
	}
	disk := value.(map[string]any)
	if _, ok := disk["author"]; ok {
		t.Fatal("disk retained repository-only author")
	}
	if len(disk["blocks"].(nbt.List).Items) != 2 {
		t.Fatal("saved native blocks", disk)
	}
}
func TestTestInstanceResetBarrierBoundary(t *testing.T) {
	s := New("unused")
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 4, Y: 0, Z: 4}
	barrier, _ := world.BlockState("barrier")
	outside := world.Pos{X: 3, Y: 0, Z: 4}
	inside := world.Pos{X: 4, Y: 1, Z: 5}
	d.w.SetBlock(outside, barrier)
	d.w.SetBlock(inside, barrier)
	s.testInstanceRemoveBarriers(d, pos, testInstanceData{size: [3]int32{2, 1, 3}})
	if d.w.GetBlock(outside) != world.Air || d.w.GetBlock(inside) != barrier {
		t.Fatal("barrier boundary")
	}
}
func TestTestInstanceNativePersistence(t *testing.T) {
	s := nativeSaveTestServer(t)
	pos := world.Pos{X: 4, Y: 0, Z: 4}
	s.runSetblock("P", []string{"4", "0", "4", `test_instance_block{data:{test:"test:saved",size:[I;3,2,4],rotation:"clockwise_90",ignore_entities:1b,status:"finished",error_message:" λ "},errors:[{pos:[I;7,0,8],text:"marker λ",unknown:1}],unknown:1}`})
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	restored := nativeSaveTestServer(t)
	restored.WorldDir = s.WorldDir
	if err := restored.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	fields, ok := restored.blockEntitySaveMap(restored.dims[dimOverworld], pos)
	if !ok {
		t.Fatal("controller missing")
	}
	data, ok := testInstanceDataFromNative(fields["data"])
	if !ok || data.test != "test:saved" || data.size != ([3]int32{3, 2, 4}) || data.rotation != 1 || data.status != 2 || data.errorMessage != " λ " {
		t.Fatal("native restore", data, ok)
	}
	if _, ok := fields["unknown"]; ok {
		t.Fatal("unknown persisted")
	}
	markers, ok := fields["errors"].(nbt.List)
	if !ok || len(markers.Items) != 1 {
		t.Fatal("native error markers", fields)
	}
	marker := markers.Items[0].(map[string]any)
	if marker["text"] != "marker λ" || len(marker) != 2 {
		t.Fatal("marker codec", marker)
	}
	// A malformed record load keeps the live record as a single codec unit.
	save := cloneNBTValue(fields).(map[string]any)
	save["data"].(map[string]any)["rotation"] = "BAD"
	restored.restoreBlockEntity(restored.dims[dimOverworld], save)
	now, _ := restored.blockEntitySaveMap(restored.dims[dimOverworld], pos)
	if !reflect.DeepEqual(now["data"], fields["data"]) {
		t.Fatal("invalid native load changed record", now, fields)
	}
}

func TestTestInstanceEditorPermissionsAndTarget(t *testing.T) {
	s := New("unused")
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 4, Y: 0, Z: 4}
	block, _ := world.BlockState("test_instance_block")
	d.w.SetBlock(pos, block)
	sess := placeSession(t, s, "TestInst", .5, .5, .5)
	sess.setMode(GameModeSurvival)
	packet := protocol.Packet{Data: testInstancePacketForUnit(2, 1, 2)}
	s.handleTestInstanceBlockAction(sess, packet)
	g := s.testInstanceAt(d, pos)
	data, _ := testInstanceDataFromNative(g.fields["data"])
	if data.status != 0 {
		t.Fatal("survival changed controller")
	}
	sess.setMode(GameModeCreative)
	s.handleTestInstanceBlockAction(sess, packet)
	data, _ = testInstanceDataFromNative(g.fields["data"])
	if data.status != 2 || data.rotation != 1 {
		t.Fatal("creative SET missing", data)
	}
	s.OpsRequired = true
	s.handleTestInstanceBlockAction(sess, protocol.Packet{Data: testInstancePacketForUnit(2, 0, 0)})
	data, _ = testInstanceDataFromNative(g.fields["data"])
	if data.status != 2 {
		t.Fatal("non-op changed controller")
	}
	s.OpsRequired = false
	s.handleTestInstanceBlockAction(sess, protocol.Packet{Data: packet.Data[:2]})
	data, _ = testInstanceDataFromNative(g.fields["data"])
	if data.status != 2 {
		t.Fatal("malformed packet changed controller")
	}
	d.w.SetBlock(pos, world.Air)
	delete(d.genericBE, pos)
	s.handleTestInstanceBlockAction(sess, packet)
	if d.genericBE[pos] != nil {
		t.Fatal("wrong block created controller")
	}
}
