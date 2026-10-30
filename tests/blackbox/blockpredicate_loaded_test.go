//go:build blackbox

package blackbox

import (
	"fmt"
	"testing"

	"mc-go/internal/server"
)

func TestExecuteBlockReadinessParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative", "view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("ScheduledProbe")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.ViewDistance = 2
		s.SimulationDistance = 2
	})
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, "ScheduledProbe")
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			p := &scheduledProbeBB{t: t, pw: pw, sink: newChatSinkTP(pr, pw)}
			p.command("tick freeze")
			check := func(label, condition string, want bool) {
				marker := "BLOCK_LOADED_" + label
				lines := p.command("execute " + condition + " run say " + marker)
				got := structureSaidBB(lines, marker)
				t.Logf("CASE %s match=%v want=%v", label, got, want)
				var keys []string
				for _, line := range lines {
					if key, _, ok := componentArgs(line); ok {
						keys = append(keys, key)
					}
					if tree, ok := componentTree(line, true); ok {
						t.Logf("FEEDBACK %s %s", label, tree)
					}
				}
				if got != want {
					t.Errorf("%s match%v want%v: %s", label, got, want, condition)
				}
				if label == "cold_bad_id" {
					if len(keys) != 1 || keys[0] != "argument.block.id.invalid" {
						t.Errorf("parser failure was replaced by readiness: %v", keys)
					}
				} else if len(keys) != 0 {
					t.Errorf("runtime fork emitted feedback for %s: %v", label, keys)
				}
			}
			check("cold_air_if", "if block 4008 -59 4 air", false)
			check("cold_air_unless", "unless block 4008 -59 4 air", false)
			check("cold_stone_if", "if block 4008 -59 4 stone", false)
			check("cold_stone_unless", "unless block 4008 -59 4 stone", false)
			check("height_if", "if block 8 1000 4 air", false)
			check("height_unless", "unless block 8 1000 4 air", false)
			check("cold_bad_id", "if block 4008 -59 4 minecraft:not_a_block", false)
			leaf := func(label, condition, wantKey string) {
				keys := []string{}
				for _, line := range p.command("execute " + condition) {
					if key, _, ok := componentArgs(line); ok {
						keys = append(keys, key)
					}
					if tree, ok := componentTree(line, true); ok {
						t.Logf("LEAF_FEEDBACK %s %s", label, tree)
					}
				}
				t.Logf("LEAF %s keys=%v", label, keys)
				if len(keys) != 1 || keys[0] != wantKey {
					t.Errorf("%s keys%v want%q", label, keys, wantKey)
				}
			}
			leaf("cold_if", "if block 4008 -59 4 air", "argument.pos.unloaded")
			leaf("cold_unless", "unless block 4008 -59 4 air", "argument.pos.unloaded")
			leaf("height_if", "if block 8 1000 4 air", "argument.pos.outofworld")
			leaf("height_unless", "unless block 8 1000 4 air", "argument.pos.outofworld")
			leaf("cold_height_if", "if block 4008 1000 4 air", "argument.pos.unloaded")
			leaf("cold_height_unless", "unless block 4008 1000 4 air", "argument.pos.unloaded")
			p.command("tp @s 4008.5 -60 5.5 180 56")
			p.moveReady(4008.6, -60, 5.5)
			p.await("if loaded 4008 -59 4", "far_entity_ready")
			p.command("setblock 4008 -59 4 oak_log strict")
			check("loaded_log_if", "if block 4008 -59 4 oak_log", true)
			check("loaded_log_unless", "unless block 4008 -59 4 oak_log", false)
			check("loaded_tag_if", "if block 4008 -59 4 #minecraft:logs", true)
			check("loaded_empty_nbt_if", "if block 4008 -59 4 oak_log{}", false)
			check("loaded_empty_nbt_unless", "unless block 4008 -59 4 oak_log{}", true)
			leaf("loaded_if", "if block 4008 -59 4 oak_log", "commands.execute.conditional.pass")
			leaf("loaded_unless", "unless block 4008 -59 4 oak_log", "commands.execute.conditional.fail")
			p.command("tp @s 8.5 -60 5.5 180 56")
			p.moveReady(8.6, -60, 5.5)
			p.await("if loaded 8 -59 4", "home_entity_ready")
			p.command("tick rate 1000")
			p.step(20)
			for _, row := range []struct{ label, predicate string }{{"literal", "oak_log"}, {"tag", "#minecraft:logs"}, {"literal_nbt", "oak_log{}"}, {"tag_nbt", "#minecraft:logs{}"}} {
				check(row.label+"_after_leave_if", fmt.Sprintf("if block 4008 -59 4 %s", row.predicate), false)
				check(row.label+"_after_leave_unless", fmt.Sprintf("unless block 4008 -59 4 %s", row.predicate), false)
				leaf(row.label+"_leaf_if", fmt.Sprintf("if block 4008 -59 4 %s", row.predicate), "argument.pos.unloaded")
				leaf(row.label+"_leaf_unless", fmt.Sprintf("unless block 4008 -59 4 %s", row.predicate), "argument.pos.unloaded")
			}
		})
	}
}
