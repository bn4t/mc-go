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

func blockedTickTargetBB(t *testing.T, jar, kind, dir string) (string, func(), *server.Server) {
	t.Helper()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	if kind == "native" {
		addr, stop := startVanillaInDir(t, jar, port, map[string]string{"gamemode": "creative", "view-distance": "6", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("ScheduledRead")}, dir)
		return addr, stop, nil
	}
	if err := os.WriteFile(filepath.Join(dir, "ops.json"), []byte(opsJSONFor("ScheduledRead")), 0644); err != nil {
		t.Fatal(err)
	}
	var runtime *server.Server
	addr := startMcGoCfg(t, port, func(s *server.Server) {
		runtime = s
		s.GameMode = server.GameModeCreative
		s.ViewDistance = 6
		s.SimulationDistance = 2
		s.WorldDir = filepath.Join(dir, "world")
		s.ServerDir = dir
	})
	return addr, runtime.Stop, runtime
}

func saveBlockedTickBB(t *testing.T, p *scheduledProbeBB, runtime *server.Server, path string) (map[[2]int32]map[string]any, int32) {
	t.Helper()
	p.command("save-all flush")
	if runtime != nil {
		if err := runtime.SaveWorld(); err != nil {
			t.Fatal(err)
		}
	}
	chunks, err := world.ReadRegionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := chunks[[2]int32{20, 0}]["block_ticks"].(nbt.List)
	for _, value := range list.Items {
		row, _ := value.(map[string]any)
		if row["i"] == "minecraft:stone_button" && row["x"] == int32(3912) && row["y"] == int32(-60) && row["z"] == int32(4) {
			delay, ok := row["t"].(int32)
			if !ok {
				t.Fatalf("delay type %#v", row)
			}
			return chunks, delay
		}
	}
	t.Fatalf("blocked pending row absent %#v", list)
	return nil, 0
}

func cloneBlockedTickBB(t *testing.T, p *scheduledProbeBB, runtime *server.Server, path string, dest int, delay int32, label string) {
	t.Helper()
	p.command(fmt.Sprintf("clone 3912 -60 4 3912 -60 4 %d -60 4 replace force", dest))
	p.expect(fmt.Sprintf("if block %d -60 4 stone_button[powered=true]", dest), label+"_placed")
	chunks, _ := saveBlockedTickBB(t, p, runtime, path)
	list, _ := chunks[[2]int32{16, 0}]["block_ticks"].(nbt.List)
	found := 0
	for _, value := range list.Items {
		row, _ := value.(map[string]any)
		if row["i"] == "minecraft:stone_button" && row["x"] == int32(dest) {
			found++
			if row["t"] != delay {
				t.Errorf("copied overdue deadline%v want%d", row["t"], delay)
			}
		}
	}
	if found != 1 {
		t.Fatalf("overdue clone record count%d %#v", found, list)
	}
	age := gameTimeOf(t, p.pw, p.sink)
	p.step(1)
	p.await(fmt.Sprintf("if block %d -60 4 stone_button[powered=false]", dest), label+"_next_tick")
	if after := gameTimeOf(t, p.pw, p.sink); after != age+1 {
		t.Fatalf("overdue applied age%d want%d", after, age+1)
	}
	p.expect("if block 3912 -60 4 stone_button[powered=true]", label+"_source_blocked")
}

