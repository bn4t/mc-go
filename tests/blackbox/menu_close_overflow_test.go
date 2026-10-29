//go:build blackbox

package blackbox

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestTransientCloseOverflowParity(t *testing.T) {
	names := []string{"CloseGV", "CloseGM", "CloseCV", "CloseCM"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	for n, tc := range []struct {
		block       string
		menu        int32
		size, first int
	}{{"grindstone", 15, 3, 0}, {"crafting_table", 12, 10, 1}} {
		t.Run(tc.block, func(t *testing.T) {
			run := func(addr, name string) string {
				d := newClickDriverOn(t, addr, name, "minecraft:"+tc.block, tc.menu, [][2]string{{"hotbar.0", "minecraft:iron_ingot[enchantments={sharpness:1}] 8"}, {"hotbar.1", "minecraft:gold_ingot[enchantments={sharpness:1}] 3"}, {"hotbar.2", "minecraft:stone 5"}}, nil)
				d.click("lift-input0", int16(tc.size+27), 0, 0, false, false)
				d.click("input0", int16(tc.first), 0, 0, false, false)
				d.click("lift-input1", int16(tc.size+28), 0, 0, false, false)
				d.click("input1", int16(tc.first+1), 0, 0, false, false)
				d.click("cursor", int16(tc.size+29), 0, 0, false, false)
				sendCommand(t, d.pw, "gamemode survival @s")
				sendCommand(t, d.pw, "tick freeze")
				for slot := 0; slot < 9; slot++ {
					sendCommand(t, d.pw, fmt.Sprintf("item replace entity @s hotbar.%d with minecraft:stone 64", slot))
				}
				for slot := 0; slot < 27; slot++ {
					sendCommand(t, d.pw, fmt.Sprintf("item replace entity @s inventory.%d with minecraft:stone 64", slot))
				}
				sendCommand(t, d.pw, "item replace entity @s hotbar.0 with minecraft:stone 60")
				sendCommand(t, d.pw, "item replace entity @s hotbar.1 with minecraft:stone 62")
				if !digSentinel(t, d.pw, d.sink, "CLOSE-READY", 10*time.Second) {
					t.Fatal("close setup")
				}
				d.click("close-resync", int16(tc.size), 0, 6, false, true)
				var close protocol.Buffer
				close.VarInt(d.win)
				_ = d.pw.WritePacket(server.IDPlayCloseContainerSB, close.Bytes())
				itemType, _ := server.EntityTypeID("item")
				spawned := map[int32]bool{}
				stacks := map[int32]string{}
				settleUse(t, d.sink, func(ps []usePkt) {
					d.foldPkts(ps)
					for _, p := range ps {
						r := protocol.NewPacketReader(p.data)
						switch p.id {
						case server.IDPlaySpawnEntity:
							id, _ := r.VarInt()
							_, _ = r.UUID()
							typ, _ := r.VarInt()
							if typ == itemType {
								spawned[id] = true
							}
						case server.IDPlayEntityMetadata:
							id, _ := r.VarInt()
							if spawned[id] {
								index, _ := r.Byte()
								typ, _ := r.Byte()
								if index == 8 && typ == 7 {
									it, e := readSlotBB(r)
									if e == nil {
										stacks[id] = stackKey(it)
									}
								}
							}
						}
					}
				}, "close-overflow")
				d.win = -1
				d.click("close-player", 9, 0, 6, true, true)
				var drops []string
				for id := range spawned {
					drops = append(drops, stacks[id])
				}
				sort.Strings(drops)
				var stats protocol.Buffer
				stats.VarInt(1)
				_ = d.pw.WritePacket(server.IDPlayClientStatusSB, stats.Bytes())
				droppedCount := int32(0)
				settleUse(t, d.sink, func(ps []usePkt) {
					for _, p := range ps {
						if p.id != 0x03 {
							continue
						}
						r := protocol.NewPacketReader(p.data)
						n, _ := r.VarInt()
						for range n {
							cat, _ := r.VarInt()
							_, _ = r.VarInt()
							value, _ := r.VarInt()
							if cat == 5 {
								droppedCount += value
							}
						}
					}
				}, "close-stats")
				result := fmt.Sprintf("hotbar0=%s hotbar1=%s cursor=%s drops=%v droppedStat=%d", d.snap.p0[36], d.snap.p0[37], d.snap.carried, drops, droppedCount)
				sendCommand(t, d.pw, "tick unfreeze")
				if !digSentinel(t, d.pw, d.sink, "CLOSE-DONE", 10*time.Second) {
					t.Fatal("close unfreeze")
				}
				return result
			}
			v, g := run(tg.vanilla, names[2*n]), run(tg.mcgo, names[2*n+1])
			t.Logf("vanilla=%s mcgo=%s", v, g)
			if !reflect.DeepEqual(v, g) {
				t.Error("close overflow differs")
			}
		})
	}
}

func TestMenuCloseMergePriorityParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("MergeCloseV", "MergeCloseM")}, nil)
	defer tg.cleanup()
	run := func(addr, name string) string {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, [][2]string{{"hotbar.0", "minecraft:stone 60"}, {"hotbar.1", "minecraft:stone 61"}, {"hotbar.2", "minecraft:stone 63"}, {"hotbar.3", "minecraft:stone 8"}, {"weapon.offhand", "minecraft:stone 62"}, {"inventory.0", "minecraft:stone 60"}}, nil)
		d.click("cursor", 57, 0, 0, false, false)
		var held protocol.Buffer
		held.Short(2)
		_ = d.pw.WritePacket(server.IDPlayHeldChangeSB, held.Bytes())
		d.close()
		d.click("closed-inventory", 9, 0, 6, true, true)
		return fmt.Sprintf("h0=%s h1=%s h2=%s offhand=%s main=%s cursor=%s", d.snap.p0[36], d.snap.p0[37], d.snap.p0[38], d.snap.p0[45], d.snap.p0[9], d.snap.carried)
	}
	v, g := run(tg.vanilla, "MergeCloseV"), run(tg.mcgo, "MergeCloseM")
	t.Logf("vanilla=%s mcgo=%s", v, g)
	if v != g {
		t.Error("menu close merge priority differs")
	}
}
