//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"mc-go/internal/server"
)

func TestAdvancementUnattachedTraversalParity(t *testing.T) {
	names := []string{"UnattachedV", "UnattachedM"}
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(names...)}
	bodies := map[string]string{
		"pack.mcmeta":                             `{"pack":{"description":"unattached advancement commands","min_format":107,"max_format":107}}`,
		"data/unattached/advancement/a.json":      `{"parent":"unattached:b","criteria":{"manual":{"trigger":"minecraft:impossible"}},"rewards":{"experience":2}}`,
		"data/unattached/advancement/b.json":      `{"parent":"unattached:a","criteria":{"manual":{"trigger":"minecraft:impossible"}},"rewards":{"experience":3}}`,
		"data/unattached/advancement/orphan.json": `{"parent":"unattached:missing","criteria":{"manual":{"trigger":"minecraft:impossible"}},"rewards":{"experience":2}}`,
	}
	for name, body := range bodies {
		rel := "datapacks/unattached/" + name
		files["world/"+rel] = body
		path := filepath.Join(directory, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) { s.WorldDir = directory })
	defer tg.cleanup()
	run := func(addr, name string) {
		d := newMenuLifecycleObserver(t, addr, name)
		command := func(c string) { sendCommand(t, d.pw, c); settleUse(t, d.sink, func([]usePkt) {}, "unattached-command") }
		command("scoreboard objectives add tree_xp dummy")
		cases := []struct {
			cmd string
			xp  int
		}{
			{"advancement grant @s until unattached:a", 2},
			{"advancement grant @s only unattached:b", 5},
			{"advancement revoke @s through unattached:a", 5},
			{"advancement grant @s only unattached:b", 5},
			{"advancement grant @s from unattached:a", 7},
			{"advancement revoke @s from unattached:b", 7},
			{"advancement grant @s until unattached:b", 10},
			{"advancement grant @s through unattached:orphan", 12},
			{"advancement grant @s only unattached:orphan", 12},
		}
		for i, c := range cases {
			command(c.cmd)
			command("execute store result score @s tree_xp run data get entity @s XpTotal")
			yes := fmt.Sprintf("UNATTACHED_Y_%d", i)
			sendCommand(t, d.pw, fmt.Sprintf("execute if score @s tree_xp matches %d run say %s", c.xp, yes))
			sendCommand(t, d.pw, fmt.Sprintf("execute unless score @s tree_xp matches %d run say UNATTACHED_NO", c.xp))
			got := false
			settleUse(t, d.sink, func(ps []usePkt) {
				for _, p := range ps {
					if bytes.Contains(p.data, []byte(yes)) {
						got = true
					}
				}
			}, "unattached-probe")
			if !got {
				t.Errorf("%s %s wantedXP%d", name, c.cmd, c.xp)
			}
		}
	}
	run(tg.vanilla, names[0])
	run(tg.mcgo, names[1])
}
