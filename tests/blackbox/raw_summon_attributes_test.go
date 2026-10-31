//go:build blackbox

package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

// Raw summon ingress must materialize attributes, not merely echo the NBT.
// Typed command results and saved instances are paired with delayed live boxes.
func TestRawLivingSummonAttributesParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "simulation-distance": "8", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor("RawAttrRead")}, func(s *server.Server) { s.SimulationDistance = 8; s.SetOpLevel("RawAttrRead", 4) })
	defer targets.cleanup()
	cases := []struct {
		name, typ, attrs, extra, key    string
		base, value, health, tickHealth float64
		mod, operation                  string
		amount                          float64
	}{
		{"base", "horse", `[{id:"minecraft:scale",base:2d}]`, "", "scale", 2, 2, 53, 53, "", "", 0},
		{"add", "horse", `[{id:"minecraft:scale",base:1d,modifiers:[{id:"rawpin:add",amount:1d,operation:"add_value"}]}]`, "", "scale", 1, 2, 53, 53, "rawpin:add", "add_value", 1},
		{"base_mod", "horse", `[{id:"minecraft:scale",base:2d,modifiers:[{id:"rawpin:base",amount:0.5d,operation:"add_multiplied_base"}]}]`, "", "scale", 2, 3, 53, 53, "rawpin:base", "add_multiplied_base", .5},
		{"total_mod", "horse", `[{id:"minecraft:scale",base:2d,modifiers:[{id:"rawpin:total",amount:0.5d,operation:"add_multiplied_total"}]}]`, "", "scale", 2, 3, 53, 53, "rawpin:total", "add_multiplied_total", .5},
		{"health_default", "pig", `[{id:"minecraft:max_health",base:40d}]`, "", "max_health", 40, 40, 40, 40, "", "", 0},
		{"health_clamp", "pig", `[{id:"minecraft:max_health",base:40d}]`, `,Health:100f`, "max_health", 40, 40, 40, 40, "", "", 0},
		{"wild_order", "wolf", `[{id:"minecraft:max_health",base:40d}]`, "", "max_health", 8, 8, 40, 8, "", "", 0},
	}
	for _, leg := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "RawAttrRead")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			_ = c.SetDeadline(time.Time{})
			sink := newChatSinkResolvedTP(pr, pw)
			p := &scheduledProbeBB{t: t, pw: pw, sink: sink}
			p.command("gamemode creative @s")
			p.command("tp @s 0.5 162 0.5")
			p.command("tick freeze")
			p.command("forceload add 0 0 64 0")
			p.command("scoreboard objectives add RawAttrs dummy")
			query := func(cmd string, want float64, label string) {
				t.Helper()
				p.command("scoreboard players set #raw RawAttrs -2000000000")
				rows := p.command("execute store result score #raw RawAttrs run " + cmd)
				marker := "RAW_QUERY_" + label
				if !structureSaidBB(p.command(fmt.Sprintf("execute if score #raw RawAttrs matches %d run say %s", int(want*1000), marker)), marker) {
					t.Errorf("%s want%g: %v", label, want, rows)
				}
			}
			health := func(target string, want float64, label string) {
				t.Helper()
				marker := "RAW_HEALTH_" + label
				if !structureSaidBB(p.command(fmt.Sprintf("execute if data entity %s {Health:%gf} run say %s", target, want, marker)), marker) {
					t.Errorf("%s health want%g", label, want)
				}
			}
			box := func(i int, offset float64, want bool, label string) {
				t.Helper()
				marker := "RAW_BOX_" + label
				x := 8.5 + 8*float64(i) + offset
				seen := structureSaidBB(p.command(fmt.Sprintf("execute if entity @e[tag=WireRaw%d,x=%g,y=162,z=0,dx=0,dy=0,dz=0] run say %s", i, x, marker)), marker)
				if seen != want {
					t.Errorf("%s box%v want%v", label, seen, want)
				}
			}
			pack := func(target, key string, wantBase float64, mod, operation string, amount float64) {
				t.Helper()
				var row map[string]any
				for _, line := range p.command("data get entity " + target + " attributes") {
					if !strings.Contains(line, "data.entity.query") {
						continue
					}
					value, ok := parseSNBT(line)
					if !ok {
						continue
					}
					list, ok := value.([]any)
					if !ok {
						continue
					}
					for _, v := range list {
						m, ok := v.(map[string]any)
						if ok && m["id"] == "minecraft:"+key {
							if row != nil {
								t.Fatal("duplicate saved attribute identity")
							}
							row = m
						}
					}
				}
				if row == nil {
					t.Errorf("%s materialized attribute %s missing", target, key)
					return
				}
				base, ok := hrNum(row["base"])
				if !ok || math.IsNaN(base) || math.IsInf(base, 0) || base != wantBase {
					t.Errorf("raw base%v want%g", row["base"], wantBase)
				}
				mods, _ := row["modifiers"].([]any)
				wantCount := 0
				if mod != "" {
					wantCount = 1
				}
				if len(mods) != wantCount {
					t.Errorf("modifiers%v want%d", mods, wantCount)
				} else if wantCount == 1 {
					m := mods[0].(map[string]any)
					got, ok := hrNum(m["amount"])
					if !ok || math.IsNaN(got) || math.IsInf(got, 0) || got != amount || m["id"] != mod || m["operation"] != operation {
						t.Errorf("permanent modifier%v", m)
					}
				}
			}
			for i, tc := range cases {
				target := fmt.Sprintf("@e[tag=WireRaw%d,limit=1]", i)
				p.command(fmt.Sprintf(`summon %s %g 162 0.5 {Tags:["WireRaw%d"],NoAI:1b,NoGravity:1b,attributes:%s%s}`, tc.typ, 8.5+8*float64(i), i, tc.attrs, tc.extra))
				p.await("if entity "+target, fmt.Sprintf("raw_admit%d", i))
				pack(target, tc.key, tc.base, tc.mod, tc.operation, tc.amount)
				query("attribute "+target+" minecraft:"+tc.key+" base get 1000", tc.base, tc.name+"_base")
				query("attribute "+target+" minecraft:"+tc.key+" get 1000", tc.value, tc.name+"_effective")
				if tc.mod != "" {
					query("attribute "+target+" minecraft:"+tc.key+" modifier value get "+tc.mod+" 1000", tc.amount, tc.name+"_modifier")
				}
				health(target, tc.health, tc.name+"_initial")
				if i == 4 {
					p.command("damage " + target + " 10 minecraft:generic")
					health(target, 30, tc.name+"_damaged")
				}
				if i < 4 {
					box(i, 1, false, tc.name+"_initial")
					box(i, 1.75, false, tc.name+"_initial_far")
				}
			}
			shulkerStepTicks(t, pw, sink, 1)
			for i, tc := range cases {
				target := fmt.Sprintf("@e[tag=WireRaw%d,limit=1]", i)
				wantHealth := tc.tickHealth
				if i == 4 {
					wantHealth = 30
				}
				health(target, wantHealth, tc.name+"_tick1")
				if i < 4 {
					box(i, 1, true, tc.name+"_tick1")
					box(i, 1.75, tc.value == 3, tc.name+"_tick1_far")
					wantY := 162.0
					if tc.value == 2 {
						wantY = 161.19999998807907
					}
					found := false
					for _, line := range p.command("data get entity " + target + " Pos") {
						if !strings.Contains(line, "data.entity.query") {
							continue
						}
						value, ok := parseSNBT(line)
						if !ok {
							continue
						}
						list, ok := value.([]any)
						if !ok || len(list) != 3 {
							continue
						}
						y, ok := hrNum(list[1])
						if !ok || math.IsNaN(y) || math.IsInf(y, 0) || math.Abs(y-wantY) > 1e-8 {
							t.Errorf("%s physical refresh Y%v want%.17g", tc.name, list[1], wantY)
						}
						found = true
					}
					if !found {
						t.Error("missing physical position receipt")
					}
				}
			}
		})
	}
}
