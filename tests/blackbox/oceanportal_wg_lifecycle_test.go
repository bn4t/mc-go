//go:build blackbox

package blackbox

import (
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// This native-only control pins the FULL-chunk cache lifetime independently
// of the Go implementation: retained after edits, omitted from disk, fresh
// after reopening a copied completed save.
func TestOceanPortalNativeWGLifecycle(t *testing.T) {
	const name = "OceanWGCache"
	jar := blackboxJar(t)
	props := map[string]string{"level-type": "minecraft:normal", "generator-settings": "{}", "level-seed": "1", "generate-structures": "false", "view-distance": "2", "simulation-distance": "2"}
	files := map[string]string{"ops.json": opsJSONFor(name)}
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute
	dir := t.TempDir()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	addr, stop := startVanillaInDir(t, jar, port, props, files, dir)
	for phase := 0; phase < 2; phase++ {
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		drainToJoinBlackbox(t, "native", pr)
		awaitPlayReadyBlackbox(t, "native", c, pr, pw)
		sink := newChatSinkResolvedTP(pr, pw)
		seq := 0
		command := func(text string) []string {
			seq++
			sendCommand(t, pw, text)
			mark := fmt.Sprintf("NATIVE_WG_DONE_%d", seq)
			sendCommand(t, pw, "say "+mark)
			return waitForExactSayBB(t, sink, mark, 60*time.Second)
		}
		feedback := func(text, want string) {
			t.Helper()
			for _, line := range command(text) {
				if key, _, ok := componentArgs(line); ok && key == want {
					return
				}
			}
			t.Fatalf("%s missing %s", text, want)
		}
		witness := func(text string) bool {
			t.Helper()
			seq++
			mark := fmt.Sprintf("NATIVE_WG_IF_%d", seq)
			return structureSaidBB(command(text+" run say "+mark), mark)
		}
		count := func(block string) int {
			t.Helper()
			for _, line := range command("fill 0 30 -11 0 50 19 air replace " + block) {
				if key, args, ok := componentArgs(line); ok {
					if key == "commands.fill.failed" {
						return 0
					}
					if key == "commands.fill.success" && len(args) == 1 {
						n, err := strconv.Atoi(fmt.Sprint(args[0]))
						if err != nil {
							t.Fatal(err)
						}
						return n
					}
				}
			}
			t.Fatal("missing typed fill count")
			return -1
		}
		command("tick freeze")
		command("gamerule minecraft:spawn_mobs false")
		command("forceload add -16 -16 31 31")
		command("tp @s 0 100 0")
		deadline := time.Now().Add(2 * time.Minute)
		for !witness("execute if loaded -16 64 -16 if loaded 31 64 31") {
			if time.Now().After(deadline) {
				t.Fatal("source chunks not ready")
			}
		}
		if n, m := count("netherrack"), count("magma_block"); n != 0 || m != 0 {
			t.Fatalf("phase%d baseline netherrack%d magma%d", phase, n, m)
		}
		if phase == 0 {
			feedback("place structure ruined_portal_ocean 0 100 0", "commands.place.structure.success")
			if !witness(`execute if data block 9 40 3 {LootTable:"minecraft:chests/ruined_portal"}`) {
				t.Fatal("initial chest absent")
			}
			for _, band := range [][2]int{{20, 50}, {51, 80}, {81, 85}} {
				feedback(fmt.Sprintf("fill -10 %d -11 20 %d 19 stone", band[0], band[1]), "commands.fill.success")
			}
		}
		if n, m := count("netherrack"), count("magma_block"); n != 0 || m != 0 {
			t.Fatalf("phase%d raised/reloaded baseline netherrack%d magma%d", phase, n, m)
		}
		if !witness("execute if block 0 85 3 stone if block 0 86 3 air") {
			t.Fatal("changed or reloaded terrain absent")
		}
		feedback("execute positioned 0 0 3 positioned over ocean_floor run tp @s ~ ~ ~", "commands.teleport.success.location.single")
		if !witness(`execute if data entity @s {Pos:[0.5d,86.0d,3.5d]}`) {
			t.Fatal("current ocean floor is not first-free y86")
		}
		feedback("place structure ruined_portal_ocean 0 100 0", "commands.place.structure.success")
		if !witness(`execute if data block 9 40 3 {LootTable:"minecraft:chests/ruined_portal"}`) {
			t.Fatal("repeat chest absent")
		}
		n, m := count("netherrack"), count("magma_block")
		t.Logf("phase%d outside-template netherrack%d magma%d", phase, n, m)
		if phase == 0 && n+m <= 0 || phase == 1 && n+m != 0 {
			t.Fatalf("phase%d retained/fresh heightmap outcome netherrack%d magma%d", phase, n, m)
		}
		if phase == 0 {
			feedback("save-all flush", "commands.save.success")
			saved := readSavedChunk(t, filepath.Join(dir, "world", "dimensions", "minecraft", "overworld", "region"), 0, 0, -64, 384)
			maps, _ := saved.raw["Heightmaps"].(map[string]any)
			if len(maps) != 4 {
				t.Fatalf("saved heightmap count%d", len(maps))
			}
			for _, key := range []string{"MOTION_BLOCKING", "MOTION_BLOCKING_NO_LEAVES", "OCEAN_FLOOR", "WORLD_SURFACE"} {
				if _, ok := maps[key]; !ok {
					t.Fatalf("missing final map%s", key)
				}
			}
			copied := t.TempDir()
			copyTree(t, filepath.Join(dir, "world"), filepath.Join(copied, "world"))
			_ = c.Close()
			stop()
			dir = copied
			port, ok = freePort()
			if !ok {
				t.Skip("loopback unavailable")
			}
			addr, stop = startVanillaInDir(t, jar, port, props, files, dir)
		} else {
			_ = c.Close()
			stop()
		}
	}
}
