package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFunctionTagCodecDefaultsAndFailures(t *testing.T) {
	entries, replace, ok := parseFunctionTagFile([]byte(`{"values":["pin:a",{"id":"#pin:b","required":null},{"id":"pin:missing","required":false}],"replace":null}`))
	want := []functionTagEntry{{"pin:a", false, true}, {"pin:b", true, true}, {"pin:missing", false, false}}
	if !ok || replace || !reflect.DeepEqual(entries, want) {
		t.Fatalf("defaults: %v,%v,%v", entries, replace, ok)
	}
	for _, raw := range []string{`{}`, `{"values":null}`, `{"values":[7]}`, `{"values":[{"id":"pin:a","required":0}]}`, `{"values":["BAD ID"]}`, `{"replace":3,"values":[]}`} {
		if _, _, ok := parseFunctionTagFile([]byte(raw)); ok {
			t.Errorf("invalid tag accepted: %s", raw)
		}
	}
}

func TestFunctionTagResolutionDependenciesAndFirstOccurrence(t *testing.T) {
	element := func(id string) functionTagEntry { return functionTagEntry{id: id, required: true} }
	tag := func(id string, required bool) functionTagEntry {
		return functionTagEntry{id: id, tag: true, required: required}
	}
	defs := map[string][]functionTagEntry{
		"pin:group":    {element("pin:a"), tag("pin:nested", true), element("pin:a")},
		"pin:nested":   {element("pin:b"), element("pin:a")},
		"pin:optional": {tag("pin:missing", false), {id: "pin:unknown", required: false}, element("pin:b")},
		"pin:broken":   {element("pin:unknown")},
		"pin:cycle_a":  {tag("pin:cycle_b", true)}, "pin:cycle_b": {tag("pin:cycle_a", true)},
		"pin:empty": {}, "pin:uses_empty": {tag("pin:empty", true), element("pin:a")},
	}
	got := resolveFunctionTags(defs, map[string][]string{"pin:a": {}, "pin:b": {}})
	if !reflect.DeepEqual(got["pin:group"], []string{"pin:a", "pin:b"}) || !reflect.DeepEqual(got["pin:optional"], []string{"pin:b"}) || !reflect.DeepEqual(got["pin:uses_empty"], []string{"pin:a"}) {
		t.Fatalf("resolved %v", got)
	}
	for _, id := range []string{"pin:broken", "pin:cycle_a", "pin:cycle_b"} {
		if _, ok := got[id]; ok {
			t.Errorf("unresolved tag retained %s", id)
		}
	}
	if _, ok := got["pin:empty"]; !ok {
		t.Fatal("valid empty tag lost")
	}
}

