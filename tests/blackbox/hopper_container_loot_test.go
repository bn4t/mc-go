//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type hlCase struct {
	kind         string
	sulfur, push bool
	p, h         world.Pos
	index        int
}
type hlItem struct {
	name  string
	count int
}

func hlPos(p world.Pos) string  { return fmt.Sprintf("%d %d %d", p.X, p.Y, p.Z) }
func hlWorld(cmd string) string { return "execute in minecraft:overworld run " + cmd }
func hlBoundary(t *testing.T, push bool) (world.Pos, world.Pos) {
	return hlBoundaryWhere(t, push, false)
}

func hlBoundaryWhere(t *testing.T, push, acrossCell bool) (world.Pos, world.Pos) {
	t.Helper()
	biome := func(p world.Pos) string {
		return worldgen.ZoomBiome(12345, int32(p.X), int32(p.Y), int32(p.Z), func(x, y, z int32) string {
			if x == 2 && y == -15 && z == 2 {
				return "sulfur_caves"
			}
			return "plains"
		})
	}
	for x := 4; x < 20; x++ {
		for y := -62; y < -49; y++ {
			for z := 4; z < 20; z++ {
				p := world.Pos{X: x, Y: y, Z: z}
				h := p
				if push {
					h.X--
				} else {
					h.Y--
				}
				// Crossing a quart cell also makes command-biome witnesses
				// independent of the server's coarser legacy query path.
				if acrossCell && p.X>>2 == h.X>>2 && p.Y>>2 == h.Y>>2 && p.Z>>2 == h.Z>>2 {
					continue
				}
				if biome(p) == "sulfur_caves" && biome(h) == "plains" {
					return p, h
				}
			}
		}
	}
	t.Fatal("no adjacent biome boundary")
	return world.Pos{}, world.Pos{}
}
func hlCases(t *testing.T) []hlCase {
	var cases []hlCase
	for _, kind := range []string{"chest", "barrel"} {
		for _, sulfur := range []bool{false, true} {
			for _, push := range []bool{false, true} {
				p, h := hlBoundary(t, push)
				cases = append(cases, hlCase{kind, sulfur, push, p, h, len(cases)})
			}
		}
	}
	return cases
}
func hlItems(items map[int]hlItem) string {
	var rows []string
	for slot := 0; slot < 27; slot++ {
		if item, ok := items[slot]; ok {
			rows = append(rows, fmt.Sprintf(`{Slot:%db,id:"minecraft:%s",count:%d}`, slot, item.name, item.count))
		}
	}
	return "[" + strings.Join(rows, ",") + "]"
}
func hlBag(c hlCase) map[int]hlItem {
	bag := map[int]hlItem{1: {"iron_pickaxe", 1}, 3: {"activator_rail", 1}, 5: {"gold_ingot", 1}, 6: {"rail", 2}, 8: {"torch", 1}, 9: {"rail", 2}, 12: {"iron_ingot", 2}, 17: {"rail", 1}, 18: {"torch", 10}, 19: {"iron_ingot", 1}, 20: {"gold_ingot", 2}, 23: {"iron_ingot", 1}, 24: {"iron_ingot", 1}, 25: {"rail", 1}, 26: {"activator_rail", 2}}
	if c.sulfur {
		bag = map[int]hlItem{3: {"iron_ingot", 2}, 5: {"rail", 1}, 6: {"rail", 2}, 8: {"iron_pickaxe", 1}, 9: {"torch", 4}, 12: {"rail", 5}, 17: {"gold_ingot", 1}, 18: {"torch", 7}, 19: {"rail", 2}, 20: {"iron_ingot", 1}, 23: {"iron_ingot", 1}, 24: {"iron_ingot", 1}, 26: {"gold_ingot", 2}}
	}
	return bag
}
func hlResult(c hlCase) map[int]hlItem {
	bag := hlBag(c)
	if c.push {
		bag[0] = hlItem{"stone", 1}
	} else if c.sulfur {
		bag[3] = hlItem{"iron_ingot", 1}
	} else {
		delete(bag, 1)
	}
	return bag
}
func hlInput(c hlCase, phase string) map[int]hlItem {
	items := map[int]hlItem{}
	if phase == "disabled" && c.push {
		items[0] = hlItem{"stone", 2}
	}
	if phase == "blocked" && !c.push {
		for i := 0; i < 5; i++ {
			items[i] = hlItem{"stone", 64}
		}
	}
	if phase == "moved" {
		name := "iron_pickaxe"
		if c.sulfur {
			name = "iron_ingot"
		}
		if c.push {
			name = "stone"
		}
		items[0] = hlItem{name, 1}
	}
	return items
}
func hlSetup(c hlCase) []string {
	face := "down"
	if c.push {
		face = "east"
	}
	power := c.h
	power.Z++
	rows := []string{"fill 0 -64 0 23 -45 23 minecraft:air", "kill @e[type=minecraft:item]", "fillbiome 0 -64 0 23 -45 23 minecraft:plains"}
	if c.sulfur {
		rows = append(rows, "fillbiome 8 -60 8 11 -57 11 minecraft:sulfur_caves")
	}
	rows = append(rows, fmt.Sprintf(`setblock %s minecraft:%s{LootTable:"minecraft:chests/abandoned_mineshaft",LootTableSeed:-653164966778954613L}`, hlPos(c.p), c.kind), fmt.Sprintf(`setblock %s minecraft:hopper[facing=%s,enabled=false]{TransferCooldown:0,Items:%s}`, hlPos(c.h), face, hlItems(hlInput(c, "disabled"))), "setblock "+hlPos(power)+" minecraft:redstone_block")
	// Keep hopper load ingress separate from transfer behavior: the native
	// /setblock payload and Go's explicit /data loader are both witnessed.
	rows = append(rows, fmt.Sprintf(`data merge block %s {TransferCooldown:0,Items:%s}`, hlPos(c.h), hlItems(hlInput(c, "disabled"))))
	return rows
}
func hlPrepare(c hlCase, phase string) []string {
	if phase == "blocked" {
		power := c.h
		power.Z++
		face := "down"
		if c.push {
			face = "east"
		}
		return []string{"setblock " + hlPos(power) + " minecraft:air", fmt.Sprintf(`setblock %s minecraft:hopper[facing=%s,enabled=true]{TransferCooldown:0,Items:%s}`, hlPos(c.h), face, hlItems(hlInput(c, "blocked"))), fmt.Sprintf(`data merge block %s {TransferCooldown:0,Items:%s}`, hlPos(c.h), hlItems(hlInput(c, "blocked")))}
	}
	var rows []string
	for i := 0; i < 5; i++ {
		rows = append(rows, fmt.Sprintf("item replace block %s container.%d with minecraft:air", hlPos(c.h), i))
	}
	if c.push {
		rows = append(rows, "item replace block "+hlPos(c.h)+" container.0 with minecraft:stone 2")
	}
	return rows
}
func hlConfig(c hlCase, enabled bool) map[string]string {
	biome := "minecraft:plains"
	if c.sulfur {
		biome = "minecraft:sulfur_caves"
	}
	return map[string]string{"enabled": fmt.Sprintf("execute if block %s minecraft:hopper[enabled=%v] run say %%s", hlPos(c.h), enabled), "origin": fmt.Sprintf("execute positioned %g %g %g if biome ~ ~ ~ %s run say %%s", float64(c.p.X)+.5, float64(c.p.Y)+.5, float64(c.p.Z)+.5, biome), "hopperorigin": fmt.Sprintf("execute positioned %g %g %g if biome ~ ~ ~ minecraft:plains run say %%s", float64(c.h.X)+.5, float64(c.h.Y)+.5, float64(c.h.Z)+.5), "noitems": "execute unless entity @e[type=minecraft:item] run say %s"}
}
func hlPreChecks(c hlCase, phase string) map[string]string {
	items := hlInput(c, phase)
	if phase == "moved" {
		items = map[int]hlItem{}
		if c.push {
			items[0] = hlItem{"stone", 2}
		}
	}
	return map[string]string{"input": fmt.Sprintf(`execute if data block %s {Items:%s,TransferCooldown:0} run say %%s`, hlPos(c.h), hlItems(items)), "inputcount": fmt.Sprintf("execute store result score #input hl run data get block %s Items\nexecute if score #input hl matches %d run say %%s", hlPos(c.h), len(items))}
}

