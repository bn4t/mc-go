package server

import (
	"bytes"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"testing"
)

func TestPotionContentsWireOptionals(t *testing.T) {
	tag, _, e := parseSNBTTag("{custom_color:1193046,custom_name:'probe',custom_effects:[{id:'minecraft:speed',duration:800,amplifier:2,ambient:true,show_particles:false,show_icon:true,hidden_effect:{duration:100}}]}", 0)
	if e != nil {
		t.Fatal(e)
	}
	var item ItemStack
	if e := setPotionContents(&item, snbtToValue(tag)); e != nil {
		t.Fatal(e)
	}
	var b protocol.Buffer
	writePotionContents(&b, item)
	// Optional holder absent; custom color uses big-endian fixed32. The
	// custom name is UTF8 String; hidden effect details omit an effect ID.
	want := []byte{0, 1, 0, 0x12, 0x34, 0x56, 1, 0, 2, 0xa0, 6, 1, 0, 1, 1, 0, 100, 0, 1, 1, 0, 1, 5, 'p', 'r', 'o', 'b', 'e'}
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("wire %x want%x", b.Bytes(), want)
	}
	var got ItemStack
	if e := readPotionContents(protocol.NewPacketReader(want), &got); e != nil {
		t.Fatal(e)
	}
	if got.PotionExtra != item.PotionExtra {
		t.Fatalf("payload changed: %s !=%s", got.PotionExtra, item.PotionExtra)
	}
	effects := potionContentsValue(item)["custom_effects"].(nbt.List)
	if effects.Items[0].(map[string]any)["hidden_effect"].(map[string]any)["show_icon"] != int8(1) {
		t.Fatal("native hidden icon default omitted")
	}
	item.PotionScaleSet, item.PotionScale = true, .5
	restored := itemFromNBT(itemNBT(item))
	if restored.PotionExtra != item.PotionExtra || !restored.PotionScaleSet || restored.PotionScale != .5 {
		t.Fatalf("native potion payload changed: %+v", restored)
	}
}
func TestPotionDurationScaleRounding(t *testing.T) {
	for _, tc := range []struct {
		duration int32
		scale    float32
		want     int32
	}{{101, .5, 50}, {2147483647, 2, 2147483647}, {1, 0, 1}, {0, 2, 0}, {-1, 0, -1}} {
		if got := scaledPotionTicks(tc.duration, tc.scale); got != tc.want {
			t.Errorf("%d*%g=%d want%d", tc.duration, tc.scale, got, tc.want)
		}
	}
	var it ItemStack
	if !setPotionScale(&it, float32(0)) || !it.PotionScaleSet || potionDurationScale(it) != 0 {
		t.Fatal("explicit zero scale lost")
	}
}

func TestPotionCloudDuplicateDoses(t *testing.T) {
	tag, _, e := parseSNBTTag("{potion:'minecraft:strong_swiftness',custom_effects:[{id:'minecraft:speed',duration:800,hidden_effect:{duration:100}}]}", 0)
	if e != nil {
		t.Fatal(e)
	}
	it := ItemStack{ID: world.ItemIDs["lingering_potion"], Count: 1}
	if e := setPotionContents(&it, snbtToValue(tag)); e != nil {
		t.Fatal(e)
	}
	doses := potionCloudDoses(&Entity{Item: it, CloudPotionContents: it.Potion}, 0)
	if len(doses) != 2 || doses[0].id != doses[1].id || doses[0].amplifier != 1 || doses[1].amplifier != 0 || doses[0].ticks != 450 || doses[1].ticks != 200 {
		t.Fatalf("ordered duplicate doses %+v", doses)
	}
}

func TestCustomInstantPotionEvent(t *testing.T) {
	tag, _, e := parseSNBTTag("{custom_effects:[{id:'minecraft:instant_health'}]}", 0)
	if e != nil {
		t.Fatal(e)
	}
	var item ItemStack
	if e := setPotionContents(&item, snbtToValue(tag)); e != nil {
		t.Fatal(e)
	}
	if got := potionStackEvent(item); got != 2002 {
		t.Fatalf("custom instant event %d want2002", got)
	}
	item.Potion = "healing"
	if got := potionStackEvent(item); got != 2007 {
		t.Fatalf("base instant event%d want2007", got)
	}
}

func TestSummonedCloudPotionDurationScale(t *testing.T) {
	tag, _, err := parseSNBTTag("{custom_effects:[{id:'minecraft:speed',duration:200,hidden_effect:{duration:100}}]}", 0)
	if err != nil {
		t.Fatal(err)
	}
	var cloud Entity
	setCloudPotionContents(&cloud, snbtToValue(tag), 1, 0)
	doses := potionCloudDoses(&cloud, 80)
	if len(doses) != 1 || doses[0].ticks != 200 {
		t.Fatalf("summoned cloud dose %+v want immutable200", doses)
	}
}

func TestPotionActiveEffectFlagsNBT(t *testing.T) {
	if _, ok := mobEffectNBT(effectSpeed, mobEffectInst{expiry: 0}, 0)["duration"]; ok {
		t.Fatal("zero duration must encode as omitted codec default")
	}
	hidden := &mobEffectInst{amplifier: 0, expiry: 100, flags: 6, flagsSet: true}
	m := mobEffectNBT(effectSpeed, mobEffectInst{amplifier: 2, expiry: 200, flags: 1, flagsSet: true, hidden: hidden}, 0)
	if m["ambient"] != int8(1) || m["show_particles"] != int8(0) || m["show_icon"] != int8(0) {
		t.Fatalf("custom effect scalar flags lost: %+v", m)
	}
	if child, ok := m["hidden_effect"].(map[string]any); !ok || child["id"] != nil {
		t.Fatalf("hidden details must not repeat id: %+v", m)
	}
}

