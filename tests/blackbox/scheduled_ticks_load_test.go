//go:build blackbox

package blackbox

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

func writeLoaderRegionBB(t *testing.T, path string, chunks map[[2]int32]map[string]any) {
	t.Helper()
	// Region format: two header sectors, then length/compression-prefixed chunks.
	keys := make([][2]int32, 0, len(chunks))
	for k := range chunks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	out := make([]byte, 8192)
	for _, key := range keys {
		if key[0] < 0 || key[0] > 31 || key[1] < 0 || key[1] > 31 {
			t.Fatal("invalid local chunk", key)
		}
		var raw, compressed bytes.Buffer
		if err := nbt.WriteNamed(&raw, "", chunks[key]); err != nil {
			t.Fatal(err)
		}
		zw := zlib.NewWriter(&compressed)
		if _, err := zw.Write(raw.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		size := compressed.Len() + 5
		sectors := (size + 4095) / 4096
		if sectors > 255 {
			t.Fatal("fixture chunk too large")
		}
		offset := len(out) / 4096
		slot := int(key[0] + key[1]*32)
		binary.BigEndian.PutUint32(out[slot*4:], uint32(offset<<8|sectors))
		data := make([]byte, sectors*4096)
		binary.BigEndian.PutUint32(data, uint32(compressed.Len()+1))
		data[4] = 2
		copy(data[5:], compressed.Bytes())
		out = append(out, data...)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatal(err)
	}
}

func loaderFixtureRecordsBB() nbt.List {
	row := func(name string, x, y, z, delay, priority any) map[string]any {
		return map[string]any{"i": name, "x": x, "y": y, "z": z, "t": delay, "p": priority}
	}
	valid := row("stone_button", int32(8), int32(-60), int32(4), int32(1000), int32(0))
	duplicate := row("minecraft:stone_button", int32(8), int32(-60), int32(4), int32(1001), int32(-99))
	missing := row("minecraft:stone_button", int32(9), int32(-60), int32(4), int32(1000), int32(0))
	delete(missing, "p")
	return nbt.List{Elem: nbt.TagCompound, Items: []any{
		valid, missing, duplicate,
		row("minecraft:not_a_block", int32(10), int32(-60), int32(4), int32(1000), int32(0)),
		row("minecraft:stone_button", int32(24), int32(-60), int32(4), int32(1000), int32(0)),
		row("minecraft:stone_button", float64(11.75), int16(-60), int32(4), float64(1200.75), float64(99.75)),
		row("minecraft:stone_button", int32(12), int32(1000), int32(4), int32(1300), int32(0)),
		row("minecraft:stone_button", int32(13), int32(-60), int32(4), "1000", int32(0)),
		row("minecraft:stone_button", int32(4), int32(-60), int32(4), int32(1400), int32(0)),
	}}
}

func awaitLoadedTickRecordsBB(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last any
	for time.Now().Before(deadline) {
		chunks, err := world.ReadRegionFile(path)
		if err != nil {
			last = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		list, _ := chunks[[2]int32{}]["block_ticks"].(nbt.List)
		last = list
		if len(list.Items) != 5 {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var buttons []world.SavedTick
		for _, v := range list.Items {
			row, _ := v.(map[string]any)
			if row["i"] != "minecraft:stone_button" {
				continue
			}
			x, xok := row["x"].(int32)
			y, yok := row["y"].(int32)
			z, zok := row["z"].(int32)
			delay, tok := row["t"].(int32)
			priority, pok := row["p"].(int32)
			if !xok || !yok || !zok || !tok || !pok {
				t.Fatalf("saved codec types %#v", row)
			}
			buttons = append(buttons, world.SavedTick{Type: "minecraft:stone_button", Pos: world.Pos{X: int(x), Y: int(y), Z: int(z)}, Delay: delay, Priority: priority})
		}
		if len(buttons) != 5 {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		remaining := int32(-1)
		for _, record := range buttons {
			if record.Pos == (world.Pos{X: 8, Y: -60, Z: 4}) && record.Priority == 0 {
				remaining = record.Delay
			}
		}
		if remaining <= 1 || remaining >= 1000 {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		want := map[world.Pos]map[int32]int32{
			{X: 8, Y: -60, Z: 4}:   {0: remaining, -3: remaining + 1},
			{X: 11, Y: -60, Z: 4}:  {3: remaining + 200},
			{X: 12, Y: 1000, Z: 4}: {0: remaining + 300},
			{X: 4, Y: -60, Z: 4}:   {0: remaining + 400},
		}
		for _, record := range buttons {
			value, ok := want[record.Pos][record.Priority]
			if !ok || value != record.Delay {
				t.Fatalf("loaded native metadata %#v, want %#v", buttons, want)
			}
			delete(want[record.Pos], record.Priority)
		}
		for pos, priorities := range want {
			if len(priorities) != 0 {
				t.Fatalf("loaded native record missing at %#v: %#v", pos, priorities)
			}
		}
		return int(remaining)
	}
	t.Fatalf("loaded partial/duplicate/foreign records did not become fresh canonical save: %#v", last)
	return 0
}

func awaitClonedLoadedTickBB(t *testing.T, path string, remaining int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last any
	for time.Now().Before(deadline) {
		chunks, err := world.ReadRegionFile(path)
		if err != nil {
			last = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		list, _ := chunks[[2]int32{1, 0}]["block_ticks"].(nbt.List)
		var records []map[string]any
		for _, value := range list.Items {
			row, _ := value.(map[string]any)
			if row["i"] == "minecraft:stone_button" && row["x"] == int32(24) && row["y"] == int32(-60) && row["z"] == int32(4) {
				records = append(records, row)
			}
		}
		last = records
		if len(records) == 1 && records[0]["t"] == int32(remaining) && records[0]["p"] == int32(0) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("clone did not first-request deduplicate loaded records with original metadata: %#v", last)
}

func TestScheduledTickChunkLoadParity(t *testing.T) {
	jar := blackboxJar(t)
	source := t.TempDir()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	if err := os.WriteFile(filepath.Join(source, "ops.json"), []byte(opsJSONFor("ScheduledSave")), 0644); err != nil {
		t.Fatal(err)
	}
	var sourceServer *server.Server
	addr := startMcGoCfg(t, port, func(s *server.Server) {
		sourceServer = s
		s.GameMode = server.GameModeCreative
		s.WorldDir = filepath.Join(source, "world")
		s.ServerDir = source
	})
	p, closeSource := scheduledSaveJoinBB(t, "source", addr, "ScheduledSave")
	armScheduledSaveBB(p)
	awaitSavedTickTargetsBB(t, savedTickRegionBB(source))
	prepared := t.TempDir()
	copyScheduledSaveBB(t, source, prepared)
	closeSource()
	sourceServer.Stop()
	chunks, err := world.ReadRegionFile(savedTickRegionBB(prepared))
	if err != nil {
		t.Fatal(err)
	}
	chunks[[2]int32{}]["block_ticks"] = loaderFixtureRecordsBB()
	writeLoaderRegionBB(t, savedTickRegionBB(prepared), chunks)
	for _, kind := range []string{"vanilla", "mc-go"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			copyScheduledSaveBB(t, prepared, dir)
			port, ok := freePort()
			if !ok {
				t.Skip("loopback unavailable")
			}
			var stop func()
			var addr string
			if kind == "vanilla" {
				addr, stop = startVanillaInDir(t, jar, port, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("ScheduledRead")}, dir)
			} else {
				if err := os.WriteFile(filepath.Join(dir, "ops.json"), []byte(opsJSONFor("ScheduledRead")), 0644); err != nil {
					t.Fatal(err)
				}
				var runtime *server.Server
				addr = startMcGoCfg(t, port, func(s *server.Server) {
					runtime = s
					s.GameMode = server.GameModeCreative
					s.WorldDir = filepath.Join(dir, "world")
					s.ServerDir = dir
				})
				stop = runtime.Stop
			}
			defer stop()
			read, closeRead := scheduledSaveJoinBB(t, kind, addr, "ScheduledRead")
			defer closeRead()
			read.step(1)
			read.command("save-all flush")
			remaining := awaitLoadedTickRecordsBB(t, savedTickRegionBB(dir))
			read.command("clone 8 -60 4 8 -60 4 24 -60 4")
			read.expect("if block 24 -60 4 stone_button[powered=true]", "loader_clone_applied")
			read.command("save-all flush")
			awaitClonedLoadedTickBB(t, savedTickRegionBB(dir), remaining)
			read.command("tick rate 1000")
			read.step(remaining - 1)
			read.expect("if block 8 -60 4 stone_button[powered=true]", "loader_before_deadline")
			read.expect("if block 24 -60 4 stone_button[powered=true]", "loader_clone_before_deadline")
			read.step(1)
			read.expect("if block 8 -60 4 stone_button[powered=false]", "loader_exact_deadline")
			read.expect("if block 24 -60 4 stone_button[powered=false]", "loader_clone_exact_deadline")
			read.step(401)
			read.expect("if block 4 -60 4 oak_button[powered=true]", "loader_stale_owner")
			read.expect("if block 4 -60 13 water", "loader_fluid_resumed")
		})
	}
}