func hlChecks(c hlCase, phase string) map[string]string {
	rows := map[string]string{}
	if phase != "moved" {
		rows["latent"] = fmt.Sprintf(`execute if data block %s {LootTable:"minecraft:chests/abandoned_mineshaft",LootTableSeed:-653164966778954613L} unless data block %s Items run say %%s`, hlPos(c.p), hlPos(c.p))
	} else {
		bag := hlResult(c)
		rows["bag"] = fmt.Sprintf(`execute if data block %s {Items:%s} unless data block %s LootTable unless data block %s LootTableSeed run say %%s`, hlPos(c.p), hlItems(bag), hlPos(c.p), hlPos(c.p))
		rows["bagcount"] = fmt.Sprintf("execute store result score #bag hl run data get block %s Items\nexecute if score #bag hl matches %d run say %%s", hlPos(c.p), len(bag))
	}
	cooldown := 0
	if phase == "moved" {
		cooldown = 8
	}
	items := hlInput(c, phase)
	rows["hopper"] = fmt.Sprintf(`execute if data block %s {Items:%s,TransferCooldown:%d} run say %%s`, hlPos(c.h), hlItems(items), cooldown)
	rows["hoppercount"] = fmt.Sprintf("execute store result score #hopper hl run data get block %s Items\nexecute if score #hopper hl matches %d run say %%s", hlPos(c.h), len(items))
	return rows
}