func TestPotionWeakerFlagsMerge(t *testing.T) {
	inst := mobEffectInst{amplifier: 2, expiry: 450, flags: 14, flagsSet: true, hidden: &mobEffectInst{amplifier: 0, expiry: 800}}
	if !potionMergeFlags(&inst, 9, false, 0, 800, 0) {
		t.Fatal("weaker visibility change lost")
	}
	if inst.amplifier != 2 || inst.expiry != 450 || inst.flags != 0 || !inst.hide || inst.hidden.flags != 9 || !inst.hidden.flagsSet {
		t.Fatalf("scalar/hidden flag merge %+v hidden%+v", inst, inst.hidden)
	}
	inst.flags = 15
	if !potionMergeFlags(&inst, 8, false, 0, 800, 0) || inst.flags != 0 {
		t.Fatal("declined dose did not clear ambient")
	}
}

func TestPotionCustomColorInt32(t *testing.T) {
	for _, color := range []int32{-2147483648, -1, 0, 16777216, 2147483647} {
		var it ItemStack
		if err := setPotionContents(&it, map[string]any{"custom_color": color}); err != nil {
			t.Fatalf("Codec.INT color%d rejected: %v", color, err)
		}
		var wire protocol.Buffer
		writePotionContents(&wire, it)
		r := protocol.NewPacketReader(wire.Bytes())
		_, _ = r.Byte()
		_, _ = r.Byte()
		got, err := r.Int()
		if err != nil || got != color {
			t.Fatalf("color%d wire%d err%v", color, got, err)
		}
	}
}
func TestNativeLiveEffectHiddenRoundTrip(t *testing.T) {
	original := New("unused")
	original.WorldDir = t.TempDir()
	original.clock.count = 40
	original.clock.base = 10000
	d := original.dims["minecraft:overworld"]
	entity, ok := original.summonMobRaw(d, "villager", 2, -60, 2, false, false)
	if !ok {
		t.Fatal("villager spawn")
	}
	entity.NoAI = true
	entity.NoGravity = true
	entity.Effects = map[int32]mobEffectInst{effectSpeed: {amplifier: 255, expiry: 80, born: 40, flags: 15, flagsSet: true, hidden: &mobEffectInst{amplifier: 128, expiry: 240, born: 40, flags: 9, flagsSet: true, hide: true}}}
	if err := original.saveEntityRegions(d); err != nil {
		t.Fatal(err)
	}
	restored := New("unused")
	restored.WorldDir = original.WorldDir
	restored.clock.count = 900
	restored.clock.base = 20000
	restored.restoreEntities(restored.dims[d.name])
	entities := restored.entities.All()
	if len(entities) != 1 {
		t.Fatalf("restored entities%d", len(entities))
	}
	loaded, ok := restored.entities.GetPtr(entities[0].ID)
	if !ok {
		t.Fatal("restored entity absent")
	}
	effect, ok := loaded.Effects[effectSpeed]
	if !ok || effect.amplifier != 255 || effect.expiry-900 != 40 || effect.flags != 15 || effect.hidden == nil || effect.hidden.amplifier != 128 || effect.hidden.expiry-900 != 200 || effect.hidden.flags != 9 || !effect.hidden.hide {
		t.Fatalf("native live/hidden state lost: %+v hidden%+v", effect, effect.hidden)
	}
	for i := 0; i < 41; i++ {
		restored.GameTick()
	}
	effect = loaded.Effects[effectSpeed]
	if effect.amplifier != 128 || effect.expiry-restored.clock.count != 159 || effect.flags != 9 {
		t.Fatalf("restored hidden did not resume: %+v now%d", effect, restored.clock.count)
	}
	saved := entitySaveMap(loaded, restored.clock.count, restored.entities).m["active_effects"].(nbt.List)
	if saved.Items[0].(map[string]any)["duration"] != int32(159) {
		t.Fatalf("stale ExtraNBT shadowed live status save: %+v", saved)
	}
}

func TestPotionDefaultAndExplicitEffectFlags(t *testing.T) {
	s := simulationTestServer(t)
	viewer := entityViewer(t, s, 776, "OrdinaryFlags")
	for _, tc := range []struct {
		hide  bool
		flags byte
		clear bool
	}{{false, 14, false}, {true, 0, false}, {false, 6, false}, {true, 8, true}} {
		s.simulationAction(func() {
			if tc.clear {
				s.clearMobEffects(viewer)
			}
			s.applyMobEffectFlags(viewer, effectSpeed, 0, 200, tc.hide)
		})
		body := entityPacketOf(t, entityPackets(t, viewer), viewer.ids().PlayUpdateMobEffect)
		r := protocol.NewPacketReader(body)
		for range 4 {
			_, _ = r.VarInt()
		}
		flags, _ := r.Byte()
		if flags != tc.flags {
			t.Fatalf("ordinary hide%v flags%d want%d", tc.hide, flags, tc.flags)
		}
	}
	s.simulationAction(func() { s.applyMobEffectWire(viewer, effectSpeed, 0, 200, true, 0) })
	body := entityPacketOf(t, entityPackets(t, viewer), viewer.ids().PlayUpdateMobEffect)
	r := protocol.NewPacketReader(body)
	for range 4 {
		_, _ = r.VarInt()
	}
	if flags, _ := r.Byte(); flags != 0 {
		t.Fatalf("explicit zero flags lost: %d", flags)
	}
}