func TestScheduledTickBlockedRestartParity(t *testing.T) {
	jar := blackboxJar(t)
	for _, kind := range []string{"native", "mc-go"} {
		t.Run(kind, func(t *testing.T) {
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
			seed, closeSeed := scheduledSaveJoinBB(t, "source", addr, "ScheduledSave")
			armScheduledSaveBB(seed)
			awaitSavedTickTargetsBB(t, savedTickRegionBB(source))
			dir := t.TempDir()
			copyScheduledSaveBB(t, source, dir)
			closeSeed()
			sourceServer.Stop()
			input, err := world.ReadRegionFile(savedTickRegionBB(dir))
			if err != nil {
				t.Fatal(err)
			}
			template := input[[2]int32{}]
			far := map[[2]int32]map[string]any{}
			for _, cx := range []int{240, 244, 248} {
				chunk := map[string]any{}
				for key, value := range template {
					chunk[key] = value
				}
				chunk["xPos"], chunk["zPos"] = int32(cx), int32(0)
				chunk["block_ticks"] = nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"i": "minecraft:stone_button", "x": int32(cx*16 + 8), "y": int32(-60), "z": int32(4), "t": int32(17), "p": int32(0)}}}
				chunk["fluid_ticks"] = nbt.List{Elem: nbt.TagCompound}
				far[[2]int32{int32(cx % 32), 0}] = chunk
			}
			path := filepath.Join(filepath.Dir(savedTickRegionBB(dir)), "r.7.0.mca")
			writeLoaderRegionBB(t, path, far)
			addr, stop, runtime := blockedTickTargetBB(t, jar, kind, dir)
			defer stop()
			p, closeRead := scheduledSaveJoinBB(t, kind, addr, "ScheduledRead")
			defer closeRead()
			p.step(100)
			p.command("tp @s 3848.5 -60 5.5 180 56")
			p.moveReady(3848.6, -60, 5.5)
			for dx := -1; dx <= 1; dx++ {
				for dz := -1; dz <= 1; dz++ {
					p.await(fmt.Sprintf("if loaded %d -60 %d", 3848+16*dx, 4+16*dz), fmt.Sprintf("blocked_center_ready_%d_%d", dx, dz))
				}
			}
			p.await("if loaded 3912 -60 4", "blocked_source_entity_ready")
			p.step(40)
			p.expect("if block 3848 -60 4 stone_button[powered=false]", "blocked_center_fired")
			p.expect("if block 3912 -60 4 stone_button[powered=true]", "blocked_source_held")
			p.command("setblock 3905 -60 1 stone strict")
			before, first := saveBlockedTickBB(t, p, runtime, path)
			if first >= 0 {
				t.Fatalf("activated nonsimulating deadline not overdue%d", first)
			}
			p.step(20)
			p.command("setblock 3905 -60 1 gold_block strict")
			after, second := saveBlockedTickBB(t, p, runtime, path)
			if second != first-20 {
				t.Fatalf("absolute deadline aged%d→%d want%d", first, second, first-20)
			}
			if kind == "native" {
				a, _ := before[[2]int32{20, 0}]["LastUpdate"].(int64)
				b, _ := after[[2]int32{20, 0}]["LastUpdate"].(int64)
				if b-a != 20 {
					t.Fatalf("fresh native ages%d→%d", a, b)
				}
			}
			cold, _ := after[[2]int32{24, 0}]["block_ticks"].(nbt.List)
			if len(cold.Items) != 1 {
				t.Fatalf("cold FULL row absent %#v", cold)
			}
			coldRow, _ := cold.Items[0].(map[string]any)
			if coldRow["t"] != int32(17) {
				t.Fatalf("cold relative delay changed %#v", coldRow)
			}
			cloneBlockedTickBB(t, p, runtime, path, 3850, second, "blocked_before_restart")
			p.command("setblock 3905 -60 1 diamond_block strict")
			_, saved := saveBlockedTickBB(t, p, runtime, path)
			if saved != second-1 {
				t.Fatalf("pre-restart overdue value%d want%d", saved, second-1)
			}
			closeRead()
			stop()
			addr, stop, runtime = blockedTickTargetBB(t, jar, kind, dir)
			defer stop()
			p, closeRead = scheduledSaveJoinBB(t, kind, addr, "ScheduledRead")
			defer closeRead()
			p.await("if loaded 3912 -60 4", "blocked_restart_entity_ready")
			p.expect("if block 3912 -60 4 stone_button[powered=true]", "blocked_restart_held")
			p.step(20)
			p.command("setblock 3905 -60 1 emerald_block strict")
			_, remaining := saveBlockedTickBB(t, p, runtime, path)
			if remaining >= 0 {
				t.Fatalf("own restart reset negative pending delay%d", remaining)
			}
			cloneBlockedTickBB(t, p, runtime, path, 3851, remaining, "blocked_after_restart")
		})
	}
}
