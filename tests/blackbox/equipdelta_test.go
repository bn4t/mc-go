//go:build blackbox

package blackbox

import (
	"fmt"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestEquipDeltaParity pins vanilla's detectEquipmentUpdates on world
// entities: an /item write emits a set_equipment packet carrying ONLY
// the slot that changed (a no-op write sends nothing, a cleared slot
// goes out as an empty stack), one flush per tick. mc-go used to
// resend the whole equipment set on any change.
func TestEquipDeltaParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 22
		time.Sleep(500 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(1200 * time.Millisecond)
		sink.take()
		var out [][]string
		drain := func() []string {
			var slots []string
			for _, l := range sink.take() {
				if len(l) > 5 && l[:5] == "0x66:" {
					slots = append(slots, l)
				}
			}
			return slots
		}
		leg := func(cmd string) {
			sink.take()
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			time.Sleep(900 * time.Millisecond)
			out = append(out, drain())
		}
		// use raw packet capture: sink lines are "0x66:" + payload bytes —
		// decode manually instead since sink may not give raw bytes.
		_ = leg
		// custom capture: read packets directly for the legs
		sendCommand(t, pw, "summon minecraft:husk 0.5 -60 0.5 {NoAI:1b}")
		time.Sleep(900 * time.Millisecond)
		sink.take()
		cmds := []string{
			"item replace entity @e[type=minecraft:husk,limit=1] armor.chest with minecraft:iron_chestplate",
			"item replace entity @e[type=minecraft:husk,limit=1] armor.chest with minecraft:iron_chestplate",
			"item replace entity @e[type=minecraft:husk,limit=1] weapon.mainhand with minecraft:iron_sword",
			"item replace entity @e[type=minecraft:husk,limit=1] armor.chest with minecraft:air",
			"item replace entity @e[type=minecraft:husk,limit=1] armor.body with minecraft:leather_horse_armor",
			"item replace entity @e[type=minecraft:husk,limit=1] saddle with minecraft:saddle",
		}
		for _, cmd := range cmds {
			sink.take()
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			time.Sleep(900 * time.Millisecond)
			var evs []string
			for _, l := range sink.take() {
				if len(l) > 4 && l[:5] == "0x66:" {
					// decode slot ids + counts from the raw payload after "0x66:"
					raw := []byte(l[5:])
					r := protocol.NewPacketReader(raw)
					r.VarInt() // eid
					var parts string
					for {
						sb, err := r.Byte()
						if err != nil {
							break
						}
						more := sb&0x80 != 0
						cnt, e := r.VarInt()
						var iid int32
						if e == nil && cnt > 0 {
							iid, _ = r.VarInt()
						}
						parts += fmt.Sprintf("[%d,%d,%d]", sb&0x7f, cnt, iid)
						if !more {
							break
						}
					}
					evs = append(evs, parts)
				}
			}
			out = append(out, evs)
		}
		return out
	}
	want := run(targets.vanilla)
	got := run(targets.mcgo)
	cmds2 := []string{
		"chest write", "chest noop", "mainhand write", "chest clear",
		"body write", "saddle write",
	}
	for i := range want {
		if fmt.Sprintf("%v", want[i]) != fmt.Sprintf("%v", got[i]) {
			t.Errorf("%s: set_equipment legs vanilla %v != mc-go %v", cmds2[i], want[i], got[i])
		}
	}
	// Anchors: the chest write is one slot (4) carrying one iron
	// chestplate; the noop emits nothing at all.
	if fmt.Sprintf("%v", want[0]) != "[[4,1,995]]" || len(want[1]) != 0 {
		t.Fatalf("vanilla anchors drifted: %v %v", want[0], want[1])
	}
}
