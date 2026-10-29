//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestCopperChestViewerTransformParity(t *testing.T) {
	names := []string{"ToolViewWV", "ToolViewWM", "ToolViewSV", "ToolViewSM"}
	for i := range names {
		for c := 'a'; c <= 'p'; c++ {
			candidate := names[i] + string(c)
			off := 0
			for _, b := range []byte(candidate) {
				off = (31*off + int(b)) & 15
			}
			if off == 0 {
				names[i] = candidate
				break
			}
		}
	}
	actors := []string{"ToolDoWV", "ToolDoWM", "ToolDoSV", "ToolDoSM"}
	ops := append(append([]string{}, names...), actors...)
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(ops...)}, nil)
	defer tg.cleanup()
	for phase, tc := range []struct{ name, block, item, next string }{{"wax", "copper_chest", "honeycomb", "waxed_copper_chest"}, {"scrape", "weathered_copper_chest", "diamond_axe", "exposed_copper_chest"}} {
		run := func(addr, viewer, actor string) string {
			d := newClickDriverOn(t, addr, viewer, tc.block+`{CustomName:"Cargo",Items:[{Slot:0b,id:"minecraft:diamond",count:7}]}`, 2, nil, nil)
			if d.snap.menu[0] != "926#7" {
				t.Fatalf("%s original menu cargo=%v", addr, d.snap.menu)
			}
			other := newMenuLifecycleObserver(t, addr, actor)
			command := func(text, tag string) []usePkt {
				sendCommand(t, other.pw, text)
				sendCommand(t, other.pw, "say "+tag)
				return collectToolUseMarkerBB(t, other.sink, tag)
			}
			command("gamemode creative @s", "VIEWCREATIVE")
			command("tick freeze", "VIEWFROZEN")
			command("tp @s 6 -60 -2 0 0", "VIEWPOSITION")
			command("item replace entity @s weapon.offhand with "+tc.item, "VIEWHELD")
			sendCommand(t, other.pw, "execute if block 8 -61 0 "+tc.block+` if data block 8 -61 0 {CustomName:"Cargo",Items:[{Slot:0b,id:"minecraft:diamond",count:7}]} run say VIEWBEFORE`)
			collectToolUseMarkerBB(t, other.sink, "VIEWBEFORE")
			d.foldPkts(d.sink.take())
			d.sawClose = false
			var b protocol.Buffer
			b.VarInt(1)
			b.Position(8, -61, 0)
			b.VarInt(1)
			b.Float(.5)
			b.Float(1)
			b.Float(.5)
			b.Bool(false)
			b.Bool(false)
			b.VarInt(8800 + int32(phase))
			if err := other.pw.WritePacket(server.IDPlayUseItemOnSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			acked := false
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) && !acked {
				for _, p := range other.sink.take() {
					if p.id == server.IDPlayBlockChangedAck {
						id, e := protocol.NewPacketReader(p.data).VarInt()
						acked = e == nil && id == 8800+int32(phase)
					}
				}
				if !acked {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if !acked {
				t.Fatal("transform original click missing acknowledgement")
			}
			sendCommand(t, other.pw, "execute if block 8 -61 0 "+tc.next+` if data block 8 -61 0 {CustomName:"Cargo",Items:[{Slot:0b,id:"minecraft:diamond",count:7}]} run say VIEWTRANSFORMED`)
			collectToolUseMarkerBB(t, other.sink, "VIEWTRANSFORMED")
			d.foldPkts(d.sink.take())
			d.click("viewer-pickup", 0, 0, 0, false, false)
			kept := !d.sawClose && d.snap.carried == "926#7"
			if !kept {
				t.Errorf("%s %s preserved menu close=%v cursor=%s", addr, tc.name, d.sawClose, d.snap.carried)
			}
			if kept {
				d.click("viewer-return", 0, 0, 0, false, false)
			}
			command("setblock 8 -61 0 stone", "VIEWREMOVED")
			command(`setblock 8 -61 0 copper_chest{CustomName:"Replacement",Items:[{Slot:0b,id:"minecraft:diamond",count:7}]}`, "VIEWREPLACED")
			sendCommand(t, d.pw, "say VIEWRETIRED")
			d.foldPkts(collectToolUseMarkerBB(t, d.sink, "VIEWRETIRED"))
			d.click("replacement-refuses-old-menu", 0, 0, 0, false, false)
			retired := d.sawClose && d.snap.carried == "-"
			if !retired {
				t.Errorf("%s %s command replacement close=%v cursor=%s", addr, tc.name, d.sawClose, d.snap.carried)
			}
			command("tick unfreeze", "VIEWUNFROZEN")
			d.close()
			return fmt.Sprintf("kept=%v retired=%v", kept, retired)
		}
		v, g := run(tg.vanilla, names[phase*2], actors[phase*2]), run(tg.mcgo, names[phase*2+1], actors[phase*2+1])
		t.Logf("%s vanilla=%s mcgo=%s", tc.name, v, g)
		if v != "kept=true retired=true" || g != v {
			t.Errorf("%s viewer lifetime differs", tc.name)
		}
	}
}

func collectToolUseMarkerBB(t *testing.T, sink *useClient, marker string) []usePkt {
	t.Helper()
	var out []usePkt
	var lines []string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		batch := sink.take()
		out = append(out, batch...)
		for _, p := range batch {
			lines = append(lines, fmt.Sprintf("%#x:%s", p.id, p.data))
		}
		if structureSaidBB(lines, marker) {
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("missing exact observer body %s", marker)
	return nil
}
