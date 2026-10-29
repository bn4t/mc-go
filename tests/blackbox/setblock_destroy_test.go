//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// Commands are issued while frozen so destruction drops cannot merge or be
// collected. Scoreboard stores pin the command result independently of state.
func TestSetblockDestroyModesParity(t *testing.T) {
	names := []string{"DestroyProbeV", "DestroyProbeM"}
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
					id, _ := r.Int()
					if id == 2001 {
						breaks++
					}
				}
			}
		}
		command := func(c string) { sendCommand(t, d.pw, c); settleUse(t, d.sink, fold, "destroy-command") }
		command("tp @s 6 -57 2 0 0")
		command("gamemode spectator @s")
		command("tick freeze")
		command("scoreboard objectives add destroy_result dummy")
		command("scoreboard objectives add destroy_success dummy")
		var out []string
		query := 0
		probe := func(condition string) bool {
			query++
			yes := fmt.Sprintf("DESTROY_Y_%d", query)
			sendCommand(t, d.pw, "execute if "+condition+" run say "+yes)
			sendCommand(t, d.pw, "execute unless "+condition+" run say DESTROY_N")
			got := false
			settleUse(t, d.sink, func(ps []usePkt) {
				fold(ps)
				for _, p := range ps {
					if bytes.Contains(p.data, []byte(yes)) {
						got = true
					}
				}
			}, "destroy-probe")
			return got
		}
		cases := []struct {
			name, setup, cmd, state string
			result                  int
		}{
			{"replace-air", "air", "stone", "stone", 1},
			{"same-replace", "stone", "stone replace", "stone", 0},
			{"keep-occupied", "stone", "gold_block keep", "stone", 0},
			{"keep-air", "air", "gold_block keep", "gold_block", 1},
			{"replace-inventory", `chest{Items:[{Slot:0b,id:"minecraft:iron_ingot",count:8}]}`, "stone", "stone", 1},
			{"destroy-same-inventory", `chest{Items:[{Slot:0b,id:"minecraft:iron_ingot",count:8}]}`, "chest destroy", "chest", 1},
			{"destroy-inventory", `chest{Items:[{Slot:0b,id:"minecraft:iron_ingot",count:8}]}`, "air destroy", "air", 1},
			{"destroy-air", "air", "air destroy", "air", 1},
			{"destroy-stone", "stone", "stone destroy", "stone", 1},
			{"fill-destroy-same", `chest{Items:[{Slot:0b,id:"minecraft:iron_ingot",count:8}]}`, "fill chest destroy", "chest", 1},
			{"fill-destroy-air", `chest{Items:[{Slot:0b,id:"minecraft:iron_ingot",count:8}]}`, "fill air destroy", "air", 1},
			{"strict-powered-tnt", "air", "tnt strict", "tnt", 1},
			{"normal-powered-tnt", "air", "tnt", "air", 1},
		}
		for _, c := range cases {
			command("kill @e[type=minecraft:item]")
			command("setblock 8 -61 0 air")
			command("setblock 8 -61 0 " + c.setup)
			command("setblock 9 -61 0 redstone_block")
			drops = newMenuDropProbe()
			breaks = 0
			cmd := "setblock 8 -61 0 " + c.cmd
			if strings.HasPrefix(c.cmd, "fill ") {
				cmd = "fill 8 -61 0 8 -61 0 " + strings.TrimPrefix(c.cmd, "fill ")
			}
			command("execute store result score @s destroy_result store success score @s destroy_success run " + cmd)
			resultOK := probe(fmt.Sprintf("score @s destroy_result matches %d", c.result))
			successOK := probe(fmt.Sprintf("score @s destroy_success matches %d", c.result))
			stateOK := probe("block 8 -61 0 " + c.state)
			wantBreaks := 0
			var wantDrops []string
			if strings.Contains(c.name, "destroy") && c.setup != "air" {
				wantBreaks = 1
				if c.setup == "stone" {
					wantDrops = []string{fmt.Sprintf("%d#1", world.ItemIDs["cobblestone"])}
				} else {
					wantDrops = []string{fmt.Sprintf("%d#1", world.ItemIDs["chest"]), fmt.Sprintf("%d#8", world.ItemIDs["iron_ingot"])}
				}
			}
			sort.Strings(wantDrops)
			if breaks != wantBreaks || drops.snapshot() != fmt.Sprint(wantDrops) {
				t.Errorf("%s %s effects: breaks=%d want%d drops=%s want%v", name, c.name, breaks, wantBreaks, drops.snapshot(), wantDrops)
			}
			if c.state == "chest" && strings.Contains(c.name, "destroy") && probe("data block 8 -61 0 Items[0]") {
				t.Errorf("%s %s retained input", name, c.name)
			}
			entry := fmt.Sprintf("%s result=%v success=%v state=%v breaks=%d drops=%s", c.name, resultOK, successOK, stateOK, breaks, drops.snapshot())
			t.Logf("%s %s", name, entry)
			out = append(out, entry)
			if !resultOK || !successOK || !stateOK {
				t.Errorf("%s command/state mismatch: %s", name, entry)
			}
		}
		command("tick unfreeze")
		return out
	}
	v, g := run(tg.vanilla, names[0]), run(tg.mcgo, names[1])
	if !reflect.DeepEqual(v, g) {
		t.Errorf("destroy modes differ: vanilla=%v mcgo=%v", v, g)
	}
}