func TestHopperLootPlayerFreeNative(t *testing.T) {
	jar := blackboxJar(t)
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	dir := t.TempDir()
	_, say, wait := vanillaConsole(t, jar, port, map[string]string{"level-seed": "12345", "pause-when-empty-seconds": "-1"}, nil, dir)
	defer func() {
		say("stop")
		if code := wait(); code != 0 {
			t.Errorf("native exit%d", code)
		}
	}()
	said := func(mark string) bool {
		raw, _ := os.ReadFile(filepath.Join(dir, "vanilla.log"))
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasSuffix(strings.TrimSpace(line), "[Server] "+mark) {
				return true
			}
		}
		return false
	}
	await := func(mark string) {
		deadline := time.Now().Add(30 * time.Second)
		for !said(mark) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if !said(mark) {
			t.Fatal("native marker absent", mark, vanillaLogTailText(dir))
		}
	}
	check := func(prefix string, rows map[string]string) {
		for key, cmd := range rows {
			mark := prefix + key
			for _, line := range strings.Split(fmt.Sprintf(cmd, mark), "\n") {
				say(hlWorld(line))
			}
		}
		end := prefix + "end"
		say("say " + end)
		await(end)
		for key := range rows {
			if !said(prefix + key) {
				t.Fatalf("native check%s absent: %s", prefix+key, vanillaLogTailText(dir))
			}
		}
	}
	step := func(mark string) {
		say("execute store result score #before hl run time query gametime")
		say("tick step 1")
		deadline := time.Now().Add(30 * time.Second)
		for !said(mark) && time.Now().Before(deadline) {
			say("execute store result score #now hl run time query gametime")
			say("scoreboard players operation #delta hl = #now hl")
			say("scoreboard players operation #delta hl -= #before hl")
			say("execute if score #delta hl matches 1 run say " + mark)
			say("execute if score #delta hl matches 2.. run say " + mark + "OVER")
			time.Sleep(50 * time.Millisecond)
		}
		await(mark)
		if said(mark + "OVER") {
			t.Fatal("native tick overrun")
		}
	}
	for _, cmd := range []string{"tick freeze", "gamerule minecraft:spawn_mobs false", "gamerule minecraft:block_drops false", "scoreboard objectives add hl dummy", "forceload add 0 0 31 31"} {
		say(cmd)
	}
	deadline := time.Now().Add(60 * time.Second)
	for !said("HLLOADED") && time.Now().Before(deadline) {
		say("execute if loaded 0 -60 0 if loaded 31 -60 31 run say HLLOADED")
		time.Sleep(50 * time.Millisecond)
	}
	await("HLLOADED")
	for _, c := range hlCases(t) {
		t.Logf("native player-free kind%s sulfur%v push%v container%v hopper%v", c.kind, c.sulfur, c.push, c.p, c.h)
		for _, cmd := range hlSetup(c) {
			say(hlWorld(cmd))
		}
		prefix := fmt.Sprintf("HL%d", c.index)
		check(prefix+"config", hlConfig(c, false))
		for _, phase := range []string{"disabled", "blocked", "moved"} {
			if phase != "disabled" {
				for _, cmd := range hlPrepare(c, phase) {
					say(hlWorld(cmd))
				}
				check(prefix+phase+"config", hlConfig(c, true))
			}
			check(prefix+phase+"before", hlPreChecks(c, phase))
			say("execute unless entity @a run say " + prefix + phase + "NOPLAYERS")
			step(prefix + phase + "STEP")
			await(prefix + phase + "NOPLAYERS")
			check(prefix+phase, hlChecks(c, phase))
		}
	}
}