func TestFunctionTagPackFilesRespectSelectedResourceFailures(t *testing.T) {
	s, _ := randSim(t)
	s.WorldDir = t.TempDir()
	write := func(pack, path, body string) {
		f := filepath.Join(s.WorldDir, "datapacks", pack, path)
		if err := os.MkdirAll(filepath.Dir(f), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, pack := range []string{"a", "z"} {
		write(pack, "pack.mcmeta", `{"pack":{"description":"tag unit","min_format":107,"max_format":107}}`)
	}
	write("a", "data/pin/function/a.mcfunction", "say a\n")
	write("a", "data/pin/function/b.mcfunction", "say b\n")
	write("a", "data/pin/function/override.mcfunction", "say lower\n")
	write("z", "data/pin/function/override.mcfunction", "say first\nunknown_literal\n")
	for id, body := range map[string]string{"permission": "say first\ntick query\n", "slash": "say first\n/say bad\n", "bare": "say first\nsetblock\n"} {
		write("a", "data/pin/function/"+id+".mcfunction", body)
	}
	write("a", "data/pin/tags/function/merge.json", `{"values":["pin:a"]}`)
	write("z", "data/pin/tags/function/merge.json", `{"values":["pin:b"]}`)
	write("a", "data/pin/tags/function/replace.json", `{"values":["pin:a"]}`)
	write("z", "data/pin/tags/function/replace.json", `{"replace":true,"values":["pin:b"]}`)
	write("a", "data/pin/tags/function/invalid.json", `{"values":["pin:a"]}`)
	write("z", "data/pin/tags/function/invalid.json", `{"replace":true,"values":["pin:b",7]}`)
	s.loadFunctions()
	s.loadFunctionTags()
	for _, id := range []string{"override", "permission", "slash", "bare"} {
		if _, ok := s.functions["pin:"+id]; ok {
			t.Errorf("invalid function remained %s", id)
		}
	}
	for id, want := range map[string][]string{"merge": {"pin:a", "pin:b"}, "replace": {"pin:b"}, "invalid": {"pin:a"}} {
		if !reflect.DeepEqual(s.functionTags["pin:"+id], want) {
			t.Errorf("%s=%v want%v", id, s.functionTags["pin:"+id], want)
		}
	}
}

func TestFunctionTagsLoadOnceTickAndFollowingDeadline(t *testing.T) {
	s, _ := randSim(t)
	s.functions = map[string][]string{
		"pin:load":  {"scoreboard objectives add tagunit dummy", "scoreboard players add loaded tagunit 1"},
		"pin:tick":  {"execute store result score age tagunit run time query gametime", "scoreboard players add tick tagunit 1", "schedule function pin:after 1t replace"},
		"pin:after": {"scoreboard players add after tagunit 1"},
	}
	s.functionTags = map[string][]string{"minecraft:load": {"pin:load"}, "minecraft:tick": {"pin:tick"}}
	s.functionTagLoadPending = true
	for i := int64(1); i <= 2; i++ {
		s.functionTagsStep()
		s.clock.count = i
		s.functionScheduleStep()
	}
	for holder, want := range map[string]int{"loaded": 1, "tick": 2, "after": 2, "age": 1} {
		r, _ := s.runCommandAt("RCON", 4, "scoreboard", []string{"players", "get", holder, "tagunit"})
		if !r.hasResult || r.result != want {
			t.Errorf("%s=%#v want%d", holder, r, want)
		}
	}
}

func TestFunctionCommandsDoNotSynthesizeStoreCallbacks(t *testing.T) {
	s, d := randSim(t)
	s.runCommandAt("RCON", 4, "scoreboard", []string{"objectives", "add", "tagunit", "dummy"})
	s.runCommandAt("RCON", 4, "scoreboard", []string{"players", "set", "stored", "tagunit", "7"})
	s.functions = map[string][]string{"pin:a": {"scoreboard players add calls tagunit 1"}}
	s.functionTags = map[string][]string{"pin:group": {"pin:a"}}
	frame := commandSourceFrame{dim: d, display: literal("Server"), level: 2}
	s.withCommandFrame(&frame, func() reply {
		for _, name := range []string{"pin:a", "#pin:group"} {
			s.runCommand(neutralCommandActor, "execute", []string{"store", "success", "score", "stored", "tagunit", "run", "function", name})
		}
		return reply{}
	})
	r, _ := s.runCommandAt("RCON", 4, "scoreboard", []string{"players", "get", "stored", "tagunit"})
	if !r.hasResult || r.result != 7 {
		t.Fatalf("implicit store completion %#v", r)
	}
	r, _ = s.runCommandAt("RCON", 4, "scoreboard", []string{"players", "get", "calls", "tagunit"})
	if !r.hasResult || r.result != 2 {
		t.Fatalf("function bodies not executed %#v", r)
	}
	r = s.runFunctionTag("RCON", "pin:missing")
	if !r.hasResult || r.result != 0 || r.outcome != commandFailed {
		t.Fatalf("empty tag completion %#v", r)
	}
}

func TestFunctionTagMixedCyclePrioritizesRequiredDependencies(t *testing.T) {
	for _, ids := range [][2]string{{"pin:a", "pin:b"}, {"pin:z", "pin:a"}} {
		defs := map[string][]functionTagEntry{
			ids[0]: {{id: "fn:a", required: true}, {id: ids[1], tag: true, required: false}},
			ids[1]: {{id: "fn:b", required: true}, {id: ids[0], tag: true, required: true}},
		}
		got := resolveFunctionTags(defs, map[string][]string{"fn:a": {}, "fn:b": {}})
		if !reflect.DeepEqual(got[ids[0]], []string{"fn:a"}) || !reflect.DeepEqual(got[ids[1]], []string{"fn:b", "fn:a"}) {
			t.Fatalf("mixed cycle %v ->%v", ids, got)
		}
	}
}
