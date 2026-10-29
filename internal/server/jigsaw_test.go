package server

import (
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"testing"
)

func jigsawPacketForTest(name, joint string) []byte {
	var b protocol.Buffer
	b.Position(4, 0, 4)
	b.String(name)
	b.String("example:door")
	b.String("")
	b.String(" literal state ")
	b.String(joint)
	b.VarInt(math.MinInt32)
	b.VarInt(math.MaxInt32)
	return b.Bytes()
}
func TestJigsawEditorDecodeAndDefaults(t *testing.T) {
	data := jigsawPacketForTest("plain", "bad_joint")
	edit, ok := readJigsawEdit(data)
	if !ok || edit.name != "minecraft:plain" || edit.pool != "minecraft:" || edit.target != "example:door" || edit.finalState != " literal state " || edit.joint != "aligned" || edit.selection != math.MinInt32 || edit.placement != math.MaxInt32 {
		t.Fatalf("edit %+v ok%v", edit, ok)
	}
	for i := 0; i < len(data); i++ {
		if _, ok := readJigsawEdit(data[:i]); ok {
			t.Fatalf("accepted truncated packet length%d", i)
		}
	}
	if _, ok := readJigsawEdit(jigsawPacketForTest("..:target", "rollable")); ok {
		t.Fatal("accepted invalid '..' namespace")
	}
	for raw, want := range map[string]string{"": "minecraft:", ":": "minecraft:", "plain": "minecraft:plain", ".:target": ".:target"} {
		if got, ok := jigsawIdentifier(raw); !ok || got != want {
			t.Fatalf("identifier%q got%q,%v", raw, got, ok)
		}
	}
}
func TestJigsawNativeFallbackAndTypes(t *testing.T) {
	horizontal := mkBlockState(t, "jigsaw", map[string]string{"orientation": "north_up"})
	vertical := mkBlockState(t, "jigsaw", map[string]string{"orientation": "up_north"})
	fresh := jigsawDefaults()
	if fresh["joint"] != "rollable" {
		t.Fatal("constructor joint")
	}
	in := map[string]any{"name": "", "target": int32(42), "pool": "UPPER:bad", "joint": "bad", "final_state": int32(42), "selection_priority": float32(3.75), "placement_priority": float64(-2.25), "unknown": int32(1)}
	fields := jigsawSavedFields(horizontal, in)
	if fields["name"] != "minecraft:" || fields["target"] != "minecraft:empty" || fields["pool"] != "minecraft:empty" || fields["joint"] != "aligned" || fields["final_state"] != "minecraft:air" || fields["selection_priority"] != int32(3) || fields["placement_priority"] != int32(-3) {
		t.Fatalf("native fields%#v", fields)
	}
	if _, ok := fields["unknown"]; ok {
		t.Fatal("unknown native field retained")
	}
	if got := jigsawSavedFields(vertical, nil)["joint"]; got != "rollable" {
		t.Fatal("vertical native joint", got)
	}
	if got := jigsawSavedFields(horizontal, nil)["joint"]; got != "aligned" {
		t.Fatal("horizontal native joint", got)
	}
	if jigsawPriority(int64(math.MaxInt64)) != -1 || jigsawPriority(math.Inf(1)) != math.MaxInt32 || jigsawPriority(math.Inf(-1)) != math.MinInt32 || jigsawPriority(math.NaN()) != 0 {
		t.Fatal("native integer narrowing")
	}
}
func TestJigsawEditorPermissionAndTarget(t *testing.T) {
	s := New("unused")
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 4, Y: 0, Z: 4}
	state := mkBlockState(t, "jigsaw", map[string]string{"orientation": "north_up"})
	d.w.SetBlock(pos, state)
	p := placeSession(t, s, "Editor", .5, .5, .5)
	p.setMode(GameModeCreative)
	packet := protocol.Packet{Data: jigsawPacketForTest("example:saved", "rollable")}
	s.handleSetJigsawBlock(p, packet)
	g := s.jigsawAt(d, pos)
	if g == nil || g.fields["name"] != "example:saved" || g.fields["selection_priority"] != int32(math.MinInt32) || g.fields["placement_priority"] != int32(math.MaxInt32) {
		t.Fatalf("packet fields%#v", g)
	}
	p.setMode(GameModeSurvival)
	packet.Data = jigsawPacketForTest("example:denied", "aligned")
	s.handleSetJigsawBlock(p, packet)
	if g.fields["name"] != "example:saved" {
		t.Fatal("survival edit accepted")
	}
	p.setMode(GameModeCreative)
	s.OpsRequired = true
	if p.opLevel() != 0 {
		t.Fatal("non-op fixture")
	}
	s.handleSetJigsawBlock(p, packet)
	if g.fields["name"] != "example:saved" {
		t.Fatal("non-op edit accepted")
	}
	s.OpsRequired = false
	d.w.SetBlock(pos, world.Air)
	delete(d.genericBE, pos)
	s.handleSetJigsawBlock(p, packet)
	if d.genericBE[pos] != nil {
		t.Fatal("missing target created a block entity")
	}
}
func TestJigsawNativePersistenceAndFieldOnlyEdit(t *testing.T) {
	s := nativeSaveTestServer(t)
	pos := world.Pos{X: 4, Y: 0, Z: 4}
	s.runSetblock("P", []string{"4", "0", "4", `jigsaw[orientation=north_up]{name:"native",target:"goal",pool:"empty",selection_priority:3.75f,placement_priority:-2.25d,unknown:1}`})
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	restored := nativeSaveTestServer(t)
	restored.WorldDir = s.WorldDir
	if err := restored.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	fields, ok := restored.blockEntitySaveMap(restored.dims[dimOverworld], pos)
	if !ok || fields["name"] != "minecraft:native" || fields["target"] != "minecraft:goal" || fields["pool"] != "minecraft:empty" || fields["joint"] != "aligned" || fields["selection_priority"] != int32(3) || fields["placement_priority"] != int32(-3) {
		t.Fatalf("native fields%#v", fields)
	}
	if _, ok := fields["unknown"]; ok {
		t.Fatal("unknown field persisted")
	}
	g := restored.jigsawAt(restored.dims[dimOverworld], pos)
	g.fields["name"] = "example:edited"
	if err := restored.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	again := nativeSaveTestServer(t)
	again.WorldDir = s.WorldDir
	if err := again.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	fields, ok = again.blockEntitySaveMap(again.dims[dimOverworld], pos)
	if !ok || fields["name"] != "example:edited" {
		t.Fatalf("field-only edit lost%#v", fields)
	}
}
