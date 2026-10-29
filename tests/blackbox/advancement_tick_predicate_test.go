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

func TestAdvancementTickPlayerPredicatesParity(t *testing.T) {
	const name = "AdvPredicate"
	entity := func(predicate any) any {
		return map[string]any{"condition": "minecraft:entity_properties", "entity": "this", "predicate": predicate}
	}
	player := func(value any) any { return map[string]any{"minecraft:type_specific/player": value} }
	survival := entity(player(map[string]any{"gamemode": []string{"survival"}}))
	pig := entity(map[string]any{"minecraft:entity_type": "minecraft:pig"})
	type criterion struct {
		id        string
		predicate any
		first     int
	}
	cases := []criterion{
		{"empty_map", map[string]any{}, 0},
		{"type_player", map[string]any{"minecraft:entity_type": "minecraft:player"}, 0},
		{"type_pig", map[string]any{"minecraft:entity_type": "minecraft:pig"}, -1},
		{"type_list", []any{entity(map[string]any{"minecraft:entity_type": "minecraft:player"})}, 0},
		{"entity_present", []any{map[string]any{"condition": "minecraft:entity_properties", "entity": "this"}}, 0},
		{"omitted_actor", []any{map[string]any{"condition": "minecraft:entity_properties", "entity": "attacker"}}, 0},
		{"missing_actor", []any{map[string]any{"condition": "minecraft:entity_properties", "entity": "attacker", "predicate": map[string]any{"minecraft:entity_type": "minecraft:player"}}}, -1},
		{"empty_actor", []any{map[string]any{"condition": "minecraft:entity_properties", "entity": "attacker", "predicate": map[string]any{}}}, -1},
		{"player_null", nil, 0},
		{"inverted_missing_actor", []any{map[string]any{"condition": "minecraft:inverted", "term": map[string]any{"condition": "minecraft:entity_properties", "entity": "attacker", "predicate": map[string]any{"minecraft:entity_type": "minecraft:player"}}}}, 0},
		{"unsupported_condition", []any{map[string]any{"condition": "minecraft:weather_check", "raining": true}}, 4},
		{"survival", player(map[string]any{"gamemode": []string{"survival"}}), 1},
		{"level", player(map[string]any{"level": map[string]any{"min": 2, "max": 3}}), 2},
		{"level_fraction", player(map[string]any{"level": map[string]any{"min": 2.5, "max": 3.5}}), 2},
		{"level_empty", player(map[string]any{"level": map[string]any{}}), 0},
		{"high_level", player(map[string]any{"level": map[string]any{"min": 9}}), -1},
		{"sneaking", map[string]any{"minecraft:flags": map[string]any{"is_sneaking": true}}, 2},
		{"not_baby", map[string]any{"minecraft:flags": map[string]any{"is_baby": false}}, 0},
		{"baby", map[string]any{"minecraft:flags": map[string]any{"is_baby": true}}, -1},
		{"nbt_gate", map[string]any{"minecraft:nbt": `{Tags:["predicate_gate"]}`}, 3},
		{"equipment", map[string]any{"minecraft:equipment": map[string]any{"mainhand": map[string]any{"items": "minecraft:diamond"}}}, 3},
		{"location", map[string]any{"minecraft:location": map[string]any{"dimension": "minecraft:overworld", "position": map[string]any{"x": map[string]any{"min": 9, "max": 11}, "y": nil, "z": map[string]any{"min": 9, "max": 11}}}}, 4},
		{"wrong_dimension", map[string]any{"minecraft:location": map[string]any{"dimension": "minecraft:the_nether"}}, -1},
		{"biome", map[string]any{"minecraft:location": map[string]any{"biomes": "minecraft:the_void"}}, 0},
		{"changed_biome", map[string]any{"minecraft:location": map[string]any{"biomes": "minecraft:desert"}}, 4},
		{"biome_tag", map[string]any{"minecraft:location": map[string]any{"biomes": "#minecraft:is_overworld"}}, 4},
		{"inverted", []any{map[string]any{"condition": "minecraft:inverted", "term": pig}}, 0},
		{"any_of", []any{map[string]any{"condition": "minecraft:any_of", "terms": []any{pig, survival}}}, 1},
		{"all_of", []any{map[string]any{"condition": "minecraft:all_of", "terms": []any{map[string]any{"condition": "minecraft:inverted", "term": pig}, survival}}}, 1},
		{"never", []any{map[string]any{"condition": "minecraft:random_chance", "chance": 0}}, -1},
		{"always", []any{map[string]any{"condition": "minecraft:random_chance", "chance": 1}}, 0},
		{"invalid_subtype", map[string]any{"unknown:predicate": map[string]any{}}, -1},
		{"invalid_scalar", 7, -1},
		{"invalid_target", []any{map[string]any{"condition": "minecraft:entity_properties", "entity": "unknown"}}, -1},
		{"invalid_gamemode", player(map[string]any{"gamemode": "survival"}), -1},
		{"invalid_condition", []any{map[string]any{"condition": "unknown:condition"}}, -1},
	}
	bodies := map[string]string{"pack.mcmeta": `{"pack":{"description":"tick player predicate controls","min_format":107,"max_format":107}}`}
	for _, c := range cases {
		raw := map[string]any{"criteria": map[string]any{"clock": map[string]any{"trigger": "minecraft:tick", "conditions": map[string]any{"player": c.predicate}}}, "rewards": map[string]any{"function": "predpin:" + c.id}}
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		bodies["data/predpin/advancement/"+c.id+".json"] = string(encoded)
		bodies["data/predpin/function/"+c.id+".mcfunction"] = "tag @s add got_" + c.id + "\n"
	}
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(name)}
	for path, body := range bodies {
		rel := "datapacks/predpin/" + path
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
					score, e3 := r.VarInt()
					if e1 == nil && e2 == nil && e3 == nil && holder == name && objective == "pred_clock" {
						clock = score
					}
				}
			}
			return lines
		}
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("PRED_END_%d", serial)
			sendCommand(t, d.pw, "say "+marker)
			deadline := time.Now().Add(10 * time.Second)
			var lines []string
			for time.Now().Before(deadline) {
				lines = append(lines, read()...)
				if structureSaidBB(lines, marker) {
					return lines
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("predicate command barrier missing")
			return nil
		}
		command := func(c string) { sendCommand(t, d.pw, c); barrier() }
		command("tick freeze")
		command("scoreboard objectives add pred_clock minecraft.custom:minecraft.play_time")
		command("scoreboard objectives setdisplay sidebar pred_clock")
		command("tp @s 0.5 -60 0.5")
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
			t.Fatalf("%s player tick score did not advance from%d", addr, before)
		}
		var out []bool
		for phase := 0; phase < 5; phase++ {
			switch phase {
			case 1:
				command("gamemode survival @s")
			case 2:
				command("experience set @s 2 levels")
				if err := d.pw.WritePacket(server.IDPlayPlayerInputSB, []byte{0x20}); err != nil {
					t.Fatal(err)
				}
				barrier()
			case 3:
				command("item replace entity @s weapon.mainhand with minecraft:diamond")
				command("tag @s add predicate_gate")
			case 4:
				loaded := false
				deadline := time.Now().Add(15 * time.Second)
				for time.Now().Before(deadline) {
					sendCommand(t, d.pw, "execute if loaded -16 -60 -16 if loaded -16 -60 16 if loaded 16 -60 -16 if loaded 16 -60 16 run say PRED-BIOMES-LOADED")
					if structureSaidBB(barrier(), "PRED-BIOMES-LOADED") {
						loaded = true
						break
					}
					waitTicks()
				}
				if !loaded {
					t.Fatal("predicate biome region never loaded")
				}
				command("fillbiome -16 -64 -16 16 -48 16 minecraft:desert")
				command("tp @s 10 -60 10")
			}
			waitTicks()
			for _, c := range cases {
				if phase == 4 && (c.id == "invalid_subtype" || c.id == "invalid_scalar" || c.id == "invalid_condition" || c.id == "invalid_gamemode" || c.id == "invalid_target" || c.id == "unsupported_condition") {
					command("advancement grant @s only predpin:" + c.id)
				}
			}
			markers := map[string]string{}
			for _, c := range cases {
				mode := "unless"
				if c.first >= 0 && phase >= c.first {
					mode = "if"
				}
				marker := fmt.Sprintf("PRED_PASS_%d_%s", phase, c.id)
				markers[c.id] = marker
				sendCommand(t, d.pw, fmt.Sprintf("execute %s entity @s[tag=got_%s] run say %s", mode, c.id, marker))
			}
			lines := barrier()
			for _, c := range cases {
				ok := structureSaidBB(lines, markers[c.id])
				if !ok {
					t.Errorf("%s phase%d predicate%s wantedtag%v", addr, phase, c.id, c.first >= 0 && phase >= c.first)
				}
				out = append(out, ok)
			}
		}
		command("scoreboard objectives setdisplay sidebar")
		return out
	}
	v, g := run(tg.vanilla), run(tg.mcgo)
	if !reflect.DeepEqual(v, g) {
		t.Errorf("tick predicate checks differ vanilla%v mcgo%v", v, g)
	}
}
