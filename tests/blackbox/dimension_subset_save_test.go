//go:build blackbox

package blackbox

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

func subsetReadbackRecord(t *testing.T, file []byte, slot int) []byte {
	t.Helper()
	location := binary.BigEndian.Uint32(file[slot*4:])
	start := int(location>>8) * 4096
	length := int(binary.BigEndian.Uint32(file[start:]))
	return file[start+4 : start+4+length]
}

func TestNativeDimensionSubsetSaveReadback(t *testing.T) {
	jar := blackboxJar(t)
	dir := t.TempDir()
	worldDir := filepath.Join(dir, "world")
	if err := world.WriteLevel(world.LevelData{Dir: worldDir, LevelName: "Subset", Generator: "flat", GameType: 1, Difficulty: "peaceful", Spawn: [3]int{8, -60, 5}}); err != nil {
		t.Fatal(err)
	}
	w := world.New(nil, 2)
	defer w.Stop()
	w.SetBiome("minecraft:plains")
	button, ok := world.BlockStateOf("stone_button", map[string]string{"face": "floor", "powered": "true"})
	if !ok {
		t.Fatal("button fixture")
	}
	a, b, delta := world.Pos{X: 8, Y: -60, Z: 4}, world.Pos{X: 24, Y: -60, Z: 4}, world.Pos{X: 10, Y: -59, Z: 4}
	w.SetBlock(a, button)
	w.SetBlock(b, world.BlockStateMust("gold_block"))
	ticks := map[[2]int32]world.SavedChunkTicks{{0, 0}: {Blocks: []world.SavedTick{{Type: "minecraft:stone_button", Pos: a, Delay: 100000}}}}
	if err := w.CaptureDimensionSaveWithTicks(-64, 384, nil, nil, ticks).Write(worldDir, "minecraft:overworld"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(world.DimensionDir(worldDir, "minecraft:overworld"), "region", "r.0.0.mca")
	chunks, err := world.ReadRegionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	chunks[[2]int32{1, 0}]["OpaqueUntouched"] = map[string]any{"marker": "keep", "longs": []int64{7, 11, 13}}
	writeLoaderRegionBB(t, path, chunks)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint32(before[4100:], 0x12345678)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	foreign := append([]byte(nil), subsetReadbackRecord(t, before, 1)...)
	w.SetBlock(delta, world.BlockStateMust("diamond_block"))
	w.SetBlock(b, world.BlockStateMust("iron_block")) // deliberately NOT selected
	snapshot := w.CaptureDimensionChunksWithTicks(-64, 384, nil, nil, ticks)
	w.Stop()
	if err := snapshot.WriteMerged(worldDir, "minecraft:overworld"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(subsetReadbackRecord(t, after, 1), foreign) || binary.BigEndian.Uint32(after[4100:]) != 0x12345678 {
		t.Fatal("selective writer changed untouched chunk bytes/timestamp")
	}
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	addr, stop := startVanillaInDir(t, jar, port, map[string]string{"gamemode": "creative", "view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("SubsetRead")}, dir)
	defer stop()
	p, closeClient := scheduledSaveJoinBB(t, "native", addr, "SubsetRead")
	defer closeClient()
	p.command("tp @s 8.5 -60 5.5")
	p.moveReady(8.6, -60, 5.5)
	p.await("if loaded 8 -60 4", "subset_selected_loaded")
	p.await("if loaded 24 -60 4", "subset_untouched_loaded")
	p.expect("if block 10 -59 4 diamond_block", "subset_selected_delta")
	p.expect("if block 24 -60 4 gold_block", "subset_kept_disk_neighbor")
	p.expect("if block 8 -60 4 stone_button[powered=true]", "subset_saved_tick_owner")
	p.command("clone 8 -60 4 8 -60 4 9 -60 4 replace force")
	p.command("save-all flush")
	readback, err := world.ReadRegionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := readback[[2]int32{0, 0}]["block_ticks"].(nbt.List)
	found := 0
	for _, value := range rows.Items {
		row, _ := value.(map[string]any)
		if row["i"] == "minecraft:stone_button" && row["x"] == int32(9) && row["y"] == int32(-60) && row["z"] == int32(4) {
			found++
			delay, valid := row["t"].(int32)
			if !valid || delay < 50000 || delay > 100000 || row["p"] != int32(0) {
				t.Errorf("native failed to restore/copy selected saved tick: %#v", row)
			}
		}
	}
	if found != 1 {
		t.Fatalf("native copied tick count%d rows%#v", found, rows)
	}
}
