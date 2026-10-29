//go:build blackbox

package blackbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestAdvancementJSONIntegerCodecParity(t *testing.T) {
	const name = "AdvNumberCodec"
	type numberCase struct {
		id               string
		count, xp        any
		wantCount, delta int
		valid            bool
	}
	number := func(value string) any { return json.Number(value) }
	cases := []numberCase{
		{"fraction", number("3.9"), number("2.9"), 3, 2, true},
		{"wrapped", number("4294967299"), number("4294967301"), 3, 5, true},
		{"precise", number("9007199254740995"), number("9007199254740999"), 3, 7, true},
		{"exponent", number("9.007199254740995e15"), number("4.294967303e9"), 3, 7, true},
		{"upper", number("99.9"), number("0"), 99, 0, true},
		{"defaults", nil, nil, 1, 0, true},
		{"negative_xp", number("3"), number("-1.9"), 3, -1, true},
		{"zero_count", number("0.9"), number("100"), 0, 0, false},
		{"over_count", number("100.1"), number("100"), 0, 0, false},
		{"boolean_count", true, number("100"), 0, 0, false},
		{"boolean_xp", number("3"), true, 0, 0, false},
	}
	bodies := map[string]string{"pack.mcmeta": `{"pack":{"description":"advancement JSON integer codecs","min_format":107,"max_format":107}}`}
	for _, c := range cases {
		display := map[string]any{"title": "numberpin:" + c.id, "description": "JSON integer codec", "icon": map[string]any{"id": "minecraft:paper", "count": c.count}, "announce_to_chat": false}
		raw := map[string]any{"criteria": map[string]any{"manual": map[string]any{"trigger": "minecraft:impossible"}}, "display": display, "rewards": map[string]any{"experience": c.xp}}
		if c.id == "defaults" {
			raw["parent"] = nil
			raw["requirements"] = nil
			display["frame"] = nil
			display["background"] = nil
			display["show_toast"] = nil
			display["hidden"] = nil
			display["announce_to_chat"] = nil
			display["icon"].(map[string]any)["components"] = nil
			reward := raw["rewards"].(map[string]any)
			reward["function"] = nil
			reward["recipes"] = nil
			reward["loot"] = nil
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		bodies["data/numberpin/advancement/"+c.id+".json"] = string(encoded)
	}
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(name)}
	for path, body := range bodies {
		rel := "datapacks/numberpin/" + path
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
	run := func(addr string) (map[string]displayedAdvancementProbe, []bool) {
		d := newMenuLifecycleObserver(t, addr, name)
		definitions := map[string]displayedAdvancementProbe{}
		serial := 0
		read := func() []string {
			var lines []string
			for _, p := range d.sink.take() {
				lines = append(lines, fmt.Sprintf("0x%x:%s", p.id, p.data))
				if p.id == server.IDPlayUpdateAdvance {
					decodeDisplayedAdvancementPacketMatching(t, p.data, definitions, func(id string) bool { return strings.HasPrefix(id, "numberpin:") })
				}
			}
			return lines
		}
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("NUMBER_END_%d", serial)
			sendCommand(t, d.pw, "say "+marker)
			var lines []string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				lines = append(lines, read()...)
				if structureSaidBB(lines, marker) {
					return lines
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("number codec barrier missing")
			return nil
		}
		command := func(c string) { sendCommand(t, d.pw, c); barrier() }
		command("scoreboard objectives add number_xp dummy")
		total := 0
		var out []bool
		for index, c := range cases {
			command("advancement grant @s only numberpin:" + c.id)
			total += c.delta
			command("execute store result score @s number_xp run data get entity @s XpTotal")
			marker := fmt.Sprintf("NUMBER_XP_%d", index)
			sendCommand(t, d.pw, fmt.Sprintf("execute if score @s number_xp matches %d run say %s", total, marker))
			ok := structureSaidBB(barrier(), marker)
			if !ok {
				t.Errorf("%s case%s reward XP wanted%d", addr, c.id, total)
			}
			out = append(out, ok)
		}
		// A player-tick flush may follow the final command reply. Require every
		// valid definition, then compare actual icon metadata and encoded bodies.
		wanted := 0
		for _, c := range cases {
			if c.valid {
				wanted++
			}
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) && len(definitions) < wanted {
			read()
			time.Sleep(10 * time.Millisecond)
		}
		if len(definitions) != wanted {
			t.Errorf("%s number definitions%d wanted%d", addr, len(definitions), wanted)
		}
		for _, c := range cases {
			p, exists := definitions["numberpin:"+c.id]
			if exists != c.valid {
				t.Errorf("%s case%s definition present%v wanted%v", addr, c.id, exists, c.valid)
			}
			if c.valid && !strings.HasSuffix(p.Icon, fmt.Sprintf("#%d", c.wantCount)) {
				t.Errorf("%s case%s icon%s wantedcount%d", addr, c.id, p.Icon, c.wantCount)
			}
		}
		return definitions, out
	}
	v, vc := run(tg.vanilla)
	g, gc := run(tg.mcgo)
	if !reflect.DeepEqual(v, g) {
		t.Errorf("numeric advancement definition bytes/metadata differ")
	}
	if !reflect.DeepEqual(vc, gc) {
		t.Errorf("numeric reward checks differ vanilla%v mcgo%v", vc, gc)
	}
}
