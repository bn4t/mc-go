//go:build blackbox

package blackbox

import (
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/server"
)

// Residents are generated with the hut, independently of natural spawning and
// world ticks. The block-only legacy fixture separately pins the hut itself.
func TestStructureHutResidentsParity(t *testing.T) {
	overlay := structureOverrides(nil, "swamp_huts")
	files := stageDatapack(overlay)
	files["ops.json"] = opsJSONFor("HutResident")
	dir := t.TempDir()
	var goServer *server.Server
	targets := requireBlackboxFiles(t, map[string]string{
		"level-type": "minecraft:normal", "generator-settings": "{}", "level-seed": "12345", "generate-structures": "true", "view-distance": "2", "simulation-distance": "2", "difficulty": "normal",
	}, files, func(s *server.Server) {
		goServer = s
		s.SetFrozen(true)
		s.LevelType = server.LevelTypeNormal
		s.WorldSeed = 12345
		s.GenerateStructures = true
		s.WorldgenOverlay = overlay
		s.ViewDistance, s.SimulationDistance = 2, 2
		s.WorldDir = filepath.Join(dir, "world")
	})
	defer targets.cleanup()
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "HutResident")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "gamerule minecraft:spawn_mobs false")
			sendCommand(t, pw, "scoreboard objectives add hutResidents dummy")
			sendCommand(t, pw, "tp @s -2381.5 80 -1386.5")
			deadline := time.Now().Add(150 * time.Second)
			found := false
			for time.Now().Before(deadline) {
				sendCommand(t, pw, "execute if entity @e[type=minecraft:witch] if entity @e[type=minecraft:cat] run say HUTPAIR")
				sendCommand(t, pw, "say HUTPROBEEND")
				if structureSaidBB(waitForExactSayBB(t, sink, "HUTPROBEEND", 30*time.Second), "HUTPAIR") {
					found = true
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if !found {
				t.Fatal("generated resident pair absent with frozen world and natural spawning disabled")
			}
			for _, phase := range []string{"initial", "reaccess"} {
				if phase == "reaccess" {
					sendCommand(t, pw, "forceload add -2384 -1392 -2369 -1377")
					sendCommand(t, pw, "tp @s -2380.5 80 -1385.5")
				}
				for _, cmd := range []string{
					"scoreboard players set #witch hutResidents 0", "scoreboard players set #cat hutResidents 0",
					"execute as @e[type=minecraft:witch] run scoreboard players add #witch hutResidents 1",
					"execute as @e[type=minecraft:cat] run scoreboard players add #cat hutResidents 1",
					"execute if score #witch hutResidents matches 1 if score #cat hutResidents matches 1 run say HUTCOUNTS",
					`execute if data entity @e[type=minecraft:witch,limit=1] {Pos:[-2381.5d,66.0d,-1386.5d],Rotation:[0.0f,0.0f],Health:26.0f,PersistenceRequired:1b,CanJoinRaid:1b} run say HUTWITCH`,
					`execute if data entity @e[type=minecraft:cat,limit=1] {Pos:[-2381.5d,66.0d,-1386.5d],Rotation:[0.0f,0.0f],Health:10.0f,PersistenceRequired:1b,Age:0,variant:"minecraft:all_black"} run say HUTCAT`,
					"say HUTVERIFYEND",
				} {
					sendCommand(t, pw, cmd)
				}
				lines := waitForExactSayBB(t, sink, "HUTVERIFYEND", 30*time.Second)
				for _, marker := range []string{"HUTCOUNTS", "HUTWITCH", "HUTCAT"} {
					if !structureSaidBB(lines, marker) {
						t.Errorf("%s: missing %s: %v", phase, marker, lines)
					}
				}
			}
			sendCommand(t, pw, "save-all flush")
			sendCommand(t, pw, "say HUTSAVED")
			waitForExactSayBB(t, sink, "HUTSAVED", 30*time.Second)
		})
	}
	goServer.Stop()
	t.Run("go_native_reload", func(t *testing.T) {
		port, ok := freePort()
		if !ok {
			t.Skip("loopback unavailable")
		}
		addr := startMcGoCfg(t, port, func(s *server.Server) {
			s.WorldDir = filepath.Join(dir, "world")
			s.WorldgenOverlay = overlay
			s.ViewDistance, s.SimulationDistance = 2, 2
			s.SetFrozen(true)
		})
		conn, pr, pw := joinPlayBlackbox(t, addr, "HutReload")
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkResolvedTP(pr, pw)
		for _, cmd := range []string{
			"tp @s -2381.5 80 -1386.5",
			"scoreboard objectives add hutReload dummy",
			"scoreboard players set #witch hutReload 0", "scoreboard players set #cat hutReload 0",
			"execute as @e[type=minecraft:witch] run scoreboard players add #witch hutReload 1",
			"execute as @e[type=minecraft:cat] run scoreboard players add #cat hutReload 1",
			"execute if score #witch hutReload matches 1 if score #cat hutReload matches 1 run say HUTRELOADCOUNT",
			`execute if data entity @e[type=minecraft:witch,limit=1] {Pos:[-2381.5d,66.0d,-1386.5d],PersistenceRequired:1b,Health:26.0f} if data entity @e[type=minecraft:cat,limit=1] {Pos:[-2381.5d,66.0d,-1386.5d],PersistenceRequired:1b,Health:10.0f,variant:"minecraft:all_black"} run say HUTRELOADDATA`,
			"say HUTRELOADEND",
		} {
			sendCommand(t, pw, cmd)
		}
		lines := waitForExactSayBB(t, sink, "HUTRELOADEND", 30*time.Second)
		for _, marker := range []string{"HUTRELOADCOUNT", "HUTRELOADDATA"} {
			if !structureSaidBB(lines, marker) {
				t.Errorf("native-file restart missing %s: %v", marker, lines)
			}
		}
	})

}
