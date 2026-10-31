package server

import (
	"fmt"
	"math"
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"testing"
)

type rawAttributeCase struct {
	name, typ, attributes, extra, key   string
	base, effective, health, tickHealth float64
	modifier                            string
	amount                              float64
	operation                           string
}

func rawAttributeCases() []rawAttributeCase {
	return []rawAttributeCase{
		{name: "base", typ: "horse", attributes: `[{id:"minecraft:scale",base:2d}]`, key: "scale", base: 2, effective: 2, health: 53, tickHealth: 53},
		{name: "add", typ: "horse", attributes: `[{id:"minecraft:scale",base:1d,modifiers:[{id:"rawpin:add",amount:1d,operation:"add_value"}]}]`, key: "scale", base: 1, effective: 2, health: 53, tickHealth: 53, modifier: "rawpin:add", amount: 1, operation: "add_value"},
		{name: "mult_base", typ: "horse", attributes: `[{id:"minecraft:scale",base:2d,modifiers:[{id:"rawpin:base",amount:0.5d,operation:"add_multiplied_base"}]}]`, key: "scale", base: 2, effective: 3, health: 53, tickHealth: 53, modifier: "rawpin:base", amount: .5, operation: "add_multiplied_base"},
		{name: "mult_total", typ: "horse", attributes: `[{id:"minecraft:scale",base:2d,modifiers:[{id:"rawpin:total",amount:0.5d,operation:"add_multiplied_total"}]}]`, key: "scale", base: 2, effective: 3, health: 53, tickHealth: 53, modifier: "rawpin:total", amount: .5, operation: "add_multiplied_total"},
		{name: "default_health", typ: "pig", attributes: `[{id:"minecraft:max_health",base:40d}]`, key: "max_health", base: 40, effective: 40, health: 40, tickHealth: 30},
		{name: "clamped_health", typ: "pig", attributes: `[{id:"minecraft:max_health",base:40d}]`, extra: `,Health:100f`, key: "max_health", base: 40, effective: 40, health: 40, tickHealth: 40},
		{name: "wild_health_order", typ: "wolf", attributes: `[{id:"minecraft:max_health",base:40d}]`, key: "max_health", base: 8, effective: 8, health: 40, tickHealth: 8},
	}
}
func checkRawAttributeCase(t *testing.T, s *Server, e *Entity, tc rawAttributeCase, health float64) {
	t.Helper()
	target := "@e[tag=RawAttribute,limit=1]"
	if e.Riding != 0 {
		target = "@e[tag=RawChild,limit=1]"
	}
	for _, query := range []struct {
		args []string
		want float64
	}{{[]string{target, "minecraft:" + tc.key, "base", "get", "1000"}, tc.base}, {[]string{target, "minecraft:" + tc.key, "get", "1000"}, tc.effective}} {
		got := s.runAttribute("P", query.args)
		if !got.hasResult || got.result != int(query.want*1000) {
			t.Errorf("query %v result%d has%v want%g", query.args, got.result, got.hasResult, query.want)
		}
	}
	if tc.modifier != "" {
		got := s.runAttribute("P", []string{target, "minecraft:" + tc.key, "modifier", "value", "get", tc.modifier, "1000"})
		if !got.hasResult || got.result != int(tc.amount*1000) {
			t.Errorf("permanent modifier query result%d want%g", got.result, tc.amount)
		}
	}
	if float64(e.Health) != health || math.IsNaN(float64(e.Health)) {
		t.Errorf("Health%v want%g", e.Health, health)
	}
	pack, _ := entitySaveMap(e, s.clock.count, s.entities).m["attributes"].(nbt.List)
	var row map[string]any
	for _, v := range pack.Items {
		m := v.(map[string]any)
		if m["id"] == "minecraft:"+tc.key {
			if row != nil {
				t.Fatal("duplicate packed attribute")
			}
			row = m
		}
	}
	if row == nil {
		t.Errorf("materialized attribute %s missing", tc.key)
		return
	}
	if got, ok := row["base"].(float64); !ok || got != tc.base {
		t.Errorf("saved base %#v want%g", row["base"], tc.base)
	}
	mods, _ := row["modifiers"].(nbt.List)
	wantMods := 0
	if tc.modifier != "" {
		wantMods = 1
	}
	if len(mods.Items) != wantMods {
		t.Errorf("saved modifiers=%#v want%d", mods, wantMods)
	} else if wantMods == 1 {
		m := mods.Items[0].(map[string]any)
		if m["id"] != tc.modifier || m["amount"] != tc.amount || m["operation"] != tc.operation {
			t.Errorf("saved permanent modifier %#v", m)
		}
	}
}
func TestRawLivingSummonAttributes(t *testing.T) {
	for _, tc := range rawAttributeCases() {
		t.Run(tc.name, func(t *testing.T) {
			s := nativeSaveTestServer(t)
			enforceSession(t, s, "P")
			tag := fmt.Sprintf(`{Tags:["RawAttribute"],NoAI:1b,NoGravity:1b,attributes:%s%s}`, tc.attributes, tc.extra)
			s.runSummon("P", []string{tc.typ, "1", "0", "1", tag})
			var e *Entity
			for _, copy := range s.entities.All() {
				if name, _ := EntityTypeName(copy.Type); name == tc.typ {
					e, _ = s.entities.GetPtr(copy.ID)
				}
			}
			if e == nil {
				t.Fatal("actual tagged summon did not create body")
			}
			checkRawAttributeCase(t, s, e, tc, tc.health)
			if tc.name == "default_health" {
				// A real damage consumer distinguishes the loaded maximum from
				// constructor health; the admitted refresh must not heal it back.
				s.damageMob(e, 10, "generic", nil, nil)
				if e.Health != 30 || e.Dying {
					t.Fatal("loaded max-health damage consumer")
				}
			}
			s.mobTickStep()
			checkRawAttributeCase(t, s, e, tc, tc.tickHealth)
			if err := s.SaveWorld(); err != nil {
				t.Fatal(err)
			}
			restored := nativeSaveTestServer(t)
			restored.WorldDir = s.WorldDir
			if err := restored.LoadWorld(); err != nil {
				t.Fatal(err)
			}
			enforceSession(t, restored, "P")
			var loaded *Entity
			for _, copy := range restored.entities.All() {
				if copy.UUID == e.UUID {
					loaded, _ = restored.entities.GetPtr(copy.ID)
				}
			}
			if loaded == nil {
				t.Fatal("saved summon did not restart")
			}
			checkRawAttributeCase(t, restored, loaded, tc, tc.tickHealth)
		})
	}
}
func TestNativeLivingRestoreAttributeControl(t *testing.T) {
	s := nativeSaveTestServer(t)
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	tag, _, err := parseSNBTTag(`{id:"minecraft:horse",Tags:["RawAttribute"],UUID:[I;1,2,3,4],Pos:[1d,0d,1d],NoAI:1b,NoGravity:1b,attributes:[{id:"minecraft:scale",base:2d,modifiers:[{id:"rawpin:total",amount:0.5d,operation:"add_multiplied_total"}]}]}`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := world.SaveEntityRegions(world.DimensionDir(s.WorldDir, dimOverworld), map[[2]int32][]map[string]any{{0, 0}: {snbtToValue(tag).(map[string]any)}}); err != nil {
		t.Fatal(err)
	}
	loaded := nativeSaveTestServer(t)
	loaded.WorldDir = s.WorldDir
	if err := loaded.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	enforceSession(t, loaded, "P")
	for _, copy := range loaded.entities.All() {
		if name, _ := EntityTypeName(copy.Type); name == "horse" {
			e, _ := loaded.entities.GetPtr(copy.ID)
			tc := rawAttributeCases()[3]
			checkRawAttributeCase(t, loaded, e, tc, 53)
			return
		}
	}
	t.Fatal("native restore control missing")
}
func TestRawLivingPassengerAttributes(t *testing.T) {
	s := nativeSaveTestServer(t)
	enforceSession(t, s, "P")
	s.runSummon("P", []string{"pig", "1", "0", "1", `{NoAI:1b,NoGravity:1b,attributes:[{id:"minecraft:scale",base:2d}],Passengers:[{id:"minecraft:pig",Tags:["RawChild"],NoAI:1b,attributes:[{id:"minecraft:scale",base:2d,modifiers:[{id:"rawpin:rider",amount:0.5d,operation:"add_multiplied_total"}]}]}]}`})
	found := false
	for _, copy := range s.entities.All() {
		if copy.Riding == 0 {
			continue
		}
		e, _ := s.entities.GetPtr(copy.ID)
		tc := rawAttributeCase{typ: "pig", key: "scale", base: 2, effective: 3, health: 10, modifier: "rawpin:rider", amount: .5, operation: "add_multiplied_total"}
		checkRawAttributeCase(t, s, e, tc, 10)
		found = true
	}
	if !found {
		t.Fatal("raw passenger missing")
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	loaded := nativeSaveTestServer(t)
	loaded.WorldDir = s.WorldDir
	if err := loaded.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	enforceSession(t, loaded, "P")
	found = false
	for _, copy := range loaded.entities.All() {
		if copy.Riding != 0 {
			e, _ := loaded.entities.GetPtr(copy.ID)
			checkRawAttributeCase(t, loaded, e, rawAttributeCase{key: "scale", base: 2, effective: 3, modifier: "rawpin:rider", amount: .5, operation: "add_multiplied_total"}, 10)
			found = true
		}
	}
	if !found {
		t.Fatal("raw passenger modifier lost on restart")
	}
}
