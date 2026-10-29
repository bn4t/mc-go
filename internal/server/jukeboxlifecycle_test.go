package server

import (
	"mc-go/internal/world"
	"reflect"
	"testing"
)

func TestJukeboxGenericHydrationAndRetirement(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	d := s.dims[dimOverworld]
	d.genericBE = map[world.Pos]*genericBlockEntity{}
	pos := world.Pos{X: 1, Y: 0, Z: 1}
	s.runSetblock("P", []string{"1", "0", "1", "jukebox[has_record=true]"})
	fields := map[string]any{
		"components":               map[string]any{"minecraft:custom_name": "Box"},
		"RecordItem":               map[string]any{"id": "minecraft:music_disc_cat", "count": int32(2), "components": map[string]any{"minecraft:custom_name": "Disc", "minecraft:repair_cost": int32(3), "minecraft:enchantments": map[string]any{"minecraft:unbreaking": int32(2)}}},
		"ticks_since_song_started": int64(7),
	}
	d.genericBE[pos] = &genericBlockEntity{id: "minecraft:jukebox", fields: cloneNBTValue(fields).(map[string]any)}
	jb := s.jukeboxAt(d, pos)
	if jb == nil || jb.disc.Count != 2 || !jb.playing || d.genericBE[pos] != nil {
		t.Fatalf("hydrate=%+v generic=%v", jb, d.genericBE[pos])
	}
	saved := map[string]any{}
	s.jukeboxSaveFields(d, pos, saved)
	if !reflect.DeepEqual(fields, saved) {
		t.Fatalf("fields=%#v want=%#v", saved, fields)
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	restored := nativeSaveTestServer(t)
	restored.WorldDir = s.WorldDir
	if err := restored.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	rfields := map[string]any{}
	restored.jukeboxSaveFields(restored.dims[dimOverworld], pos, rfields)
	if !reflect.DeepEqual(fields, rfields) {
		t.Fatalf("reload=%#v want=%#v", rfields, fields)
	}
	s.runSetblock("P", []string{"1", "0", "1", "stone"})
	if s.jukeboxAt(d, pos) != nil {
		t.Fatal("command replacement retained record")
	}
	for _, e := range s.entities.All() {
		if e.Item.ID == world.ItemIDs["music_disc_cat"] {
			t.Fatal("command replacement dropped record despite skip-sideeffects")
		}
	}
	s.runSetblock("P", []string{"1", "0", "1", "jukebox[has_record=true]"})
	d.genericBE[pos] = &genericBlockEntity{id: "minecraft:jukebox", fields: cloneNBTValue(fields).(map[string]any)}
	s.jukeboxPopRecord(d, pos)
	if jb := s.jukeboxAt(d, pos); jb == nil || !jb.disc.Empty() || jb.components["minecraft:custom_name"] != "Box" {
		t.Fatalf("eject=%+v", jb)
	}
	var count int
	for _, e := range s.entities.All() {
		if e.Item.ID == world.ItemIDs["music_disc_cat"] {
			count++
			if e.Item.Count != 2 || e.PickupDelay != 10 || e.Y != 1.01 {
				t.Fatalf("drop=%+v", e)
			}
		}
	}
	if count != 1 {
		t.Fatalf("drops=%d", count)
	}
}

func TestJukeboxCommandTagHydration(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	s.runSetblock("P", []string{"1", "0", "1", `jukebox[has_record=true]{components:{"minecraft:custom_name":"Box"},RecordItem:{id:"minecraft:music_disc_cat",count:2,components:{"minecraft:custom_name":"Disc","minecraft:enchantments":{"minecraft:unbreaking":2},"minecraft:repair_cost":3}},ticks_since_song_started:7L}`})
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 1, Y: 0, Z: 1}
	jb := s.jukeboxAt(d, pos)
	if jb == nil || jb.disc.Count != 2 || jb.disc.RepairCost != 3 || jb.disc.Name != "Disc" || jb.components["minecraft:custom_name"] != "Box" || !jb.playing {
		t.Fatalf("command hydration=%+v", jb)
	}
}

func TestJukeboxTypedRestoreRetiresGeneric(t *testing.T) {
	s := nativeSaveTestServer(t)
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 1, Y: 0, Z: 1}
	d.genericBE = map[world.Pos]*genericBlockEntity{pos: {id: "minecraft:jukebox", fields: map[string]any{"RecordItem": map[string]any{"id": "minecraft:music_disc_cat", "count": int32(2)}}}}
	s.restoreJukebox(d, pos, map[string]any{"RecordItem": map[string]any{"id": "minecraft:music_disc_13", "count": int32(1)}})
	if d.genericBE[pos] != nil {
		t.Fatal("typed restore retained stale generic source")
	}
	if jb := s.jukeboxAt(d, pos); jb == nil || jb.disc.ID != world.ItemIDs["music_disc_13"] {
		t.Fatalf("typed record=%+v", jb)
	}
	s.jukeboxPopRecord(d, pos)
	if jb := s.jukeboxAt(d, pos); jb != nil {
		t.Fatalf("ejected record resurrected from legacy entry: %+v", jb)
	}
}
