package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestJukeboxNativeDiskPersistence(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 1, Y: 0, Z: 1}
	s.runSetblock("P", []string{"1", "0", "1", "jukebox[has_record=true]"})
	// Native insertion has no generic RecordItem tag to fall back on.
	d.jukeboxes = map[world.Pos]*jukeboxEnt{pos: {disc: ItemStack{ID: world.ItemIDs["music_disc_cat"], Count: 1}, song: "cat", songID: jukeboxSongID["cat"]}}
	entries := s.chunkClientBlockEntities(d, 0, 0)
	count := 0
	for _, entry := range entries {
		if entry.pos == pos {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("jukebox client entries=%d want1", count)
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	rd := restarted.dims[dimOverworld]
	jb := rd.jukeboxes[pos]
	if jb == nil || jb.disc.ID != world.ItemIDs["music_disc_cat"] || jb.disc.Count != 1 || jb.playing {
		t.Fatalf("native record after reload: %+v", jb)
	}
	fields, ok := restarted.blockEntitySaveMap(rd, pos)
	record, _ := fields["RecordItem"].(map[string]any)
	if !ok || record["id"] != "minecraft:music_disc_cat" || record["count"] != int32(1) {
		t.Fatalf("saved record=%#v", fields)
	}
	if _, ok := fields["ticks_since_song_started"]; ok {
		t.Fatal("stopped record gained playback clock")
	}
	restarted.restoreBlockEntity(rd, map[string]any{"id": "minecraft:jukebox", "x": int32(1), "y": int32(0), "z": int32(1)})
	if rd.jukeboxes[pos] != nil {
		t.Fatal("removed RecordItem retained native disc")
	}
	fields, _ = restarted.blockEntitySaveMap(rd, pos)
	if _, ok := fields["RecordItem"]; ok {
		t.Fatal("removed RecordItem retained saved disc")
	}
}
