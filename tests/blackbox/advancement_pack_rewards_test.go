//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestAdvancementPackRewardsParity(t *testing.T) {
	names := []string{"PackRewardV", "PackRewardM"}
	bodies := map[string]string{
		"pack.mcmeta":                              `{"pack":{"description":"advancement reward pin","min_format":107,"max_format":107}}`,
		"data/rewardpin/advancement/first.json":    `{"criteria":{"manual":{"trigger":"minecraft:impossible"}},"rewards":{"experience":7,"loot":["minecraft:blocks/diamond_block"],"recipes":["minecraft:oak_boat"],"function":"rewardpin:after"}}`,
		"data/rewardpin/advancement/child.json":    `{"parent":"rewardpin:first","criteria":{"manual":{"trigger":"minecraft:impossible"}},"rewards":{"experience":3}}`,
		"data/rewardpin/advancement/orphan.json":   `{"parent":"rewardpin:missing","criteria":{"manual":{"trigger":"minecraft:impossible"}},"rewards":{"experience":2}}`,
		"data/rewardpin/advancement/cycle_a.json":  `{"parent":"rewardpin:cycle_b","criteria":{"manual":{"trigger":"minecraft:impossible"}},"rewards":{"experience":1}}`,
		"data/rewardpin/advancement/cycle_b.json":  `{"parent":"rewardpin:cycle_a","criteria":{"manual":{"trigger":"minecraft:impossible"}},"rewards":{"experience":1}}`,
		"data/rewardpin/function/after.mcfunction": "execute store result score @s seen_xp run data get entity @s XpTotal\nexecute store result score @s seen_items run clear @s minecraft:diamond_block 0\nexecute store result score @s seen_recipe run recipe give @s minecraft:oak_boat\nscoreboard players add @s reward_function 1\nsetblock 8 -61 0 minecraft:gold_block\n",
	}
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(names...)}
	for name, body := range bodies {
		rel := "datapacks/rewardpin/" + name
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
	run := func(addr, name string) []string {
		d := newMenuLifecycleObserver(t, addr, name)
		flush := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		fold := func(ps []usePkt) {
			for _, p := range ps {
				if p.id == server.IDPlayUpdateAdvance {
					decodePackRewardAdvancement(t, p.data, flush)
				}
			}
		}
		command := func(c string) { sendCommand(t, d.pw, c); settleUse(t, d.sink, fold, "pack-reward-command") }
		command("tp @s 6 -60 2 0 0")
		for _, objective := range []string{"seen_xp", "seen_items", "seen_recipe", "reward_function", "actual_xp"} {
			command("scoreboard objectives add " + objective + " dummy")
		}
		command("recipe take @s minecraft:oak_boat")
		query := 0
		probe := func(condition string) bool {
			query++
			yes := fmt.Sprintf("PACK_REWARD_Y_%d", query)
			sendCommand(t, d.pw, "execute if "+condition+" run say "+yes)
			sendCommand(t, d.pw, "execute unless "+condition+" run say PACK_REWARD_NO")
			got := false
			settleUse(t, d.sink, func(ps []usePkt) {
				fold(ps)
				for _, p := range ps {
					if bytes.Contains(p.data, []byte(yes)) {
						got = true
					}
				}
			}, "pack-reward-probe")
			return got
		}
		var out []string
		cases := []struct {
			cmd                 string
			xp, seen, items, fn int
		}{
			{"advancement grant @s only rewardpin:first", 7, 7, 1, 1},
			{"advancement grant @s only rewardpin:first", 7, 7, 1, 1},
			{"advancement grant @s only rewardpin:child", 10, 7, 1, 1},
			{"advancement revoke @s only rewardpin:first", 10, 7, 1, 1},
			{"advancement grant @s from rewardpin:first", 17, 17, 2, 2},
			{"advancement grant @s only rewardpin:missing", 17, 17, 2, 2},
			{"advancement grant @s only rewardpin:first missing", 17, 17, 2, 2},
			{"advancement grant @s only rewardpin:orphan", 19, 17, 2, 2},
			{"advancement grant @s only rewardpin:cycle_a", 20, 17, 2, 2},
			{"advancement grant @s only rewardpin:cycle_b", 21, 17, 2, 2},
		}
		for step, c := range cases {
			flush = &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
			command(c.cmd)
			command("execute store result score @s actual_xp run data get entity @s XpTotal")
			var answers []bool
			for _, v := range []struct {
				objective string
				value     int
			}{{"actual_xp", c.xp}, {"seen_xp", c.seen}, {"seen_items", c.items}, {"seen_recipe", 0}, {"reward_function", c.fn}} {
				ok := probe(fmt.Sprintf("score @s %s matches %d", v.objective, v.value))
				answers = append(answers, ok)
				if !ok {
					t.Errorf("%s %s %s wanted%d", name, c.cmd, v.objective, v.value)
				}
			}
			blockOK := probe("block 8 -61 0 minecraft:gold_block")
			if !blockOK {
				t.Errorf("%s reward function did not continue after failing recipe line", name)
			}
			defs := map[string][]byte{}
			progress := map[string]map[string]bool{}
			removed := map[string]bool{}
			for id, def := range flush.defs {
				if strings.HasPrefix(id, "rewardpin:") {
					defs[id] = def
				}
			}
			for id, p := range flush.prog {
				if strings.HasPrefix(id, "rewardpin:") {
					progress[id] = p
				}
			}
			for id, p := range flush.removed {
				if strings.HasPrefix(id, "rewardpin:") {
					removed[id] = p
				}
			}
			if step == 0 && (len(defs["rewardpin:first"]) == 0 || !progress["rewardpin:first"]["manual"]) {
				t.Errorf("%s first completion definition/progress missing", name)
			}
			if step == 2 && (len(defs["rewardpin:child"]) == 0 || !progress["rewardpin:child"]["manual"]) {
				t.Errorf("%s child completion definition/progress missing", name)
			}
			entry := fmt.Sprintf("%s checks=%v block=%v defs=%x progress=%v removed=%v", c.cmd, answers, blockOK, defs, progress, removed)
			t.Logf("%s %s", name, entry)
			out = append(out, entry)
		}
		return out
	}
	v, g := run(tg.vanilla, names[0]), run(tg.mcgo, names[1])
	if !reflect.DeepEqual(v, g) {
		t.Errorf("datapack rewards differ:\nvanilla%v\nmcgo%v", v, g)
	}
}