func TestHopperContainerBiomeLootParity(t *testing.T) {
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor("HopperLoot")}
	bodies := map[string]string{"pack.mcmeta": `{"pack":{"description":"hopper loot credit","min_format":107,"max_format":107}}`, "data/hopperloot/advancement/open.json": `{"criteria":{"opened":{"trigger":"minecraft:player_generates_container_loot","conditions":{"loot_table":"minecraft:chests/abandoned_mineshaft"}}}}`}
	for name, body := range bodies {
		rel := "datapacks/hopperloot/" + name
		files["world/"+rel] = body
		path := filepath.Join(directory, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	targets := requireBlackboxFiles(t, map[string]string{"level-seed": "12345"}, files, func(s *server.Server) { s.WorldDir = directory; s.WorldSeed = 12345 })
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "HopperLoot")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			barrier := func() []string {
				serial++
				mark := fmt.Sprintf("HLEND%d", serial)
				sendCommand(t, pw, "say "+mark)
				return waitForExactSayBB(t, sink, mark, 30*time.Second)
			}
			check := func(checkT *testing.T, prefix string, rows map[string]string) {
				checkT.Helper()
				for key, cmd := range rows {
					for _, line := range strings.Split(fmt.Sprintf(cmd, prefix+key), "\n") {
						sendCommand(t, pw, hlWorld(line))
					}
				}
				lines := barrier()
				for key := range rows {
					if !structureSaidBB(lines, prefix+key) {
						checkT.Errorf("check%s absent", prefix+key)
					}
				}
			}
			credit := func(checkT *testing.T, want bool) {
				checkT.Helper()
				predicate := "false"
				if want {
					predicate = "true"
				}
				sendCommand(t, pw, "execute if entity @s[advancements={hopperloot:open="+predicate+"}] run say HLCREDIT")
				if !structureSaidBB(barrier(), "HLCREDIT") {
					checkT.Fatalf("actual loot advancement want%v", want)
				}
			}
			for _, cmd := range []string{"tick freeze", "gamerule minecraft:spawn_mobs false", "gamerule minecraft:block_drops false", "gamemode creative @s", "scoreboard objectives add hl dummy", "execute in minecraft:overworld run forceload add 0 0 31 31", "execute in minecraft:the_nether run tp @s 100.5 70.5 100.5"} {
				sendCommand(t, pw, cmd)
			}
			barrier()
			sendCommand(t, pw, "execute if dimension minecraft:the_nether if entity @s[x=100.5,y=70.5,z=100.5,distance=..0.01] run say HLFAROBSERVER")
			if !structureSaidBB(barrier(), "HLFAROBSERVER") {
				t.Fatal("actual far observer absent")
			}
			credit(t, false)
			cases := hlCases(t)
			for i := range cases {
				cases[i].p, cases[i].h = hlBoundaryWhere(t, cases[i].push, true)
			}
			for _, c := range cases {
				t.Run(fmt.Sprintf("%s/sulfur%v/push%v", c.kind, c.sulfur, c.push), func(t *testing.T) {
					t.Logf("container%v hopper%v", c.p, c.h)
					for _, cmd := range hlSetup(c) {
						sendCommand(t, pw, hlWorld(cmd))
					}
					prefix := fmt.Sprintf("HLC%d", c.index)
					check(t, prefix+"config", hlConfig(c, false))
					for _, phase := range []string{"disabled", "blocked", "moved"} {
						if phase != "disabled" {
							for _, cmd := range hlPrepare(c, phase) {
								sendCommand(t, pw, hlWorld(cmd))
							}
							check(t, prefix+phase+"config", hlConfig(c, true))
						}
						check(t, prefix+phase+"before", hlPreChecks(c, phase))
						shulkerStepTicks(t, pw, sink, 1)
						check(t, prefix+phase, hlChecks(c, phase))
						credit(t, false)
					}
				})
			}
			useSequence := int32(0)
			open := func(pos world.Pos, want map[int]hlItem) {
				sendCommand(t, pw, fmt.Sprintf("execute in minecraft:overworld run tp @s %g %g %g", float64(pos.X)+.5, float64(pos.Y), float64(pos.Z)-1.5))
				barrier()
				if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
					t.Fatal(err)
				}
				useSequence++
				sendUseItemOn(t, pw, int32(pos.X), int32(pos.Y), int32(pos.Z), 1, useSequence)
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
								t.Fatalf("menu%d slots%d", menu, n)
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
					t.Fatal("actual menu absent", sink.health())
				}
				for i, item := range slots {
					w, filled := want[i]
					if !filled {
						if !item.Empty() {
							t.Errorf("unexpected menu slot%d %s", i, stackKey(item))
						}
						continue
					}
					id, _ := world.ItemID(w.name)
					if item.ID != id || int(item.Count) != w.count {
						t.Errorf("menu slot%d %s want%s/%d", i, stackKey(item), w.name, w.count)
					}
				}
				var close protocol.Buffer
				close.VarInt(window)
				pw.WritePacket(server.IDPlayCloseContainerSB, close.Bytes())
				barrier()
			}
			last := cases[len(cases)-1]
			open(last.p, hlResult(last))
			credit(t, false)
			positive := world.Pos{X: 32, Y: -60, Z: 0}
			sendCommand(t, pw, hlWorld(`setblock 32 -60 0 minecraft:chest{LootTable:"minecraft:chests/abandoned_mineshaft",LootTableSeed:-653164966778954613L}`))
			barrier()
			open(positive, hlBag(hlCase{}))
			credit(t, true)
		})
	}
}
