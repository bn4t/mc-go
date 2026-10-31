//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/worldgen"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Preserve the original native red boundary, then pin source frames.
// No hopper or loot consumer is involved.
func TestExecuteBiomeEditedQuartBoundaryParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"level-seed": "12345", "view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("QuartRed", "QuartLoader")}, func(s *server.Server) { s.WorldSeed = 12345; s.ViewDistance = 2; s.SimulationDistance = 2 })
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "QuartRed")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			for _, cmd := range []string{"tick freeze", "fillbiome 0 -64 0 23 -45 23 minecraft:plains", "execute positioned 8.5 -57.5 11.5 if biome ~ ~ ~ minecraft:plains run say REDBASESOURCE", "execute positioned 8.5 -58.5 11.5 if biome ~ ~ ~ minecraft:plains run say REDBASEHOPPER", "say REDBASEEND"} {
				sendCommand(t, pw, cmd)
			}
			lines := waitForExactSayBB(t, sink, "REDBASEEND", 30*time.Second)
			for _, mark := range []string{"REDBASESOURCE", "REDBASEHOPPER"} {
				if !structureSaidBB(lines, mark) {
					t.Fatalf("base biome receipt%s absent", mark)
				}
			}
			for _, cmd := range []string{"fillbiome 8 -60 8 11 -57 11 minecraft:sulfur_caves", "execute positioned 8.5 -57.5 11.5 if biome ~ ~ ~ minecraft:sulfur_caves run say REDEDITEDSOURCE", "execute positioned 8.5 -58.5 11.5 if biome ~ ~ ~ minecraft:plains run say REDNEIGHBORPLAINS", "say REDEDITEND"} {
				sendCommand(t, pw, cmd)
			}
			lines = waitForExactSayBB(t, sink, "REDEDITEND", 30*time.Second)
			for _, mark := range []string{"REDEDITEDSOURCE", "REDNEIGHBORPLAINS"} {
				if !structureSaidBB(lines, mark) {
					t.Errorf("edited biome receipt%s absent", mark)
				}
			}
			executeBiomeFramePins(t, leg.addr, pw, sink)
		})
	}
}

func executeBiomeFramePins(t *testing.T, addr string, pw *protocol.Writer, sink *chatSink) {
	t.Helper()
	serial := 0
	command := func(cmd string) []string {
		serial++
		mark := fmt.Sprintf("BIOMEFRAMEEND%d", serial)
		sendCommand(t, pw, cmd)
		sendCommand(t, pw, "say "+mark)
		return waitForExactSayBB(t, sink, mark, 30*time.Second)
	}
	expect := func(condition, label string) {
		t.Helper()
		mark := "BIOMEFRAME_" + label
		if !structureSaidBB(command("execute "+condition+" run say "+mark), mark) {
			t.Errorf("source-frame biome%s absent", label)
		}
	}
	loadIndex := 0
	waitLoaded := func(dimension string, x0, z0, x1, z1 int) {
		loadIndex++
		mark := fmt.Sprintf("BIOMEFRAMELOAD%d", loadIndex)
		ready := false
		deadline := time.Now().Add(90 * time.Second)
		for !ready && time.Now().Before(deadline) {
			ready = structureSaidBB(command(fmt.Sprintf("execute in %s if loaded %d 70 %d if loaded %d 70 %d run say %s", dimension, x0, z0, x1, z1, mark)), mark)
			if !ready {
				time.Sleep(20 * time.Millisecond)
			}
		}
		if !ready {
			t.Fatal("biome frame area not loaded", dimension, x0, z0, x1, z1)
		}
	}
	command("gamemode spectator @s")
	command("tp @s 0.5 -60 0.5")
	command("forceload add -32 -32 31 31")
	command("fillbiome -24 -64 -24 -1 -45 -1 minecraft:jungle")
	command("fillbiome 0 64 0 23 83 23 minecraft:desert")
	expect("positioned 100.5 70.5 100.5 if biome 8 -58 11 minecraft:sulfur_caves", "absolute_source")
	expect("positioned 100.5 70.5 100.5 if biome 8 -59 11 minecraft:plains unless biome 8 -59 11 minecraft:sulfur_caves", "absolute_neighbor")
	expect("positioned 8.5 -57.5 11.5 rotated 0 0 if biome ~ ~-1 ~ minecraft:plains unless biome ~ ~-1 ~ minecraft:sulfur_caves", "relative_vertical_neighbor")
	expect("positioned -8.25 -58.25 -8.25 if biome ~ ~ ~ minecraft:jungle unless biome ~ ~ ~ minecraft:sulfur_caves", "negative_relative")
	expect("positioned 100.5 70.5 100.5 if biome -9 -59 -9 minecraft:jungle", "negative_absolute")
	expect("if dimension minecraft:overworld if entity @s[x=0.5,y=-60,z=0.5,distance=..0.01]", "editor_before")
	loader, pr, lpw := joinPlayBlackbox(t, addr, "QuartLoader")
	defer loader.Close()
	awaitPlayReadyBlackbox(t, addr, loader, pr, lpw)
	lsink := newChatSinkResolvedTP(pr, lpw)
	lserial := 0
	loaderCommand := func(cmd string) {
		lserial++
		mark := fmt.Sprintf("BIOMELOADEREND%d", lserial)
		sendCommand(t, lpw, cmd)
		sendCommand(t, lpw, "say "+mark)
		waitForExactSayBB(t, lsink, mark, 30*time.Second)
	}
	loaderCommand("gamemode spectator @s")
	command("execute in minecraft:the_nether run forceload add 0 0 31 31")
	loaderCommand("execute in minecraft:the_nether run tp @s 8.5 70.5 11.5")
	waitLoaded("minecraft:the_nether", 0, 0, 23, 23)
	command("execute in minecraft:the_nether run fillbiome 0 64 0 23 83 23 minecraft:badlands")
	command("execute in minecraft:the_nether run forceload add 56 80 79 103")
	loaderCommand("execute in minecraft:the_nether run tp @s 64.5 70.5 88.5")
	waitLoaded("minecraft:the_nether", 56, 80, 79, 103)
	command("execute in minecraft:the_nether run fillbiome 56 64 80 79 83 103 minecraft:forest")
	expect("in minecraft:overworld if biome 8 70 11 minecraft:desert unless biome 8 70 11 minecraft:badlands", "overworld")
	expect("in minecraft:the_nether if biome 8 70 11 minecraft:badlands unless biome 8 70 11 minecraft:desert", "nether")
	expect("positioned 64.5 70.5 88.5 in minecraft:the_nether if biome ~ ~ ~ minecraft:badlands unless biome ~ ~ ~ minecraft:forest", "scaled_position_then_dimension")
	expect("in minecraft:the_nether positioned 64.5 70.5 88.5 if biome ~ ~ ~ minecraft:forest unless biome ~ ~ ~ minecraft:badlands", "unscaled_dimension_then_position")
	expect("positioned 64.5 70.5 88.5 in minecraft:the_nether if biome 64 70 88 minecraft:forest", "absolute_ignores_scaled_source")
	expect("in minecraft:the_nether positioned 64.5 70.5 88.5 if biome 8 70 11 minecraft:badlands", "absolute_ignores_unscaled_source")
	expect("if dimension minecraft:overworld if entity @s[x=0.5,y=-60,z=0.5,distance=..0.01]", "editor_after")
}

