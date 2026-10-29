//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/world"
	"reflect"
	"strings"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Keep inventory-removal drops separate from Block.dropResources: the
// gamerule can affect those two producers differently.
func TestCommandDestroyBlockDropsRuleParity(t *testing.T) {
	names := []string{"RuleDestroyV", "RuleDestroyM"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	run := func(addr, name string) []string {
		d := newMenuLifecycleObserver(t, addr, name)
		drops := newMenuDropProbe()
		breaks := 0
		fold := func(ps []usePkt) {
			drops.fold(ps)
			for _, p := range ps {
				if p.id == server.IDPlayLevelEvent {
					r := protocol.NewPacketReader(p.data)
					event, _ := r.Int()
					if event == 2001 {
						breaks++
					}
				}
			}
		}
		command := func(c string) { sendCommand(t, d.pw, c); settleUse(t, d.sink, fold, "destroy-rule-command") }
		command("tp @s 6 -57 2 0 0")
		command("gamemode spectator @s")
		command("tick freeze")
		command("gamerule minecraft:block_drops false")
		var out []string
		for _, block := range []string{"stone", `chest{Items:[{Slot:0b,id:"minecraft:iron_ingot",count:8}]}`, `shulker_box{Items:[{Slot:0b,id:"minecraft:iron_ingot",count:8}]}`} {
			for _, verb := range []string{"setblock", "fill"} {
				command("kill @e[type=minecraft:item]")
				command("setblock 8 -61 0 air")
				command("setblock 8 -61 0 " + block)
				drops = newMenuDropProbe()
				breaks = 0
				if verb == "setblock" {
					command("setblock 8 -61 0 air destroy")
				} else {
					command("fill 8 -61 0 8 -61 0 air destroy")
				}
				entry := fmt.Sprintf("%s %s breaks=%d entities=%d drops=%s", verb, block, breaks, len(drops.spawned), drops.snapshot())
				t.Logf("%s %s", name, entry)
				out = append(out, entry)
				wantCount := 0
				wantDrops := "[]"
				if strings.HasPrefix(block, "chest{") {
					wantCount = 1
					wantDrops = fmt.Sprintf("[%d#8]", world.ItemIDs["iron_ingot"])
				}
				if len(drops.spawned) != wantCount || drops.snapshot() != wantDrops {
					t.Errorf("%s %s effects entities=%d drops=%s wanted%d %s", name, verb, len(drops.spawned), drops.snapshot(), wantCount, wantDrops)
				}
				if breaks != 1 {
					t.Errorf("%s destruction break count%d wanted1", name, breaks)
				}
			}
		}
		command("gamerule minecraft:block_drops true")
		command("tick unfreeze")
		return out
	}
	v, g := run(tg.vanilla, names[0]), run(tg.mcgo, names[1])
	if !reflect.DeepEqual(v, g) {
		t.Errorf("block_drops destruction differs: vanilla%v mcgo%v", v, g)
	}
}
