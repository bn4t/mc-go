//go:build blackbox

package blackbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestAdvancementPlayerProgressPredicateParity(t *testing.T) {
	const name = "AdvProgressPred"
	type progressCase struct {
		id     string
		value  any
		first  int
		manual bool
	}
	cases := []progressCase{
		{"not_done", map[string]any{"progresspin:control": false}, 0, false},
		{"has_a", map[string]any{"progresspin:control": map[string]any{"a": true}}, 1, false},
		{"partial", map[string]any{"progresspin:control": map[string]any{"a": true, "b": false}}, 1, false},
		{"done", map[string]any{"progresspin:control": true}, 2, false},
		{"b_only", map[string]any{"progresspin:control": map[string]any{"a": false, "b": true}}, 3, false},
		{"empty_criteria", map[string]any{"progresspin:control": map[string]any{}}, 0, false},
		{"empty_map", map[string]any{}, 0, false},
		{"null_map", nil, 0, false},
		{"empty_namespace", map[string]any{":recipes/decorations/crafting_table": true}, 0, false},
		{"empty_reference", map[string]any{"": false}, 3, true},
		{"dot_reference", map[string]any{"ns:a/../b": false}, 3, true},
		{"invalid_identifier", map[string]any{"BAD ID": false}, -1, true},
		{"invalid_namespace", map[string]any{"..:foo": false}, -1, true},
		{"missing_id", map[string]any{"progresspin:missing": false}, 3, true},
		{"missing_criterion", map[string]any{"progresspin:control": map[string]any{"missing": false}}, 3, true},
		{"invalid_state", map[string]any{"progresspin:control": 7}, -1, true},
		{"invalid_criterion", map[string]any{"progresspin:control": map[string]any{"a": "false"}}, -1, true},
		{"invalid_array", []any{}, -1, true},
		{"invalid_null_state", map[string]any{"progresspin:control": nil}, -1, true},
	}
	bodies := map[string]string{
		"pack.mcmeta": `{"pack":{"description":"player advancement progress predicates","min_format":107,"max_format":107}}`,
		"data/progresspin/advancement/control.json": `{"criteria":{"a":{"trigger":"minecraft:impossible"},"b":{"trigger":"minecraft:impossible"}},"requirements":[["a"],["b"]]}`,
	}
	for _, c := range cases {
		raw := map[string]any{"criteria": map[string]any{"clock": map[string]any{"trigger": "minecraft:tick", "conditions": map[string]any{"player": map[string]any{"minecraft:type_specific/player": map[string]any{"advancements": c.value}}}}}, "rewards": map[string]any{"function": "progresspin:" + c.id}}
		body, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		bodies["data/progresspin/advancement/"+c.id+".json"] = string(body)
		bodies["data/progresspin/function/"+c.id+".mcfunction"] = "tag @s add pp_" + c.id + "\n"
	}
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(name)}
	for path, body := range bodies {
		rel := "datapacks/progresspin/" + path
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
		d := newMenuLifecycleObserver(t, addr, name)
		clock := int32(-1)
		serial := 0
		read := func() []string {
			var lines []string
			for _, p := range d.sink.take() {
				lines = append(lines, fmt.Sprintf("0x%x:%s", p.id, p.data))
				if p.id == server.IDPlayUpdateScore {
					r := protocol.NewPacketReader(p.data)
					holder, e1 := r.String()
					objective, e2 := r.String()
					value, e3 := r.VarInt()
					if e1 == nil && e2 == nil && e3 == nil && holder == name && objective == "progress_clock" {
						clock = value
					}
				}
			}
			return lines
		}
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("PROGRESS_END_%d", serial)
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
			t.Fatal("progress predicate barrier missing")
			return nil
		}
		command := func(c string) { sendCommand(t, d.pw, c); barrier() }
		command("tick freeze")
		command("scoreboard objectives add progress_clock minecraft.custom:minecraft.play_time")
		command("scoreboard objectives setdisplay sidebar progress_clock")
		waitTicks := func() {
			read()
			before := clock
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				read()
				if clock >= 0 && clock >= before+2 {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("player progress predicate tick receipt missing")
		}
		var out []bool
		for phase := 0; phase < 4; phase++ {
			switch phase {
			case 1:
				command("advancement grant @s only progresspin:control a")
			case 2:
				command("advancement grant @s only progresspin:control b")
			case 3:
				command("advancement revoke @s only progresspin:control a")
				for _, c := range cases {
					if c.manual {
						command("advancement grant @s only progresspin:" + c.id)
					}
				}
			}
			waitTicks()
			markers := map[string]string{}
			for _, c := range cases {
				mode := "unless"
				if c.first >= 0 && phase >= c.first {
					mode = "if"
				}
				marker := fmt.Sprintf("PROGRESS_PASS_%d_%s", phase, c.id)
				markers[c.id] = marker
				sendCommand(t, d.pw, fmt.Sprintf("execute %s entity @s[tag=pp_%s] run say %s", mode, c.id, marker))
			}
			lines := barrier()
			for _, c := range cases {
				ok := structureSaidBB(lines, markers[c.id])
				if !ok {
					t.Errorf("%s phase%d progress predicate%s wantedtag%v", addr, phase, c.id, c.first >= 0 && phase >= c.first)
				}
				out = append(out, ok)
			}
		}
		command("scoreboard objectives setdisplay sidebar")
		return out
	}
	v, g := run(tg.vanilla), run(tg.mcgo)
	if !reflect.DeepEqual(v, g) {
		t.Errorf("player progress predicate checks differ vanilla%v mcgo%v", v, g)
	}
}
