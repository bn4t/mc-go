//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"strings"
	"testing"
	"time"
)

// The same seeded table is consumed at the actual block origin through
// /item and a fresh physical menu. The command actor lives far away in
// another dimension; native biome witnesses establish both contexts.
func TestBlockContainerBiomeLootParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("BlockBiome")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "BlockBiome")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			barrier := func() []string {
				serial++
				mark := fmt.Sprintf("BLOCKLOOTEND%d", serial)
				sendCommand(t, pw, "say "+mark)
				return waitForExactSayBB(t, sink, mark, 30*time.Second)
			}
			for _, cmd := range []string{"tick freeze", "gamemode creative @s", "gamerule minecraft:spawn_mobs false", "scoreboard objectives add blockLoot dummy", "forceload add 0 0 191 15"} {
				sendCommand(t, pw, cmd)
			}
			barrier()
			index := 0
			for _, kind := range []string{"chest", "barrel"} {
				for _, sulfur := range []bool{false, true} {
					for _, consumer := range []string{"replace", "read", "menu"} {
						command := consumer != "menu"
						t.Run(fmt.Sprintf("%s/sulfur%v/%s", kind, sulfur, consumer), func(t *testing.T) {
							x := 8 + index*16
							index++
							pos := fmt.Sprintf("%d -60 8", x)
							biome := "minecraft:plains"
							if sulfur {
								biome = "minecraft:sulfur_caves"
							}
							sendCommand(t, pw, fmt.Sprintf("fillbiome %d -64 0 %d -49 15 %s", x-8, x+7, biome))
							sendCommand(t, pw, fmt.Sprintf(`setblock %s minecraft:%s{LootTable:"minecraft:chests/abandoned_mineshaft",LootTableSeed:-653164966778954613L}`, pos, kind))
							sendCommand(t, pw, fmt.Sprintf(`execute if data block %s {LootTable:"minecraft:chests/abandoned_mineshaft",LootTableSeed:-653164966778954613L} unless data block %s Items run say BLOCKLOOTLATENT`, pos, pos))
							sendCommand(t, pw, fmt.Sprintf("execute positioned %g -59.5 8.5 if biome ~ ~ ~ %s run say BLOCKLOOTORIGIN", float64(x)+0.5, biome))
							lines := barrier()
							for _, mark := range []string{"BLOCKLOOTLATENT", "BLOCKLOOTORIGIN"} {
								if !structureSaidBB(lines, mark) {
									t.Fatalf("%s absent", mark)
								}
							}
							if command {
								sendCommand(t, pw, "execute in minecraft:the_nether run tp @s 100.5 70.5 100.5")
								ready := false
								deadline := time.Now().Add(60 * time.Second)
								for !ready && time.Now().Before(deadline) {
									sendCommand(t, pw, "execute if dimension minecraft:the_nether if entity @s[x=100.5,y=70.5,z=100.5,distance=..0.01] if loaded 100 70 100 unless biome ~ ~ ~ minecraft:sulfur_caves run say BLOCKLOOTFARACTOR")
									ready = structureSaidBB(barrier(), "BLOCKLOOTFARACTOR")
									if !ready {
										time.Sleep(20 * time.Millisecond)
									}
								}
								if !ready {
									t.Fatal("far nether actor witness absent")
								}
								if consumer == "replace" {
									sendCommand(t, pw, "execute in minecraft:overworld run item replace block "+pos+" container.26 with minecraft:stone")
								} else {
									sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:air")
									sendCommand(t, pw, "execute in minecraft:overworld run item replace entity @s hotbar.0 from block "+pos+" container.26")
									item := "minecraft:activator_rail"
									if sulfur {
										item = "minecraft:gold_ingot"
									}
									sendCommand(t, pw, fmt.Sprintf(`execute if data entity @s {Inventory:[{Slot:0b,id:"%s",count:2}]} run say BLOCKLOOTREAD`, item))
								}
								sendCommand(t, pw, "execute in minecraft:overworld if data block "+pos+" Items unless data block "+pos+" LootTable unless data block "+pos+" LootTableSeed run say BLOCKLOOTCOMMANDUNPACKED")
								receipts := barrier()
								if !structureSaidBB(receipts, "BLOCKLOOTCOMMANDUNPACKED") {
									t.Error("command did not consume loot before menu")
								}
								if consumer == "read" {
									if !structureSaidBB(receipts, "BLOCKLOOTREAD") {
										t.Error("read did not copy exact seeded source slot")
									}
									sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:air")
									barrier()
								}
							}
							sendCommand(t, pw, fmt.Sprintf("execute in minecraft:overworld run tp @s %g -60 6.5", float64(x)+0.5))
							barrier()
							if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
								t.Fatal(err)
							}
							sendUseItemOn(t, pw, int32(x), -60, 8, 1, int32(index))
							window, menu := int32(-1), int32(-1)
							var slots []server.ItemStack
							deadline := time.Now().Add(30 * time.Second)
							for slots == nil && time.Now().Before(deadline) {
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
										if window <= 0 || wid != window {
											continue
										}
										if menu != 2 || n != 63 {
											t.Fatalf("menu%d slots%d want2/63", menu, n)
										}
										slots = make([]server.ItemStack, 27)
										for i := int32(0); i < n; i++ {
											item, err := readSlotBB(r)
											if err != nil {
												t.Fatal(err)
											}
											if i < 27 {
												slots[i] = item
											}
										}
									}
								}
								if slots == nil {
									time.Sleep(10 * time.Millisecond)
								}
							}
							if slots == nil {
								t.Fatal("actual block menu absent", sink.health())
							}
							want := map[int]struct {
								name  string
								count int32
							}{1: {"iron_pickaxe", 1}, 3: {"activator_rail", 1}, 5: {"gold_ingot", 1}, 6: {"rail", 2}, 8: {"torch", 1}, 9: {"rail", 2}, 12: {"iron_ingot", 2}, 17: {"rail", 1}, 18: {"torch", 10}, 19: {"iron_ingot", 1}, 20: {"gold_ingot", 2}, 23: {"iron_ingot", 1}, 24: {"iron_ingot", 1}, 25: {"rail", 1}, 26: {"activator_rail", 2}}
							if sulfur {
								want = map[int]struct {
									name  string
									count int32
								}{3: {"iron_ingot", 2}, 5: {"rail", 1}, 6: {"rail", 2}, 8: {"iron_pickaxe", 1}, 9: {"torch", 4}, 12: {"rail", 5}, 17: {"gold_ingot", 1}, 18: {"torch", 7}, 19: {"rail", 2}, 20: {"iron_ingot", 1}, 23: {"iron_ingot", 1}, 24: {"iron_ingot", 1}, 26: {"gold_ingot", 2}}
							}
							if consumer == "replace" {
								want[26] = struct {
									name  string
									count int32
								}{"stone", 1}
							}
							var expected []string
							for slot := 0; slot < 27; slot++ {
								item := slots[slot]
								w, filled := want[slot]
								if !filled {
									if !item.Empty() {
										t.Errorf("unexpected slot%d=%s", slot, stackKey(item))
									}
									continue
								}
								id, _ := world.ItemID(w.name)
								if item.ID != id || item.Count != w.count {
									t.Errorf("slot%d=%s want%s/%d", slot, stackKey(item), w.name, w.count)
								}
								expected = append(expected, fmt.Sprintf(`{Slot:%db,id:"minecraft:%s",count:%d}`, slot, w.name, w.count))
							}
							sendCommand(t, pw, `execute if data block `+pos+` {Items:[`+strings.Join(expected, ",")+`]} unless data block `+pos+` LootTable unless data block `+pos+` LootTableSeed run say BLOCKLOOTEXACT`)
							sendCommand(t, pw, "execute store result score #filled blockLoot run data get block "+pos+" Items")
							sendCommand(t, pw, fmt.Sprintf("execute if score #filled blockLoot matches %d run say BLOCKLOOTCOUNT", len(want)))
							lines = barrier()
							for _, mark := range []string{"BLOCKLOOTEXACT", "BLOCKLOOTCOUNT"} {
								if !structureSaidBB(lines, mark) {
									t.Errorf("%s absent", mark)
								}
							}
							var close protocol.Buffer
							close.VarInt(window)
							if err := pw.WritePacket(server.IDPlayCloseContainerSB, close.Bytes()); err != nil {
								t.Fatal(err)
							}
							barrier()
						})
					}
				}
			}
		})
	}
}
