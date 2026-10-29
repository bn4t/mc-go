//go:build blackbox

package blackbox

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestTrappedDoubleChestPowerParity(t *testing.T) {
	names := []string{"TrapActorV", "TrapActorM", "TrapWatchV", "TrapWatchM"}
	for _, i := range []int{0, 1} {
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
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	run := func(addr, name, watchName string) []string {
		d := newClickDriverOn(t, addr, name, "minecraft:trapped_chest", 2, nil, nil)
		d.close()
		command := func(view *clickDriver, text string) {
			sendCommand(t, view.pw, text)
			settleUse(t, view.sink, func(ps []usePkt) { view.foldPkts(ps) }, "trapped-setup")
		}
		command(d, "setblock 9 -61 0 minecraft:trapped_chest[facing=north,type=right]")
		command(d, "setblock 7 -61 0 minecraft:redstone_wire")
		command(d, "setblock 10 -61 0 minecraft:redstone_wire")
		command(d, "tick freeze")
		other := newMenuLifecycleObserver(t, addr, watchName)
		command(other, "tp @s 6 -60 2 0 0")
		var out []string
		query := 0
		probe := func(x, power int) string {
			marker := fmt.Sprintf("TRAP_Y_%d", query)
			no := fmt.Sprintf("TRAP_N_%d", query)
			query++
			cond := fmt.Sprintf("block %d -61 0 minecraft:redstone_wire[power=%d]", x, power)
			sendCommand(t, other.pw, "execute if "+cond+" run say "+marker)
			sendCommand(t, other.pw, "execute unless "+cond+" run say "+no)
			end := fmt.Sprintf("TRAP_END_%d", query)
			sendCommand(t, other.pw, "say "+end)
			var lines []string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) && !structureSaidBB(lines, end) {
				for _, p := range other.sink.take() {
					lines = append(lines, fmt.Sprintf("%#x:%s", p.id, p.data))
				}
				if !structureSaidBB(lines, end) {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if !structureSaidBB(lines, end) {
				t.Fatalf("%s power probe missing command receipt %s", name, end)
			}
			answer := "missing"
			if structureSaidBB(lines, marker) {
				answer = "yes"
			}
			if structureSaidBB(lines, no) {
				answer = "no"
			}
			if answer != "yes" {
				t.Errorf("%s wire x%d wanted power%d: %s", name, x, power, answer)
			}
			return answer
		}
		sample := func(tag string, power int) {
			out = append(out, fmt.Sprintf("%s expected=%d left=%s right=%s", tag, power, probe(7, power), probe(10, power)))
		}
		d.reopen(8, -61, 0, 50, "trapped-first")
		sample("one-viewer", 1)
		other.reopen(9, -61, 0, 51, "trapped-second")
		sample("two-viewers", 2)
		other.close()
		sample("one-after-close", 1)
		command(other, "gamemode spectator @s")
		other.reopen(9, -61, 0, 52, "trapped-spectator")
		sample("spectator-excluded", 1)
		other.close()
		d.close()
		sample("closed", 0)
		d.reopen(9, -61, 0, 53, "trapped-reopen")
		sample("reopened", 1)
		var stats protocol.Buffer
		stats.VarInt(1)
		_ = d.pw.WritePacket(server.IDPlayClientStatusSB, stats.Bytes())
		opened := int32(0)
		settleUse(t, d.sink, func(ps []usePkt) {
			for _, p := range ps {
				if p.id != 0x03 {
					continue
				}
				r := protocol.NewPacketReader(p.data)
				n, _ := r.VarInt()
				for range n {
					cat, _ := r.VarInt()
					id, _ := r.VarInt()
					value, _ := r.VarInt()
					if cat == 8 && id == int32(server.CustomStatIDs["trigger_trapped_chest"]) {
						opened = value
					}
				}
			}
		}, "trapped-stat")
		if opened != 3 {
			t.Errorf("%s trapped trigger statistic=%d want3", name, opened)
		}
		out = append(out, fmt.Sprintf("triggered=%d", opened))
		command(other, "kick "+name)
		sample("disconnect", 0)
		command(other, "tick unfreeze")
		return out
	}
	v, g := run(tg.vanilla, names[0], names[2]), run(tg.mcgo, names[1], names[3])
	t.Logf("vanilla=%v mcgo=%v", v, g)
	if !reflect.DeepEqual(v, g) {
		t.Error("trapped chest opener power differs")
	}
}
