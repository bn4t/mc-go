//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStructureMineshaftResidentsParity(t *testing.T) {
	overlay := structureOverrides(nil, "mineshafts")
	files := stageDatapack(overlay)
	files["ops.json"] = opsJSONFor("MineCartGen")
	dir := t.TempDir()
	var goServer *server.Server
	configure := func(s *server.Server) {
		goServer = s
		s.LevelType = server.LevelTypeNormal
		s.WorldSeed = 12345
		s.GenerateStructures = true
		s.WorldgenOverlay = overlay
		s.ViewDistance = 2
		s.SimulationDistance = 2
		s.WorldDir = filepath.Join(dir, "world")
		s.SetFrozen(true)
	}
	targets := requireBlackboxFiles(t, map[string]string{"level-type": "minecraft:normal", "generator-settings": "{}", "level-seed": "12345", "generate-structures": "true", "view-distance": "2", "simulation-distance": "2"}, files, configure)
	defer targets.cleanup()
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute
	points := []struct {
		x, y, z float64
		seed    int64
	}{{-85.5, 41.5, -20.5, -653164966778954613}, {-28.5, 37.5, 84.5, -4714629231958949515}}
	probe := func(t *testing.T, addr, name, objective string, materialize bool) {
		conn, pr, pw := joinPlayBlackbox(t, addr, name)
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkResolvedTP(pr, pw)
		for _, cmd := range []string{"tick freeze", "gamerule minecraft:spawn_mobs false", "gamemode spectator @s", "scoreboard objectives add " + objective + " dummy", "say MINESETUP"} {
			sendCommand(t, pw, cmd)
		}
		waitForExactSayBB(t, sink, "MINESETUP", 20*time.Second)
		sendCommand(t, pw, "forceload add -112 -32 -1 127")
		if materialize {
			for x := -7; x <= -1; x++ {
				for z := -2; z <= 7; z++ {
					goServer.World().GetBlock(world.Pos{X: x * 16, Y: 0, Z: z * 16})
				}
			}
		}
		for index, point := range points {
			selector := fmt.Sprintf("@e[type=minecraft:chest_minecart,x=%g,y=%g,z=%g,distance=..0.01,limit=1]", point.x, point.y, point.z)
			sendCommand(t, pw, fmt.Sprintf("tp @s %g %g %g", point.x, point.y+1, point.z))
			ready := false
			deadline := time.Now().Add(150 * time.Second)
			for !ready && time.Now().Before(deadline) {
				sendCommand(t, pw, "execute if entity "+selector+" run say MINECARTREADY")
				sendCommand(t, pw, "say MINECARTEND")
				ready = structureSaidBB(waitForExactSayBB(t, sink, "MINECARTEND", 30*time.Second), "MINECARTREADY")
				if !ready {
					time.Sleep(100 * time.Millisecond)
				}
			}
			if !ready {
				t.Fatalf("native generated cart%d missing", index)
			}
			sendCommand(t, pw, fmt.Sprintf(`execute if data entity %s {Pos:[%gd,%gd,%gd],Rotation:[0.0f,0.0f],HasTicked:1b,FlippedRotation:0b,OnGround:0b,Motion:[0.0d,0.0d,0.0d],LootTable:"minecraft:chests/abandoned_mineshaft"} unless data entity %s Items run say MINECARTDATA`, selector, point.x, point.y, point.z, selector))
			sendCommand(t, pw, "data get entity "+selector+" LootTableSeed")
			sendCommand(t, pw, "say MINEVERIFYEND")
			lines := waitForExactSayBB(t, sink, "MINEVERIFYEND", 30*time.Second)
			if !structureSaidBB(lines, "MINECARTDATA") {
				t.Errorf("cart%d constructor/latent loot state mismatched: %v", index, lines)
			}
			exactSeed := false
			for _, line := range lines {
				key, args, ok := componentArgs(line)
				if !ok || key != "commands.data.entity.query" || len(args) < 2 {
					continue
				}
				text := mobKBComponentTextBB(args[1])
				text = strings.TrimSuffix(strings.TrimSuffix(text, "L"), "l")
				value, err := strconv.ParseInt(text, 10, 64)
				if err == nil && value == point.seed {
					exactSeed = true
				}
			}
			if !exactSeed {
				t.Errorf("cart%d exact64-bit seed want%d: %v", index, point.seed, lines)
			}
		}
		for _, cmd := range []string{"scoreboard players set #carts " + objective + " 0", "execute as @e[type=minecraft:chest_minecart,x=-112,y=-64,z=-32,dx=111,dy=383,dz=159] run scoreboard players add #carts " + objective + " 1", "execute if score #carts " + objective + " matches 2 run say MINECOUNTS", "say MINECOUNTEND"} {
			sendCommand(t, pw, cmd)
		}
		if !structureSaidBB(waitForExactSayBB(t, sink, "MINECOUNTEND", 20*time.Second), "MINECOUNTS") {
			t.Error("generated arena count want2")
		}
		sendCommand(t, pw, "save-all flush")
		sendCommand(t, pw, "say MINESAVED")
		waitForExactSayBB(t, sink, "MINESAVED", 30*time.Second)
	}
	t.Run("native", func(t *testing.T) { probe(t, targets.vanilla, "MineCartGen", "mineNative", false) })
	t.Run("go", func(t *testing.T) { probe(t, targets.mcgo, "MineCartGen", "mineGo", true) })
	goServer.Stop()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	addr := startMcGoCfg(t, port, configure)
	t.Run("go_native_reload", func(t *testing.T) { probe(t, addr, "MineCartReload", "mineReload", true) })
}
