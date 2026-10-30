//go:build blackbox

package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

func TestScheduledTickDormantRestartParity(t *testing.T) {
	t.Run("vanilla", func(t *testing.T) { scheduledDormantRestartBB(t, "native") })
	t.Run("mc-go", func(t *testing.T) { scheduledDormantRestartBB(t, "mc-go") })
}

func startScheduledRestartTargetBB(t *testing.T, jar, kind, dir string) (string, func(), *server.Server) {
	t.Helper()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	if kind == "native" {
		addr, stop := startVanillaInDir(t, jar, port, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("ScheduledRead")}, dir)
		return addr, stop, nil
	}
	if err := os.WriteFile(filepath.Join(dir, "ops.json"), []byte(opsJSONFor("ScheduledRead")), 0644); err != nil {
		t.Fatal(err)
	}
	var runtime *server.Server
	addr := startMcGoCfg(t, port, func(s *server.Server) {
		runtime = s
		s.GameMode = server.GameModeCreative
		s.WorldDir = filepath.Join(dir, "world")
		s.ServerDir = dir
	})
	return addr, runtime.Stop, runtime
}

func scheduledDormantRestartBB(t *testing.T, kind string) {
	jar := blackboxJar(t)
	source := t.TempDir()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	if err := os.WriteFile(filepath.Join(source, "ops.json"), []byte(opsJSONFor("ScheduledSave")), 0644); err != nil {
		t.Fatal(err)
	}
	var runtime *server.Server
	addr := startMcGoCfg(t, port, func(s *server.Server) {
		runtime = s
		s.GameMode = server.GameModeCreative
		s.WorldDir = filepath.Join(source, "world")
		s.ServerDir = source
	})
	p, closeSource := scheduledSaveJoinBB(t, "source", addr, "ScheduledSave")
	armScheduledSaveBB(p)
	awaitSavedTickTargetsBB(t, savedTickRegionBB(source))
	restart := t.TempDir()
	copyScheduledSaveBB(t, source, restart)
	closeSource()
	runtime.Stop()
	chunks, err := world.ReadRegionFile(savedTickRegionBB(restart))
	if err != nil {
		t.Fatal(err)
	}
	far := chunks[[2]int32{}]
	far["xPos"], far["zPos"] = int32(250), int32(0)
	far["block_ticks"] = nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"i": "minecraft:stone_button", "x": int32(4008), "y": int32(-60), "z": int32(4), "t": int32(17), "p": int32(0)}}}
	far["fluid_ticks"] = nbt.List{Elem: nbt.TagCompound}
	path := filepath.Join(filepath.Dir(savedTickRegionBB(restart)), "r.7.0.mca")
	writeLoaderRegionBB(t, path, map[[2]int32]map[string]any{{26, 0}: far})
	addr, stop, loaded := startScheduledRestartTargetBB(t, jar, kind, restart)
	defer stop()
	read, closeRead := scheduledSaveJoinBB(t, kind, addr, "ScheduledRead")
	defer closeRead()
	read.step(100)
	read.command("save-all flush")
	if loaded != nil {
		if err := loaded.SaveWorld(); err != nil {
			t.Fatal(err)
		}
	}
	preserved, err := world.ReadRegionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := preserved[[2]int32{26, 0}]["block_ticks"].(nbt.List)
	if len(pending.Items) != 1 {
		t.Errorf("dormant save lost record: %#v", pending)
	} else {
		row, _ := pending.Items[0].(map[string]any)
		if row["t"] != int32(17) || row["i"] != "minecraft:stone_button" {
			t.Errorf("dormant save aged record: %#v", row)
		}
	}
	closeRead()
	stop()
	addr, stop, loaded = startScheduledRestartTargetBB(t, jar, kind, restart)
	defer stop()
	read, closeRead = scheduledSaveJoinBB(t, kind, addr, "ScheduledRead")
	defer closeRead()
	read.step(100)
	read.command("save-all flush")
	if loaded != nil {
		if err := loaded.SaveWorld(); err != nil {
			t.Fatal(err)
		}
	}
	twice, err := world.ReadRegionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := twice[[2]int32{26, 0}]["block_ticks"].(nbt.List)
	if len(again.Items) != 1 {
		t.Fatalf("own restart lost dormant record %#v", again)
	}
	savedAgain, _ := again.Items[0].(map[string]any)
	if savedAgain["t"] != int32(17) {
		t.Fatalf("own restart aged dormant record %#v", savedAgain)
	}
	read.command("setblock 2 -60 2 gold_block")
	read.command("clone 4008 -60 4 4008 -60 4 2 -60 2 replace force")
	read.expect("if block 2 -60 2 gold_block", "unloaded_clone_leaves_destination")
	read.expect("unless loaded 4008 -60 4", "clone_did_not_activate_source")
	read.command("tp @s 4008.5 -60 5.5 180 56")
	read.moveReady(4008.6, -60, 5.5)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			read.await(fmt.Sprintf("if loaded %d -60 %d", 4008+16*dx, 4+16*dz), fmt.Sprintf("far_chunk_loaded_%d_%d", dx, dz))
		}
	}
	read.expect("if block 4008 -60 4 stone_button[powered=true]", "far_pending_after_100_dormant")
	witnessAge := gameTimeOf(t, read.pw, read.sink)
	read.command("clone 4008 -60 4 4008 -60 4 4009 -60 4 replace force")
	read.expect("if block 4009 -60 4 stone_button[powered=true]", "loaded_clone_control")
	read.command("save-all flush")
	if loaded != nil {
		if err := loaded.SaveWorld(); err != nil {
			t.Fatal(err)
		}
	}
	armed, err := world.ReadRegionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	armedRows, _ := armed[[2]int32{26, 0}]["block_ticks"].(nbt.List)
	for _, x := range []int32{4008, 4009} {
		count := 0
		for _, value := range armedRows.Items {
			row, _ := value.(map[string]any)
			if row["i"] == "minecraft:stone_button" && row["x"] == x {
				count++
				if row["t"] != int32(17) || row["p"] != int32(0) {
					t.Errorf("activated17 receipt %#v", row)
				}
			}
		}
		if count != 1 {
			t.Fatalf("activated source/clone receipt at%d count%d rows%#v", x, count, armedRows)
		}
	}
	afterWitness := gameTimeOf(t, read.pw, read.sink)
	if afterWitness != witnessAge {
		t.Fatalf("readiness witness advanced world age%d→%d", witnessAge, afterWitness)
	}
	t.Logf("ACTIVE17_WITNESS age%d absoluteDeadline%d", witnessAge, witnessAge+17)
	read.step(16)
	read.expect("if block 4008 -60 4 stone_button[powered=true]", "far_before_relative_17")
	read.step(1)
	read.await("if block 4008 -60 4 stone_button[powered=false]", "far_relative_17")
	read.await("if block 4009 -60 4 stone_button[powered=false]", "far_clone_relative_17")
	if after := gameTimeOf(t, read.pw, read.sink); after != witnessAge+17 {
		t.Fatalf("applied release age%d want%d", after, witnessAge+17)
	}
}
