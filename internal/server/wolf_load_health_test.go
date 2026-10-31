package server

import (
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"reflect"
	"testing"
)

var wolfProbeOwner = []int32{286331153, 286331153, 286331153, 286331153}

func wolfProbeBody(t *testing.T, s *Server) *Entity {
	t.Helper()
	for _, copy := range s.entities.All() {
		if name, _ := EntityTypeName(copy.Type); name == "wolf" {
			e, _ := s.entities.GetPtr(copy.ID)
			return e
		}
	}
	t.Fatal("wolf body not admitted")
	return nil
}
func checkWolfLoadState(t *testing.T, s *Server, e *Entity, base, health float64, owned bool) {
	t.Helper()
	for _, args := range [][]string{{"@e[tag=WolfProbe,limit=1]", "minecraft:max_health", "base", "get", "1000"}, {"@e[tag=WolfProbe,limit=1]", "minecraft:max_health", "get", "1000"}} {
		r := s.runAttribute("P", args)
		if !r.hasResult || r.result != int(base*1000) {
			t.Errorf("max query result%d want%g", r.result, base)
		}
	}
	if e.Health != float32(health) {
		t.Errorf("Health%v want%g", e.Health, health)
	}
	save := entitySaveMap(e, s.clock.count, s.entities).m
	if owned {
		if !reflect.DeepEqual(save["Owner"], wolfProbeOwner) || !e.tamed {
			t.Errorf("valid owned state Owner%#v tame%v", save["Owner"], e.tamed)
		}
	} else {
		if _, exists := save["Owner"]; exists || e.tamed || e.ownerUUID != ([16]byte{}) {
			t.Errorf("unowned state retained Owner%#v tame%v uuid%x", save["Owner"], e.tamed, e.ownerUUID)
		}
	}
	attrs, _ := save["attributes"].(nbt.List)
	found := false
	for _, v := range attrs.Items {
		m := v.(map[string]any)
		if m["id"] == "minecraft:max_health" {
			if found {
				t.Fatal("duplicate max-health identity")
			}
			found = true
			if m["base"] != base {
				t.Errorf("saved base %#v want%g", m["base"], base)
			}
		}
	}
	if !found {
		t.Error("materialized max-health instance absent")
	}
}

