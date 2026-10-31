//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

// Initial loaded Health can exceed the post-class maximum until a real setter
// or admitted frame. Frozen receipts keep this distinct from steady-state data.
func TestWolfFrozenLoadHealthParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "simulation-distance": "8", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor("WolfOrderRead")}, func(s *server.Server) { s.SimulationDistance = 8; s.SetOpLevel("WolfOrderRead", 4) })
	defer targets.cleanup()
	cases := []struct {
		name, path                                         string
		base, inputHealth, initial, afterDamage, afterTick float64
		owned, damage, remove                              bool
	}{
		{"raw_unowned", "raw", 40, 100, 40, 8, 8, false, true, false},
		{"raw_owned", "raw", 20, 100, 20, 20, 20, true, false, false},
		{"live_unowned", "live", 40, 100, 40, 8, 8, false, true, false},
		{"live_owned", "live", 20, 100, 20, 18, 18, true, true, false},
		{"live_unowned_frame", "live", 40, 100, 40, 40, 8, false, false, false},
		{"live_owned_no_heal", "live", 20, 100, 20, 20, 20, true, false, false},
		{"normalized_unowned", "raw", 8, 40, 8, 8, 8, false, false, false},
		{"normalized_owned", "raw", 40, 20, 20, 20, 20, true, false, false},
		{"owner_removed", "raw", 20, 100, 20, 20, 8, true, false, true},
	}
	for _, leg := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "WolfOrderRead")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			_ = c.SetDeadline(time.Time{})
			sink := newChatSinkResolvedTP(pr, pw)
			p := &scheduledProbeBB{t: t, pw: pw, sink: sink}
			p.command("gamemode creative @s")
			p.command("tp @s 0.5 162 0.5")
			p.command("tick freeze")
			p.command("forceload add 0 0 80 0")
			p.command("scoreboard objectives add WolfOrder dummy")
			owner := `[I;286331153,286331153,286331153,286331153]`
			check := func(target, label string, base, hp float64, owned bool) {
				t.Helper()
				for _, suffix := range []string{"base get 1000", "get 1000"} {
					p.command("scoreboard players set #wolf WolfOrder -2000000000")
					p.command("execute store result score #wolf WolfOrder run attribute " + target + " minecraft:max_health " + suffix)
					marker := "WOLF_MAX_" + label
					if !structureSaidBB(p.command(fmt.Sprintf("execute if score #wolf WolfOrder matches %d run say %s", int(base*1000), marker)), marker) {
						t.Errorf("%s %s want%g", label, suffix, base)
					}
				}
				marker := "WOLF_HEALTH_" + label
				if !structureSaidBB(p.command(fmt.Sprintf("execute if data entity %s {Health:%gf,attributes:[{id:\"minecraft:max_health\",base:%gd}]} run say %s", target, hp, base, marker)), marker) {
					t.Errorf("%s initial/saved Health%g base%g not observed", label, hp, base)
				}
				marker = "WOLF_OWNER_" + label
				condition := "unless data entity " + target + " Owner"
				if owned {
					condition = "if data entity " + target + " {Owner:" + owner + "}"
				}
				if !structureSaidBB(p.command("execute "+condition+" run say "+marker), marker) {
					t.Errorf("%s owner condition failed", label)
				}
			}
			for i, tc := range cases {
				target := fmt.Sprintf("@e[tag=WolfWire%d,limit=1]", i)
				own := ""
				if tc.owned {
					own = ",Owner:" + owner
				}
				fields := fmt.Sprintf(`attributes:[{id:"minecraft:max_health",base:%gd}],Health:%gf%s`, tc.base, tc.inputHealth, own)
				tag := fmt.Sprintf(`{Tags:["WolfWire%d"],NoAI:1b,NoGravity:1b}`, i)
				if tc.path == "raw" {
					tag = fmt.Sprintf(`{Tags:["WolfWire%d"],NoAI:1b,NoGravity:1b,%s}`, i, fields)
				}
				p.command(fmt.Sprintf("summon wolf %g 162 0.5 %s", 8.5+8*float64(i), tag))
				p.await("if entity "+target, fmt.Sprintf("wolf_admitted%d", i))
				if tc.path == "live" {
					p.command("data merge entity " + target + " {" + fields + "}")
				}
				expectedBase := tc.base
				if !tc.owned {
					expectedBase = 8
				}
				check(target, tc.name+"_initial", expectedBase, tc.initial, tc.owned)
				if tc.remove {
					p.command("data remove entity " + target + " Owner")
					check(target, tc.name+"_removed", 8, 20, false)
				}
				if tc.damage {
					p.command("damage " + target + " 2 minecraft:generic")
					check(target, tc.name+"_damaged", expectedBase, tc.afterDamage, tc.owned)
				}
			}
			shulkerStepTicks(t, pw, sink, 1)
			for i, tc := range cases {
				base := tc.base
				owned := tc.owned
				if !owned || tc.remove {
					base = 8
					owned = false
				}
				check(fmt.Sprintf("@e[tag=WolfWire%d,limit=1]", i), tc.name+"_tick1", base, tc.afterTick, owned)
			}
		})
	}
}
