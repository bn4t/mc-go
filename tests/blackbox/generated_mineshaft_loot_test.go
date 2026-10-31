//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGeneratedMineshaftLootParity(t *testing.T) {
	generatedMineshaftLootParity(t, false)
}

func TestGeneratedMineshaftSulfurLootParity(t *testing.T) {
	generatedMineshaftLootParity(t, true)
}

func generatedMineshaftLootParity(t *testing.T, sulfur bool) {
	t.Helper()
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
	wantCargo := map[int]struct {
		name  string
		count int32
	}{1: {"iron_pickaxe", 1}, 3: {"activator_rail", 1}, 5: {"gold_ingot", 1}, 6: {"rail", 2}, 8: {"torch", 1}, 9: {"rail", 2}, 12: {"iron_ingot", 2}, 17: {"rail", 1}, 18: {"torch", 10}, 19: {"iron_ingot", 1}, 20: {"gold_ingot", 2}, 23: {"iron_ingot", 1}, 24: {"iron_ingot", 1}, 25: {"rail", 1}, 26: {"stone", 1}}
	if sulfur {
		// Real26.2 generated cart at the same exact seed, with its origin
		// biome changed to sulfur_caves before the first unpack:13 slots.
		wantCargo = map[int]struct {
			name  string
			count int32
		}{3: {"iron_ingot", 2}, 5: {"rail", 1}, 6: {"rail", 2}, 8: {"iron_pickaxe", 1}, 9: {"torch", 4}, 12: {"rail", 5}, 17: {"gold_ingot", 1}, 18: {"torch", 7}, 19: {"rail", 2}, 20: {"iron_ingot", 1}, 23: {"iron_ingot", 1}, 24: {"iron_ingot", 1}, 26: {"stone", 1}}
	}
	probe := func(t *testing.T, addr, name, objective string, materialize, openedBefore bool) {
		conn, pr, pw := joinPlayBlackbox(t, addr, name)
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkResolvedTP(pr, pw)
		cartType, _ := server.EntityTypeID("chest_minecart")
		cartID := int32(-1)
		await := func(marker string, timeout time.Duration) []string {
			lines := waitForExactSayBB(t, sink, marker, timeout)
			if id := aniSpawnID(lines, cartType, points[0].x, points[0].z); id >= 0 {
				cartID = id
			}
			return lines
		}
		for _, cmd := range []string{"tick freeze", "gamerule minecraft:spawn_mobs false", "gamemode spectator @s", "scoreboard objectives add " + objective + " dummy", "say MINESETUP"} {
			sendCommand(t, pw, cmd)
		}
		await("MINESETUP", 20*time.Second)
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
				ready = structureSaidBB(await("MINECARTEND", 30*time.Second), "MINECARTREADY")
				if !ready {
					time.Sleep(100 * time.Millisecond)
				}
			}
			if !ready {
				t.Fatalf("native generated cart%d missing", index)
			}
			if openedBefore && index == 0 {
				continue
			}
			sendCommand(t, pw, fmt.Sprintf(`execute if data entity %s {Pos:[%gd,%gd,%gd],Rotation:[0.0f,0.0f],HasTicked:1b,FlippedRotation:0b,OnGround:0b,Motion:[0.0d,0.0d,0.0d],LootTable:"minecraft:chests/abandoned_mineshaft"} unless data entity %s Items run say MINECARTDATA`, selector, point.x, point.y, point.z, selector))
			sendCommand(t, pw, "data get entity "+selector+" LootTableSeed")
			sendCommand(t, pw, "say MINEVERIFYEND")
			lines := await("MINEVERIFYEND", 30*time.Second)
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
		if !structureSaidBB(await("MINECOUNTEND", 20*time.Second), "MINECOUNTS") {
			t.Error("generated arena count want2")
		}

		selector := "@e[type=minecraft:chest_minecart,x=-85.5,y=41.5,z=-20.5,distance=..0.01,limit=1]"
		if sulfur && !openedBefore {
			// Observe the actual container's biome and an admitted far command
			// actor in a different dimension before triggering unpack.
			sendCommand(t, pw, "fillbiome -96 32 -32 -73 51 -9 minecraft:sulfur_caves")
			sendCommand(t, pw, "execute in minecraft:the_nether run tp @s 100.5 70.5 100.5")
			ready := false
			deadline := time.Now().Add(60 * time.Second)
			for !ready && time.Now().Before(deadline) {
				sendCommand(t, pw, "execute if dimension minecraft:the_nether if entity @s[x=100.5,y=70.5,z=100.5,distance=..0.01] if loaded 100 70 100 unless biome ~ ~ ~ minecraft:sulfur_caves run say MINEOPENERNONSULFUR")
				sendCommand(t, pw, "execute in minecraft:overworld positioned -85.5 41.5 -20.5 if biome ~ ~ ~ minecraft:sulfur_caves run say MINECARTSULFUR")
				sendCommand(t, pw, "say MINEORIGINEND")
				lines := await("MINEORIGINEND", 20*time.Second)
				ready = structureSaidBB(lines, "MINEOPENERNONSULFUR") && structureSaidBB(lines, "MINECARTSULFUR")
				if !ready {
					time.Sleep(20 * time.Millisecond)
				}
			}
			if !ready {
				t.Fatal("independent actual cart/opener dimension and biome witnesses absent")
			}
			sendCommand(t, pw, "execute in minecraft:overworld run item replace entity "+selector+" container.26 with minecraft:stone")
			// Rebind a fresh tracking receipt after the dimension return.
			cartID = -1
			sendCommand(t, pw, "execute in minecraft:overworld run tp @s -85.5 42.5 -20.5")
			sendCommand(t, pw, "say MINEORIGINRETURNED")
			await("MINEORIGINRETURNED", 20*time.Second)
		} else if !openedBefore {
			sendCommand(t, pw, "item replace entity "+selector+" container.26 with minecraft:stone")
		}
		var expectedItems []string
		for slot := 0; slot < 27; slot++ {
			if item, ok := wantCargo[slot]; ok {
				expectedItems = append(expectedItems, fmt.Sprintf(`{Slot:%db,id:"minecraft:%s",count:%d}`, slot, item.name, item.count))
			}
		}
		sendCommand(t, pw, `execute if data entity `+selector+` {Items:[`+strings.Join(expectedItems, ",")+`]} unless data entity `+selector+` LootTable unless data entity `+selector+` LootTableSeed run say MINELOOTEXACT`)
		sendCommand(t, pw, "execute store result score #filled "+objective+" run data get entity "+selector+" Items")
		sendCommand(t, pw, fmt.Sprintf("execute if score #filled %s matches %d run say MINELOOTCOUNT", objective, len(wantCargo)))
		sendCommand(t, pw, "say MINELOOTEND")
		lines := await("MINELOOTEND", 20*time.Second)
		for _, marker := range []string{"MINELOOTEXACT", "MINELOOTCOUNT"} {
			if !structureSaidBB(lines, marker) {
				t.Errorf("generated loot expected exact%d-slot native bag (%s): %v", len(wantCargo), marker, lines)
			}
		}
		// The generated cart must expose the same concrete bag through its
		// actual entity menu, including after the completed save/restart.
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s -85.5 42.5 -20.5")
		sendCommand(t, pw, "say MINEWINDOWREADY")
		await("MINEWINDOWREADY", 20*time.Second)
		trackingDeadline := time.Now().Add(30 * time.Second)
		for cartID < 0 && time.Now().Before(trackingDeadline) {
			if id := aniSpawnID(sink.take(), cartType, points[0].x, points[0].z); id >= 0 {
				cartID = id
			}
			if cartID < 0 {
				time.Sleep(10 * time.Millisecond)
			}
		}
		if cartID < 0 {
			t.Fatal("generated cart spawn identity missing for menu", sink.health())
		}
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		var interact protocol.Buffer
		interact.VarInt(cartID)
		interact.VarInt(0)
		interact.Byte(0)
		interact.Bool(false)
		if err := pw.WritePacket(server.IDPlayInteractSB, interact.Bytes()); err != nil {
			t.Fatal(err)
		}
		window, menu := int32(-1), int32(-1)
		var cargo []server.ItemStack
		deadline := time.Now().Add(20 * time.Second)
		for cargo == nil && time.Now().Before(deadline) {
			for _, line := range sink.take() {
				prefix, body, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				r := protocol.NewPacketReader([]byte(body))
				switch prefix {
				case fmt.Sprintf("%#x", server.IDPlayOpenScreen):
					window, _ = r.VarInt()
					menu, _ = r.VarInt()
				case fmt.Sprintf("%#x", server.IDPlayContainerContent):
					wid, _ := r.VarInt()
					_, _ = r.VarInt()
					n, _ := r.VarInt()
					if wid != window || window <= 0 {
						continue
					}
					if menu != 2 || n != 63 {
						t.Fatalf("cart menu%d slots%d want2/63", menu, n)
					}
					cargo = make([]server.ItemStack, 27)
					for slot := int32(0); slot < n; slot++ {
						item, err := readSlotBB(r)
						if err != nil {
							t.Fatal(err)
						}
						if slot < 27 {
							cargo[slot] = item
						}
					}
				}
			}
			if cargo == nil {
				time.Sleep(10 * time.Millisecond)
			}
		}
		if cargo == nil {
			t.Fatal("generated cart menu content absent")
		}

		for slot, item := range cargo {
			want, filled := wantCargo[slot]
			if !filled {
				if !item.Empty() {
					t.Errorf("menu unexpected slot%d=%v", slot, item)
				}
				continue
			}
			id, _ := world.ItemID(want.name)
			if item.ID != id || item.Count != want.count {
				t.Errorf("menu slot%d=%v want%s/%d", slot, item, want.name, want.count)
			}
		}
		var close protocol.Buffer
		close.VarInt(window)
		if err := pw.WritePacket(server.IDPlayCloseContainerSB, close.Bytes()); err != nil {
			t.Fatal(err)
		}
		sendCommand(t, pw, "save-all flush")
		sendCommand(t, pw, "say MINESAVED")
		await("MINESAVED", 30*time.Second)
	}
	t.Run("native", func(t *testing.T) { probe(t, targets.vanilla, "MineCartGen", "mineNative", false, false) })
	t.Run("go", func(t *testing.T) { probe(t, targets.mcgo, "MineCartGen", "mineGo", true, false) })
	goServer.Stop()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	addr := startMcGoCfg(t, port, configure)
	t.Run("go_native_reload", func(t *testing.T) { probe(t, addr, "MineCartReload", "mineReload", true, true) })
}