func TestWolfLoadHealthOrdering(t *testing.T) {
	for _, path := range []string{"raw", "live", "cold"} {
		for _, tc := range []struct {
			name                                                    string
			inputBase, inputHealth, initial, afterDamage, afterTick float64
			owned, damage                                           bool
		}{
			{"unowned_damage", 40, 100, 40, 8, 8, false, true},
			{"unowned_frame", 40, 100, 40, 40, 8, false, false},
			{"owned_damage", 20, 100, 20, 18, 18, true, true},
			{"owned_no_heal", 20, 100, 20, 20, 20, true, false},
			{"normalized_unowned", 8, 40, 8, 8, 8, false, false},
			{"normalized_owned_no_heal", 40, 20, 20, 20, 20, true, false},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				s := nativeSaveTestServer(t)
				enforceSession(t, s, "P")
				wantedBase := tc.inputBase
				if !tc.owned {
					wantedBase = 8
				}
				owner := ""
				if tc.owned {
					owner = ",Owner:[I;286331153,286331153,286331153,286331153]"
				}
				fields := fmt.Sprintf(`attributes:[{id:"minecraft:max_health",base:%gd}],Health:%gf%s`, tc.inputBase, tc.inputHealth, owner)
				if path == "cold" {
					if err := s.SaveWorld(); err != nil {
						t.Fatal(err)
					}
					row := map[string]any{"id": "minecraft:wolf", "UUID": []int32{101, 202, 303, 404}, "Tags": nbt.List{Elem: nbt.TagString, Items: []any{"WolfProbe"}}, "Pos": nbt.List{Elem: nbt.TagDouble, Items: []any{float64(1), float64(0), float64(1)}}, "NoAI": int8(1), "NoGravity": int8(1), "Health": float32(tc.inputHealth), "attributes": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"id": "minecraft:max_health", "base": tc.inputBase}}}}
					if tc.owned {
						row["Owner"] = wolfProbeOwner
					}
					if err := world.SaveEntityRegions(world.DimensionDir(s.WorldDir, dimOverworld), map[[2]int32][]map[string]any{{0, 0}: {row}}); err != nil {
						t.Fatal(err)
					}
					loaded := nativeSaveTestServer(t)
					loaded.WorldDir = s.WorldDir
					if err := loaded.LoadWorld(); err != nil {
						t.Fatal(err)
					}
					enforceSession(t, loaded, "P")
					s = loaded
				} else {
					tag := `{Tags:["WolfProbe"],NoAI:1b,NoGravity:1b}`
					if path == "raw" {
						tag = `{Tags:["WolfProbe"],NoAI:1b,NoGravity:1b,` + fields + `}`
					}
					s.runSummon("P", []string{"wolf", "1", "0", "1", tag})
					if path == "live" {
						s.runData("P", []string{"merge", "entity", "@e[tag=WolfProbe,limit=1]", "{" + fields + "}"})
					}
				}
				e := wolfProbeBody(t, s)
				checkWolfLoadState(t, s, e, wantedBase, tc.initial, tc.owned)
				if tc.damage {
					s.damageMob(e, 2, "generic", nil, nil)
				}
				checkWolfLoadState(t, s, e, wantedBase, tc.afterDamage, tc.owned)
				s.mobTickStep()
				checkWolfLoadState(t, s, e, wantedBase, tc.afterTick, tc.owned)
				if err := s.SaveWorld(); err != nil {
					t.Fatal(err)
				}
				loaded := nativeSaveTestServer(t)
				loaded.WorldDir = s.WorldDir
				if err := loaded.LoadWorld(); err != nil {
					t.Fatal(err)
				}
				enforceSession(t, loaded, "P")
				checkWolfLoadState(t, loaded, wolfProbeBody(t, loaded), wantedBase, tc.afterTick, tc.owned)
			})
		}
	}
}

func TestWolfValidOwnerRemovalLoadOrder(t *testing.T) {
	s := nativeSaveTestServer(t)
	enforceSession(t, s, "P")
	s.runSummon("P", []string{"wolf", "1", "0", "1", `{Tags:["WolfProbe"],NoAI:1b,NoGravity:1b,Owner:[I;286331153,286331153,286331153,286331153],attributes:[{id:"minecraft:max_health",base:20d}],Health:100f}`})
	e := wolfProbeBody(t, s)
	checkWolfLoadState(t, s, e, 20, 20, true)
	s.runData("P", []string{"remove", "entity", "@e[tag=WolfProbe,limit=1]", "Owner"})
	checkWolfLoadState(t, s, e, 8, 20, false)
	s.mobTickStep()
	checkWolfLoadState(t, s, e, 8, 8, false)
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	loaded := nativeSaveTestServer(t)
	loaded.WorldDir = s.WorldDir
	if err := loaded.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	enforceSession(t, loaded, "P")
	checkWolfLoadState(t, loaded, wolfProbeBody(t, loaded), 8, 8, false)
}

func TestWolfDamageClampKeepsAppliedAmount(t *testing.T) {
	s := nativeSaveTestServer(t)
	enforceSession(t, s, "P")
	s.runSummon("P", []string{"wolf", "1", "0", "1", `{Tags:["WolfProbe"],NoAI:1b,NoGravity:1b,attributes:[{id:"minecraft:max_health",base:40d}],Health:100f}`})
	e := wolfProbeBody(t, s)
	checkWolfLoadState(t, s, e, 8, 40, false)
	result := s.entities.Damage(e.ID, 2, "generic", 0)
	// The independent class-maximum setter reduces transient Health40 to8.
	// Preserve the applied amount2 rather than reporting that full32 HP delta.
	if !result.Exists || result.Graced || result.Absorbed || result.Health != 8 || result.Dealt != 2 || e.Health != 8 {
		t.Fatalf("damage amount/clamp result=%#v Health%v", result, e.Health)
	}
}
