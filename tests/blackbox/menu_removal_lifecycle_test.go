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

type menuDropProbe struct {
	spawned map[int32]bool
	stacks  map[int32]string
}

func newMenuDropProbe() *menuDropProbe { return &menuDropProbe{map[int32]bool{}, map[int32]string{}} }
func (p *menuDropProbe) fold(ps []usePkt) {
	itemType, _ := server.EntityTypeID("item")
	for _, pkt := range ps {
		r := protocol.NewPacketReader(pkt.data)
		switch pkt.id {
		case server.IDPlaySpawnEntity:
			id, _ := r.VarInt()
			_, _ = r.UUID()
			typ, _ := r.VarInt()
			if typ == itemType {
				p.spawned[id] = true
			}
		case server.IDPlayEntityMetadata:
			id, _ := r.VarInt()
			if p.spawned[id] {
				index, _ := r.Byte()
				typ, _ := r.Byte()
				if index == 8 && typ == 7 {
					it, e := readSlotBB(r)
					if e == nil {
						p.stacks[id] = stackKey(it)
					}
				}
			}
		}
	}
}
func (p *menuDropProbe) snapshot() string {
	var out []string
	for id := range p.spawned {
		out = append(out, p.stacks[id])
	}
	sort.Strings(out)
	return fmt.Sprint(out)
}

func TestMenuRemovalLifecycleParity(t *testing.T) {
	names := []string{"MenuDV", "MenuDM", "ObserverDV", "ObserverDM", "MenuKV", "MenuKM", "ObserverKV", "ObserverKM", "MenuIV", "MenuIM", "ObserverIV", "ObserverIM", "MenuPV", "MenuPM", "ObserverPV", "ObserverPM"}
	// Keep the inventory-grid probe in the initial spawn chunks. This
	// isolates player-removal drops from chunk unload/reload tracking.
	for _, i := range []int{12, 13} {
		for c := 'a'; c <= 'p'; c++ {
			candidate := names[i] + string(c)
			hash := 0
			for _, b := range []byte(candidate) {
				hash = (31*hash + int(b)) & 15
			}
			if hash == 0 {
				names[i] = candidate
				break
			}
		}
	}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	for n, action := range []string{"death-respawn", "disconnect", "death-keep", "death-inventory"} {
		t.Run(action, func(t *testing.T) {
			run := func(addr, name, watchName string) []string {
				d := newClickDriverOn(t, addr, name, "minecraft:grindstone", 15, [][2]string{{"hotbar.0", "minecraft:iron_ingot[enchantments={sharpness:1}] 8"}, {"hotbar.1", "minecraft:gold_ingot[enchantments={sharpness:1}] 3"}, {"hotbar.2", "minecraft:stone 5"}, {"hotbar.3", "minecraft:diamond 2"}}, nil)
				if action == "death-inventory" {
					d.close()
					d.click("player-resync", 9, 0, 6, true, true)
					d.click("lift-grid", 36, 0, 0, true, false)
					d.click("player-grid", 1, 0, 0, true, false)
					d.click("player-cursor", 38, 0, 0, true, false)
				} else {
					d.click("input0", 0, 0, 2, false, false)
					d.click("input1", 1, 1, 2, false, false)
					d.click("cursor", 32, 0, 0, false, false)
				}
				var observer *clickDriver
				if action == "death-inventory" {
					observer = newMenuLifecycleObserver(t, addr, watchName)
				} else {
					observer = newClickDriverOn(t, addr, watchName, "minecraft:chest", 2, nil, nil)
					observer.close()
				}
				command := func(text string) {
					sendCommand(t, observer.pw, text)
					if !digSentinel(t, observer.pw, observer.sink, "MENU-CMD", 10*time.Second) {
						t.Fatal("menu command barrier")
					}
				}
				if action == "death-inventory" {
					command("gamemode spectator @s")
				}
				command("tick freeze")
				command(fmt.Sprintf("gamerule keep_inventory %v", action == "death-keep"))
				command("gamemode survival " + name)
				off := 0
				for _, c := range []byte(name) {
					off = (off*31 + int(c)) & 15
				}
				watchY := -60
				if action == "death-inventory" {
					watchY = -57
				}
				command(fmt.Sprintf("tp @s %d %d 2 0 0", 6+32*off, watchY))
				settleUse(t, observer.sink, func([]usePkt) {}, "observer-ready")
				drops := newMenuDropProbe()
				var out []string
				if action != "disconnect" {
					sendCommand(t, observer.pw, "kill "+name)
					settleUse(t, observer.sink, drops.fold, "menu-death")
					settleUse(t, d.sink, func(ps []usePkt) { d.foldPkts(ps) }, "actor-death")
					t.Logf("%s death entities=%v", name, drops.stacks)
					out = append(out, fmt.Sprintf("death drops=%s closed=%v cursor=%s", drops.snapshot(), d.sawClose, d.snap.carried))
					var req protocol.Buffer
					req.VarInt(0)
					_ = d.pw.WritePacket(server.IDPlayClientStatusSB, req.Bytes())
					settleUse(t, d.sink, func(ps []usePkt) { d.foldPkts(ps) }, "actor-respawn")
					settleUse(t, observer.sink, drops.fold, "menu-respawn")
					t.Logf("%s respawn entities=%v", name, drops.stacks)
					out = append(out, "respawn drops="+drops.snapshot())
					d.win = -1
					d.click("respawn-inventory", 9, 0, 6, true, true)
					out = append(out, fmt.Sprintf("inventory=%v cursor=%s", d.snap.p0, d.snap.carried))
				} else {
					sendCommand(t, observer.pw, "kick "+name)
					settleUse(t, observer.sink, drops.fold, "menu-disconnect")
					out = append(out, "disconnect drops="+drops.snapshot())
				}
				command("gamerule keep_inventory false")
				command("tick unfreeze")
				return out
			}
			v, g := run(tg.vanilla, names[n*4], names[n*4+2]), run(tg.mcgo, names[n*4+1], names[n*4+3])
			t.Logf("vanilla=%v mcgo=%v", v, g)
			if !reflect.DeepEqual(v, g) {
				t.Error("menu removal lifecycle differs")
			}
		})
	}
}

// An observer needs no menu or block placement. Its initial chunk window
// remains loaded throughout the nearby inventory-grid removal probe.
func newMenuLifecycleObserver(t *testing.T, addr, name string) *clickDriver {
	t.Helper()
	conn, pr, pw := joinPlayBlackbox(t, addr, name)
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Time{})
	d := &clickDriver{t: t, name: name, pw: pw, sink: newUseClient(pr, pw, true), snap: clickSnap{carried: "-", menu: map[int32]string{}, p0: map[int32]string{}}}
	sendClientInformation(t, pw)
	if !digSentinel(t, pw, d.sink, "MENU-OBSERVER-READY", 30*time.Second) {
		t.Fatal("observer ready")
	}
	_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
	return d
}
