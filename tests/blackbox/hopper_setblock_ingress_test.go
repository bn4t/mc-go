//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Commands supply every input item; no item replacement/data merge repairs
// ingress before either the menu or the physical one-item transfer.
func TestHopperSetblockIngressRestartParity(t *testing.T) {
	jar := blackboxJar(t)
	const name = "HopperIngress"
	start := func(t *testing.T, kind, dir string) (string, func()) {
		port, ok := freePort()
		if !ok {
			t.Skip("loopback unavailable")
		}
		if kind == "native" {
			files := map[string]string{"ops.json": opsJSONFor(name),
				"world/datapacks/ingress/pack.mcmeta":                            `{"pack":{"description":"freeze ingress restart","min_format":107,"max_format":107}}`,
				"world/datapacks/ingress/data/minecraft/tags/function/load.json": `{"values":["ingress:load"]}`,
				"world/datapacks/ingress/data/ingress/function/load.mcfunction":  "tick freeze\n"}
			addr, say, wait := vanillaConsole(t, jar, port, map[string]string{"view-distance": "2", "simulation-distance": "2", "function-permission-level": "4"}, files, dir)
			var once sync.Once
			return addr, func() {
				once.Do(func() {
					say("stop")
					if code := wait(); code != 0 {
						t.Errorf("native stop exit%d", code)
					}
				})
			}
		}
		var core *server.Server
		addr := startMcGoCfg(t, port, func(s *server.Server) {
			core = s
			s.WorldDir = filepath.Join(dir, "world")
			s.ServerDir = dir
			s.ViewDistance = 2
			s.SimulationDistance = 2
			s.SetOpLevel(name, 4)
			if err := s.LoadWorld(); err != nil {
				t.Fatal(err)
			}
			s.SetFrozen(true)
		})
		var once sync.Once
		return addr, func() { once.Do(core.Stop) }
	}
	for _, kind := range []string{"native", "go"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			snapshot := t.TempDir()
			for boot := 0; boot < 2; boot++ {
				func() {
					dir := source
					if boot == 1 {
						dir = snapshot
					}
					addr, stop := start(t, kind, dir)
					defer stop()
					conn, pr, pw := joinPlayBlackbox(t, addr, name)
					defer conn.Close()
					awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
					sink := newChatSinkResolvedTP(pr, pw)
					serial := 0
					command := func(cmd string) []string {
						serial++
						mark := fmt.Sprintf("INGRESS_%d_%d", boot, serial)
						sendCommand(t, pw, cmd)
						sendCommand(t, pw, "say "+mark)
						return waitForExactSayBB(t, sink, mark, 30*time.Second)
					}
					expect := func(predicate, mark string) {
						if !structureSaidBB(command("execute "+predicate+" run say "+mark), mark) {
							t.Errorf("boot%d actual predicate absent: %s", boot, predicate)
						}
					}
					command("tick freeze")
					command("scoreboard objectives add ingress dummy")
					command("forceload add 0 0 31 15")
					loaded := false
					deadline := time.Now().Add(30 * time.Second)
					for !loaded && time.Now().Before(deadline) {
						loaded = structureSaidBB(command("execute if loaded 28 -60 8 run say INGRESSLOADED"), "INGRESSLOADED")
					}
					if !loaded {
						t.Fatal("container chunks not loaded")
					}
					if boot == 0 {
						command("fill 0 -63 0 31 -57 15 minecraft:air")
						for _, cmd := range []string{
							`setblock 8 -60 8 minecraft:hopper[facing=down,enabled=true]{TransferCooldown:23,Items:[{Slot:0b,id:"minecraft:stone",count:2},{Slot:4b,id:"minecraft:apple",count:3},{Slot:5b,id:"minecraft:emerald",count:11}]}`,
							`setblock 12 -60 8 minecraft:hopper[facing=east,enabled=true]{TransferCooldown:2,Items:[{Slot:0b,id:"minecraft:diamond",count:2}]}`,
							`setblock 13 -60 8 minecraft:chest{Items:[{Slot:0b,id:"minecraft:diamond",count:4}]}`,
							`setblock 16 -60 8 minecraft:hopper{TransferCooldown:11,Items:[{Slot:0b,id:"minecraft:stone",count:7}]}`,
							`setblock 16 -60 8 minecraft:hopper{TransferCooldown:0,Items:[]}`,
							`setblock 18 -60 8 minecraft:hopper{TransferCooldown:11,Items:[{Slot:0b,id:"minecraft:stone",count:7}]}`,
							`setblock 18 -60 8 minecraft:hopper{Items:[]}`,
							`setblock 20 -60 8 minecraft:chest{Items:[{Slot:0b,id:"minecraft:apple",count:3}]}`,
							`setblock 24 -60 8 minecraft:barrel{Items:[{Slot:26b,id:"minecraft:diamond",count:4}]}`,
							`setblock 28 -60 8 minecraft:furnace{Items:[{Slot:1b,id:"minecraft:coal",count:2}]}`,
						} {
							command(cmd)
						}
					}
					checkItems := func(x, cd int, items map[int]hlItem) {
						expect(fmt.Sprintf("if data block %d -60 8 {TransferCooldown:%d,Items:%s}", x, cd, hlItems(items)), fmt.Sprintf("PAYLOAD%d", x))
						command(fmt.Sprintf("execute store result score #items ingress run data get block %d -60 8 Items", x))
						expect(fmt.Sprintf("if score #items ingress matches %d", len(items)), fmt.Sprintf("COUNT%d", x))
					}
					main := map[int]hlItem{0: {"stone", 2}, 4: {"apple", 3}}
					cd, qty := 23, 2
					if boot == 1 {
						cd, qty = 21, 1
					}
					before := gameTimeOf(t, pw, sink)
					expect("if block 8 -60 8 minecraft:hopper[facing=down,enabled=true]", "MAINBLOCK")
					checkItems(8, cd, main)
					sourceCD := 2
					if boot == 1 {
						sourceCD = 8
					}
					checkItems(12, sourceCD, map[int]hlItem{0: {"diamond", qty}})
					if boot == 0 {
						checkItems(16, 0, map[int]hlItem{})
						checkItems(18, -1, map[int]hlItem{})
					} else {
						checkItems(16, 0, map[int]hlItem{})
						checkItems(18, 0, map[int]hlItem{})
					}
					for _, row := range []struct {
						x           int
						kind, items string
					}{{20, "chest", `[{Slot:0b,id:"minecraft:apple",count:3}]`}, {24, "barrel", `[{Slot:26b,id:"minecraft:diamond",count:4}]`}, {28, "furnace", `[{Slot:1b,id:"minecraft:coal",count:2}]`}} {
						expect(fmt.Sprintf("if block %d -60 8 minecraft:%s", row.x, row.kind), fmt.Sprintf("CONTROLBLOCK%d", row.x))
						expect(fmt.Sprintf("if data block %d -60 8 {Items:%s}", row.x, row.items), fmt.Sprintf("CONTROLITEMS%d", row.x))
					}
					sequence := int32(0)
					open := func(x, size, menu int, want map[int]hlItem) {
						command(fmt.Sprintf("tp @s %g -60 6.5", float64(x)+.5))
						_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
						sequence++
						sendUseItemOn(t, pw, int32(x), -60, 8, 1, sequence)
						window := int32(-1)
						gotMenu := int32(-1)
						var slots []server.ItemStack
						deadline := time.Now().Add(20 * time.Second)
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
									gotMenu, _ = r.VarInt()
								case fmt.Sprintf("%#x", server.IDPlayContainerContent):
									wid, _ := r.VarInt()
									_, _ = r.VarInt()
									n, _ := r.VarInt()
									if window <= 0 || wid != window {
										continue
									}
									if gotMenu != int32(menu) || n != int32(size+36) {
										t.Fatalf("menu%d slots%d want%d/%d", gotMenu, n, menu, size+36)
									}
									slots = make([]server.ItemStack, size)
									for i := 0; i < int(n); i++ {
										item, err := readSlotBB(r)
										if err != nil {
											t.Fatal(err)
										}
										if i < size {
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
							t.Fatal("actual container menu absent", x, sink.health())
						}
						for i, item := range slots {
							w, filled := want[i]
							if !filled {
								if !item.Empty() {
									t.Errorf("x%d extra slot%d=%s", x, i, stackKey(item))
								}
								continue
							}
							id, _ := world.ItemID(w.name)
							if item.ID != id || int(item.Count) != w.count {
								t.Errorf("x%d slot%d=%s want%s/%d", x, i, stackKey(item), w.name, w.count)
							}
						}
						var close protocol.Buffer
						close.VarInt(window)
						_ = pw.WritePacket(server.IDPlayCloseContainerSB, close.Bytes())
						command("say MENUCLOSED")
					}
					open(8, 5, 16, main)
					if after := gameTimeOf(t, pw, sink); after != before {
						t.Fatalf("frozen ingress/menu advanced%d->%d", before, after)
					}
					if boot == 0 {
						shulkerStepTicks(t, pw, sink, 1)
						checkItems(8, 22, main)
						checkItems(12, 1, map[int]hlItem{0: {"diamond", 2}})
						expect(`if data block 13 -60 8 {Items:[{Slot:0b,id:"minecraft:diamond",count:4}]}`, "BEFORETRANSFER")
						shulkerStepTicks(t, pw, sink, 1)
						checkItems(8, 21, main)
						checkItems(12, 8, map[int]hlItem{0: {"diamond", 1}})
					}
					open(12, 5, 16, map[int]hlItem{0: {"diamond", 1}})
					open(13, 27, 2, map[int]hlItem{0: {"diamond", 5}})
					if boot == 0 {
						lines := command("save-all flush")
						keys := []string{}
						for _, line := range lines {
							if strings.HasPrefix(line, fmt.Sprintf("%#x:", 0x79)) {
								root, ok := mustComponent(t, line).(map[string]any)
								if ok {
									key, _ := root["translate"].(string)
									if key == "commands.save.saving" || key == "commands.save.success" || key == "commands.save.failed" {
										keys = append(keys, key)
									}
								}
							}
						}
						if len(keys) != 2 || keys[0] != "commands.save.saving" || keys[1] != "commands.save.success" {
							t.Fatalf("completed-save feedback%v", keys)
						}
						// Snapshot the completed save while the source is live: shutdown cannot
						// supply a missing save, and post-transfer quantities prove freshness.
						copyTree(t, filepath.Join(source, "world"), filepath.Join(snapshot, "world"))
					}
				}()
			}
		})
	}
}