// Runtime commands must read generated biome cells with the actual signed
// terrain seed, rather than the dimension's single-biome fallback.
func TestExecuteBiomeTerrainLookupParity(t *testing.T) {
	const seed = int64(-4172144997902289642)
	targets := requireBlackboxFiles(t, map[string]string{"level-type": "minecraft:normal", "generator-settings": "{}", "level-seed": strconv.FormatInt(seed, 10), "generate-structures": "false", "view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("TerrainBiome")}, func(s *server.Server) {
		s.LevelType = server.LevelTypeNormal
		s.WorldSeed = seed
		s.GenerateStructures = false
		s.ViewDistance = 2
		s.SimulationDistance = 2
		s.SetFrozen(true)
	})
	defer targets.cleanup()
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute
	points := [][3]int{{8, 64, 8}, {8, -52, 8}, {1128, 64, -9912}, {-6392, 64, 14408}}
	keys := worldgen.VanillaKeys("biome/")
	if len(keys) < 50 {
		t.Fatal("biome candidate registry absent", len(keys))
	}
	// Independently measured real26.2 values on this exact terrain seed.
	want := []string{"lukewarm_ocean", "lukewarm_ocean", "river", "swamp"}
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		ok := t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "TerrainBiome")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				serial++
				mark := fmt.Sprintf("TERRAINBIOMEEND%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+mark)
				return waitForExactSayBB(t, sink, mark, 40*time.Second)
			}
			command("tick freeze")
			command("gamemode spectator @s")
			seen := map[string]bool{}
			for i, p := range points {
				command(fmt.Sprintf("forceload add %d %d", p[0], p[2]))
				command(fmt.Sprintf("tp @s %g %g %g", float64(p[0])+.5, float64(p[1])+.5, float64(p[2])+.5))
				ready := false
				deadline := time.Now().Add(150 * time.Second)
				mark := fmt.Sprintf("TERRAINBIOMELOADED%d", i)
				for !ready && time.Now().Before(deadline) {
					ready = structureSaidBB(command(fmt.Sprintf("execute if loaded %d %d %d run say %s", p[0], p[1], p[2], mark)), mark)
					if !ready {
						time.Sleep(20 * time.Millisecond)
					}
				}
				if !ready {
					t.Fatal("actual terrain point not loaded", p)
				}
				for j, key := range keys {
					sendCommand(t, pw, fmt.Sprintf("execute if biome %d %d %d minecraft:%s run say TERRAINBIO_%d_%d", p[0], p[1], p[2], strings.TrimPrefix(key, "biome/"), i, j))
				}
				lines := command("say TERRAINBIOMEPOLL")
				var matches []string
				for j, key := range keys {
					if structureSaidBB(lines, fmt.Sprintf("TERRAINBIO_%d_%d", i, j)) {
						matches = append(matches, strings.TrimPrefix(key, "biome/"))
					}
				}
				if len(matches) != 1 || matches[0] == "the_void" {
					t.Fatalf("point%v meaningful actual biome want1 nonvoid got%v", p, matches)
				}
				actual := matches[0]
				t.Logf("seed%d point%v biome%s", seed, p, actual)
				if actual != want[i] {
					t.Errorf("point%v biome%s want native%s", p, actual, want[i])
				}
				seen[actual] = true
			}
			if len(seen) < 2 {
				t.Errorf("actual terrain samples did not establish distinct biome values: %v", seen)
			}
		})
		if !ok && leg.name == "native" {
			t.Fatal("native terrain measurement did not satisfy meaningful preconditions")
		}
	}
}
