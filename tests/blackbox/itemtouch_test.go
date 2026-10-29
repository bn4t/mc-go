package blackbox

import (
	"encoding/binary"
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Player.aiStep touches nearby items on connection ticks, even under freeze.
// Counts/removal/metadata are wire observations, not server internals.
func TestFrozenItemPickupParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("ItemTouch")}, nil)
	defer tg.cleanup()
	run := func(addr string) [][]float64 {
		c, pr, pw := joinPlayBlackbox(t, addr, "ItemTouch")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		var source, takes, removes, meta int32
		invSlots := map[int16]int32{}
		pump := func() []string {
			lines := sink.take()
			for _, line := range lines {
				prefix, data, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				id, err := strconv.ParseInt(prefix, 0, 32)
				if err != nil {
					continue
				}
				r := protocol.NewPacketReader([]byte(data))
				switch int32(id) {
				case server.IDPlayContainerSlot:
					wid, e := r.VarInt()
					_, _ = r.VarInt()
					slot, e2 := r.Short()
					it, e3 := readSlotBB(r)
					if e == nil && e2 == nil && e3 == nil && wid == 0 {
						invSlots[slot] = it.Count
					}
				case server.IDPlayContainerContent:
					wid, e := r.VarInt()
					_, _ = r.VarInt()
					n, e2 := r.VarInt()
					if e == nil && e2 == nil && wid == 0 {
						for i := int32(0); i < n; i++ {
							it, e := readSlotBB(r)
							if e != nil {
								break
							}
							invSlots[int16(i)] = it.Count
						}
					}
				case server.IDPlaySetPlayerInventory:
					slot, e := r.VarInt()
					it, e2 := readSlotBB(r)
					if e == nil && e2 == nil {
						if slot < 9 {
							slot += 36
						} else if slot == 40 {
							slot = 45
						}
						invSlots[int16(slot)] = it.Count
					}
				case server.IDPlaySpawnEntity:
					eid, e := r.VarInt()
					if e != nil {
						continue
					}
					_, _ = r.UUID()
					typ, e := r.VarInt()
					if e == nil {
						if n, ok := server.EntityTypeName(typ); ok && n == "item" {
							source = eid
						}
					}
				case server.IDPlayTakeItem:
					eid, e := r.VarInt()
					_, _ = r.VarInt()
					n, e2 := r.VarInt()
					if e == nil && e2 == nil && eid == source {
						takes += n
					}
				case server.IDPlayRemoveEntity:
					n, e := r.VarInt()
					if e == nil {
						for range n {
							eid, e := r.VarInt()
							if e == nil && eid == source {
								removes++
							}
						}
					}
				case server.IDPlayEntityMetadata:
					eid, e := r.VarInt()
					if e != nil || eid != source {
						continue
					}
					for {
						idx, e := r.Byte()
						if e != nil || idx == 255 {
							break
						}
						ser, e := r.Byte()
						if e != nil {
							break
						}
						if idx == 8 && ser == 7 {
							it, e := readSlotBB(r)
							if e == nil {
								meta = it.Count
							}
							break
						}
						if !skipMetaVal(r, ser) {
							break
						}
					}
				}
			}
			return lines
		}
		command := func(cmd string) {
			sendCommand(t, pw, cmd)
			serial++
			marker := fmt.Sprintf("TOUCH_BARRIER_%d", serial)
			sendCommand(t, pw, "say "+marker)
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range pump() {
					if strings.Contains(line, marker) {
						return
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("command timeout %s", cmd)
		}
		get := func(target, path string) float64 {
			serial++
			marker := fmt.Sprintf("TOUCH_QUERY_%d", serial)
			sendCommand(t, pw, "data get entity "+target+" "+path)
			sendCommand(t, pw, "say "+marker)
			deadline := time.Now().Add(10 * time.Second)
			reply := ""
			for time.Now().Before(deadline) {
				ended := false
				for _, line := range pump() {
					if strings.Contains(line, "data.entity.query") {
						reply = line
					}
					if strings.Contains(line, marker) {
						ended = true
					}
				}
				if ended {
					if i := strings.Index(reply, "data.entity.query"); i >= 0 {
						return lastNumberSigned(reply[:i])
					}
					return -1
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("query timeout %s %s", target, path)
			return -1
		}
		command("tick freeze")
		command("gamerule minecraft:keep_inventory true")
		command("setblock 0 69 0 stone")
		command("tp @s 0.5 70 0.5")
		reset := func(mode string) {
			command("kill @e[type=item]")
			command("gamemode " + mode)
			command("clear @s")
			pump()
			invSlots = map[int16]int32{}
			source = 0
			takes = 0
			removes = 0
			meta = -1
		}
		spawn := func(count int, extra string) {
			pump()
			source = 0
			takes = 0
			removes = 0
			meta = -1
			command(fmt.Sprintf(`summon item 0.5 70.1 0.5 {Tags:["touchcase"],NoGravity:1b,Motion:[0d,0d,0d],Item:{id:"minecraft:stone",count:%d}%s}`, count, extra))
			time.Sleep(350 * time.Millisecond)
			pump()
		}
		step := func() { command("tick step 1"); time.Sleep(200 * time.Millisecond); pump() }
		snap := func() []float64 {
			inv := float64(invSlots[36])
			if inv == 0 {
				inv = -1
			}
			left := get("@e[tag=touchcase,limit=1]", "Item.count")
			delay := get("@e[tag=touchcase,limit=1]", "PickupDelay")
			age := get("@e[tag=touchcase,limit=1]", "Age")
			off := float64(invSlots[45])
			if off == 0 {
				off = -1
			}
			return []float64{inv, left, delay, age, float64(takes), float64(removes), float64(meta), off}
		}
		fill := func() {
			for i := 0; i < 9; i++ {
				command(fmt.Sprintf("item replace entity @s hotbar.%d with minecraft:dirt 64", i))
			}
			for i := 0; i < 27; i++ {
				command(fmt.Sprintf("item replace entity @s inventory.%d with minecraft:dirt 64", i))
			}
		}
		var out [][]float64
		reset("creative")
		spawn(3, "")
		out = append(out, snap())
		reset("survival")
		spawn(3, ",PickupDelay:2s")
		out = append(out, snap())
		step()
		out = append(out, snap())
		step()
		out = append(out, snap())
		reset("survival")
		fill()
		command("item replace entity @s weapon.offhand with minecraft:dirt 64")
		command("item replace entity @s hotbar.0 with minecraft:stone 60")
		spawn(10, "")
		out = append(out, snap())
		step()
		out = append(out, snap())
		reset("survival")
		fill()
		spawn(3, "")
		step()
		out = append(out, snap())
		reset("creative")
		fill()
		spawn(3, "")
		out = append(out, snap())
		reset("survival")
		spawn(3, ",PickupDelay:-1s")
		step()
		out = append(out, snap())
		reset("survival")
		spawn(3, ",Owner:[I;0,0,0,1]")
		step()
		out = append(out, snap())
		own := server.OfflineUUID("ItemTouch")
		owner := fmt.Sprintf(",Owner:[I;%d,%d,%d,%d]", int32(binary.BigEndian.Uint32(own[0:4])), int32(binary.BigEndian.Uint32(own[4:8])), int32(binary.BigEndian.Uint32(own[8:12])), int32(binary.BigEndian.Uint32(own[12:16])))
		reset("survival")
		spawn(3, owner)
		out = append(out, snap())
		reset("spectator")
		spawn(3, "")
		step()
		out = append(out, snap())
		reset("survival")
		command("kill @s")
		spawn(3, "")
		step()
		out = append(out, snap())
		return out
	}
	want := run(tg.vanilla)
	expected := [][]float64{
		{3, -1, -1, -1, 3, 1, 3, -1}, {-1, 3, 2, 0, 0, 0, 3, -1}, {-1, 3, 1, 1, 0, 0, 3, -1}, {3, -1, -1, -1, 3, 1, 3, -1},
		{64, 6, 0, 0, 0, 0, 10, 64}, {64, 6, 0, 1, 0, 0, 10, 64}, {64, 3, 0, 1, 0, 0, 3, -1}, {64, -1, -1, -1, 3, 1, 3, -1},
		{-1, 3, -1, 1, 0, 0, 3, -1}, {-1, 3, 0, 1, 0, 0, 3, -1}, {3, -1, -1, -1, 3, 1, 3, -1}, {-1, 3, 0, 1, 0, 0, 3, -1}, {-1, 3, 0, 1, 0, 0, 3, -1},
	}
	if !reflect.DeepEqual(want, expected) {
		t.Errorf("vanilla pickup fixture changed: %v", want)
	}
	got := run(tg.mcgo)
	t.Logf("vanilla %v; mcgo %v", want, got)
	if !reflect.DeepEqual(want, got) {
		t.Fatal("item player touch differs")
	}
}
