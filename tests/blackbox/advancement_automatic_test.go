//go:build blackbox

package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestAdvancementAutomaticAndStartupOverrideParity(t *testing.T) {
	const name = "AdvAutoPin"
	bodies := map[string]string{
		"pack.mcmeta":                                                        `{"pack":{"description":"automatic advancement controls","min_format":107,"max_format":107}}`,
		"data/autopin/advancement/ticking.json":                              `{"criteria":{"clock":{"trigger":"minecraft:tick"}},"rewards":{"experience":5}}`,
		"data/minecraft/advancement/recipes/root.json":                       `{"criteria":{"root_manual":{"trigger":"minecraft:impossible"}},"rewards":{"experience":2}}`,
		"data/autopin/advancement/empty.json":                                `{"criteria":{},"rewards":{"experience":100}}`,
		"data/minecraft/advancement/recipes/decorations/crafting_table.json": `{"parent":"minecraft:recipes/root","criteria":{"manual":{"trigger":"minecraft:impossible"}},"rewards":{"experience":7}}`,
	}
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(name)}
	for path, body := range bodies {
		rel := "datapacks/autopin/" + path
		files["world/"+rel] = body
		local := filepath.Join(directory, rel)
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) { s.WorldDir = directory })
	defer tg.cleanup()
	run := func(addr string) []bool {
		conn, pr, pw := joinPlayBlackbox(t, addr, name)
		t.Cleanup(func() { conn.Close() })
		d := &clickDriver{t: t, name: name, pw: pw, sink: newUseClient(pr, pw, true, server.IDPlayUpdateAdvance)}
		sendClientInformation(t, pw)
		packets := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		stale := false
		resets := 0
		fold := func(ps []usePkt) {
			for _, p := range ps {
				if p.id == server.IDPlayUpdateAdvance {
					reset, err := protocol.NewPacketReader(p.data).Bool()
					if err != nil {
						t.Fatal(err)
					}
					if reset {
						resets++
					}
					decodePackRewardAdvancementWithOverrides(t, p.data, packets, map[string]bool{"minecraft:recipes/decorations/crafting_table": true, "minecraft:recipes/root": true})
					if _, exists := packets.prog["minecraft:recipes/decorations/crafting_table"]["unlock_right_away"]; exists {
						stale = true
					}
					if _, exists := packets.prog["minecraft:recipes/root"]["impossible"]; exists {
						stale = true
					}
				}
			}
		}
		settleUse(t, d.sink, fold, "automatic-advancement-initial")
		if stale {
			t.Errorf("%s leaked static crafting_table progress for overridden definition", addr)
		}
		command := func(c string) {
			sendCommand(t, d.pw, c)
			settleUse(t, d.sink, fold, "automatic-advancement-command")
		}
		command("tick freeze")
		command("scoreboard objectives add auto_xp dummy")
		command("scoreboard objectives add auto_recipe dummy")
		command("execute store result score @s auto_recipe run recipe give @s minecraft:crafting_table")
		sendCommand(t, d.pw, "execute if score @s auto_recipe matches 1 run say AUTO-RECIPE-NEW")
		recipeNew := false
		settleUse(t, d.sink, func(ps []usePkt) {
			fold(ps)
			for _, p := range ps {
				if structureSaidBB([]string{fmt.Sprintf("0x%x:%s", p.id, p.data)}, "AUTO-RECIPE-NEW") {
					recipeNew = true
				}
			}
		}, "automatic-startup-recipe")
		if !recipeNew {
			t.Errorf("%s overridden advancement leaked captured crafting_table recipe unlock", addr)
		}
		answers := []bool{recipeNew}
		for index, control := range []struct {
			command string
			xp      int
		}{
			{"", 5},
			{"advancement grant @s only minecraft:recipes/decorations/crafting_table", 12},
			{"advancement grant @s only minecraft:recipes/decorations/crafting_table", 12},
			{"advancement revoke @s only autopin:ticking", 17},
			{"advancement grant @s only autopin:empty", 17},
			{"advancement grant @s only autopin:ticking", 17},
			{"advancement grant @s only minecraft:recipes/root", 19},
			{"advancement grant @s only minecraft:recipes/root", 19},
		} {
			if control.command != "" {
				command(control.command)
			}
			command("execute store result score @s auto_xp run data get entity @s XpTotal")
			marker := fmt.Sprintf("AUTO_XP_%d", index)
			sendCommand(t, d.pw, fmt.Sprintf("execute if score @s auto_xp matches %d run say %s", control.xp, marker))
			got := false
			settleUse(t, d.sink, func(ps []usePkt) {
				fold(ps)
				for _, p := range ps {
					if structureSaidBB([]string{fmt.Sprintf("0x%x:%s", p.id, p.data)}, marker) {
						got = true
					}
				}
			}, "automatic-advancement-probe")
			if !got {
				t.Errorf("%s control%d %q XP wanted%d", addr, index, control.command, control.xp)
			}
			if index == 1 && !packets.prog["minecraft:recipes/decorations/crafting_table"]["manual"] {
				t.Errorf("%s replacement manual criterion progress missing", addr)
			}
			if index == 6 && !packets.prog["minecraft:recipes/root"]["root_manual"] {
				t.Errorf("%s replacement root criterion missing", addr)
			}
			answers = append(answers, got)
		}
		if stale {
			t.Errorf("%s leaked captured startup criteria", addr)
		}
		if resets != 1 {
			t.Errorf("%s initial advancement reset packets%d wanted1", addr, resets)
		}
		return answers
	}
	v, g := run(tg.vanilla), run(tg.mcgo)
	if !reflect.DeepEqual(v, g) {
		t.Errorf("automatic advancement checks differ vanilla%v mcgo%v", v, g)
	}
}