// Walk actual displayless definitions, retaining their bytes. The legacy
// advancement decoder uses captured lengths and truncates custom definitions.
func decodePackRewardAdvancement(t *testing.T, body []byte, out *advFlush) {
	t.Helper()
	r := protocol.NewPacketReader(body)
	check := func(err error) {
		if err != nil {
			t.Fatalf("custom advancement packet: %v", err)
		}
	}
	_, err := r.Bool()
	check(err)
	count, err := r.VarInt()
	check(err)
	for range count {
		start := len(body) - r.Remaining()
		id, err := r.String()
		check(err)
		if captured := data.AdvDefWire[id]; captured != nil {
			_, err = r.Read(len(captured) - (len(body) - r.Remaining() - start))
			check(err)
		} else {
			parent, err := r.Bool()
			check(err)
			if parent {
				_, err = r.String()
				check(err)
			}
			display, err := r.Bool()
			check(err)
			if display {
				t.Fatalf("unexpected displayed custom definition %s", id)
			}
			groups, err := r.VarInt()
			check(err)
			for range groups {
				names, err := r.VarInt()
				check(err)
				for range names {
					_, err = r.String()
					check(err)
				}
			}
			_, err = r.Bool()
			check(err)
		}
		out.defs[id] = append([]byte(nil), body[start:len(body)-r.Remaining()]...)
	}
	count, err = r.VarInt()
	check(err)
	for range count {
		id, err := r.String()
		check(err)
		out.removed[id] = true
	}
	count, err = r.VarInt()
	check(err)
	for range count {
		id, err := r.String()
		check(err)
		criteria, err := r.VarInt()
		check(err)
		if out.prog[id] == nil {
			out.prog[id] = map[string]bool{}
		}
		for range criteria {
			name, err := r.String()
			check(err)
			done, err := r.Bool()
			check(err)
			if done {
				_, err = r.Long()
				check(err)
			}
			out.prog[id][name] = done
		}
	}
	_, err = r.Bool()
	check(err)
	if r.Remaining() != 0 {
		t.Fatalf("custom advancement packet has %d trailing bytes", r.Remaining())
	}
}
