package server

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func assertEntityTagSave(t *testing.T, e *Entity, s *Server, want []string) {
	t.Helper()
	got := mobTagList(e)
	sort.Strings(got)
	expected := append([]string(nil), want...)
	sort.Strings(expected)
	if len(got) != len(expected) || !reflect.DeepEqual(got, expected) && len(got) > 0 {
		t.Errorf("tags%v want%v", got, expected)
	}
	save := entitySaveMap(e, s.clock.count, s.entities).m
	value, present := save["Tags"]
	if len(want) == 0 {
		if present {
			t.Errorf("empty Tags not omitted: %T %#v", value, value)
		}
	} else {
		list, ok := value.(nbt.List)
		if !ok || list.Elem != nbt.TagString || len(list.Items) != len(want) {
			t.Errorf("Tags persistent type%T value%#v", value, value)
		} else {
			for _, item := range list.Items {
				if _, ok := item.(string); !ok {
					t.Errorf("non-string tag%T", item)
				}
			}
		}
	}
	var encoded bytes.Buffer
	if err := nbt.WriteNamed(&encoded, "", save); err != nil {
		t.Errorf("tagged entity NBT encode: %v", err)
	}
}
func tagBodyByUUID(t *testing.T, s *Server, uuid [16]byte) *Entity {
	t.Helper()
	for _, copy := range s.entities.All() {
		if copy.UUID == uuid {
			e, _ := s.entities.GetPtr(copy.ID)
			return e
		}
	}
	t.Fatal("cold tagged body missing")
	return nil
}
func TestEntityTagCommandPersistence(t *testing.T) {
	for _, kind := range []string{"creeper", "pig"} {
		for _, mode := range []string{"add_duplicate", "remove_last", "empty_roundtrip", "remove_missing"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				seed := nativeSaveTestServer(t)
				if err := seed.SaveWorld(); err != nil {
					t.Fatal(err)
				}
				opaque := map[string]any{"text": "untouched", "bytes": []byte{3, 1, 4}, "nested": nbt.List{Elem: nbt.TagString, Items: []any{"a", "b"}}}
				row := map[string]any{"id": "minecraft:" + kind, "UUID": []int32{101, 202, 303, 404}, "Pos": nbt.List{Elem: nbt.TagDouble, Items: []any{float64(1), float64(0), float64(1)}}, "NoAI": int8(1), "NoGravity": int8(1), "TagOpaque": opaque}
				want := []string{"Seed"}
				if mode == "empty_roundtrip" {
					want = nil
				} else {
					row["Tags"] = nbt.List{Elem: nbt.TagString, Items: []any{"Seed"}}
				}
				if err := world.SaveEntityRegions(world.DimensionDir(seed.WorldDir, dimOverworld), map[[2]int32][]map[string]any{{0, 0}: {row}}); err != nil {
					t.Fatal(err)
				}
				s := nativeSaveTestServer(t)
				s.WorldDir = seed.WorldDir
				if err := s.LoadWorld(); err != nil {
					t.Fatal(err)
				}
				enforceSession(t, s, "P")
				uuid := uuidFromInts([]int32{101, 202, 303, 404})
				e := tagBodyByUUID(t, s, uuid)
				target := "@e[type=" + kind + "]"
				switch mode {
				case "add_duplicate":
					s.runTag("P", []string{target, "add", "Witness"})
					duplicate := s.runTag("P", []string{target, "add", "Witness"})
					if duplicate.outcome != commandFailed || len(duplicate.comp.extra) != 1 || duplicate.comp.extra[0].translate != "commands.tag.add.failed" {
						t.Error("duplicate tag add did not fail")
					}
					want = append(want, "Witness")
				case "remove_last":
					s.runTag("P", []string{target, "remove", "Seed"})
					want = nil
				case "empty_roundtrip":
					s.runTag("P", []string{target, "add", "Temporary"})
					s.runTag("P", []string{target, "remove", "Temporary"})
				case "remove_missing":
					reply := s.runTag("P", []string{target, "remove", "Missing"})
					if reply.outcome != commandFailed || len(reply.comp.extra) != 1 || reply.comp.extra[0].translate != "commands.tag.remove.failed" {
						t.Error("missing tag remove did not fail")
					}
				}
				assertEntityTagSave(t, e, s, want)
				if !reflect.DeepEqual(e.ExtraNBT["TagOpaque"], opaque) {
					t.Fatal("tag command changed unrelated ExtraNBT")
				}
				if err := s.SaveWorld(); err != nil {
					t.Fatal("actual tag command full save", err)
				}
				loaded := nativeSaveTestServer(t)
				loaded.WorldDir = s.WorldDir
				if err := loaded.LoadWorld(); err != nil {
					t.Fatal(err)
				}
				assertEntityTagSave(t, tagBodyByUUID(t, loaded, uuid), loaded, want)
				if !reflect.DeepEqual(tagBodyByUUID(t, loaded, uuid).ExtraNBT["TagOpaque"], opaque) {
					t.Fatal("cold tag save lost unrelated ExtraNBT")
				}
			})
		}
	}
}

func TestMobTagListCanonicalOwnership(t *testing.T) {
	e := &Entity{Type: mustEntityType("pig"), Living: true, Health: 10, MaxHealth: 10, ExtraNBT: map[string]any{"Other": "keep"}}
	input := []string{"one", "two"}
	setMobTagList(e, input)
	input[0] = "caller changed"
	list, ok := e.ExtraNBT["Tags"].(nbt.List)
	if !ok || list.Elem != nbt.TagString || !reflect.DeepEqual(list.Items, []any{"one", "two"}) {
		t.Fatalf("canonical detached Tags=%#v", e.ExtraNBT["Tags"])
	}
	snapshot := cloneNativeNBT(e.ExtraNBT).(map[string]any)
	read := mobTagList(e)
	read[0] = "reader changed"
	setMobTagList(e, []string{"later"})
	if !reflect.DeepEqual(snapshot["Tags"], nbt.List{Elem: nbt.TagString, Items: []any{"one", "two"}}) {
		t.Fatal("tag mutation changed detached snapshot")
	}
	setMobTagList(e, nil)
	if _, ok := e.ExtraNBT["Tags"]; ok || e.ExtraNBT["Other"] != "keep" {
		t.Fatal("empty tag mutation changed other data or retained Tags")
	}
}

// Explicit proof input is an ignored real-native artifact, never a committed
// raw region. Default skip is reported separately from the actual named run.
func TestNativeLoadedEntityTagPersistenceProof(t *testing.T) {
	file := os.Getenv("MCGO_COLD_NATIVE_ENTITY_FILE")
	if file == "" {
		t.Skip("set MCGO_COLD_NATIVE_ENTITY_FILE to ignored native four-creeper region")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != "254f037b2094ca7e339aa642f67a5bb01cc322db2d7d3b137c1ea96498311018" {
		t.Fatal("unrecognized native proof input")
	}
	seed := nativeSaveTestServer(t)
	if err := seed.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(world.DimensionDir(seed.WorldDir, dimOverworld), "entities")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "r.0.0.mca"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	s := nativeSaveTestServer(t)
	s.WorldDir = seed.WorldDir
	if err := s.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	enforceSession(t, s, "P")
	before := map[[16]byte]map[string]any{}
	tags := map[[16]byte][]string{}
	for _, copy := range s.entities.All() {
		if name, _ := EntityTypeName(copy.Type); name == "creeper" {
			e, _ := s.entities.GetPtr(copy.ID)
			before[e.UUID] = cloneNativeNBT(entitySaveMap(e, s.clock.count, s.entities).m).(map[string]any)
			tags[e.UUID] = mobTagList(e)
		}
	}
	if len(before) != 4 {
		t.Fatalf("actual native creepers%d want4", len(before))
	}
	s.runTag("P", []string{"@e[type=creeper]", "add", "GoSaveWitness"})
	s.runTag("P", []string{"@e[type=creeper]", "add", "GoSaveWitness"})
	for uuid, old := range before {
		e := tagBodyByUUID(t, s, uuid)
		assertEntityTagSave(t, e, s, append(append([]string(nil), tags[uuid]...), "GoSaveWitness"))
		now := cloneNativeNBT(entitySaveMap(e, s.clock.count, s.entities).m).(map[string]any)
		delete(old, "Tags")
		delete(now, "Tags")
		if !reflect.DeepEqual(old, now) {
			t.Fatal("actual native tag mutation changed another saved field")
		}
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal("native-loaded /tag full save", err)
	}
	if export := os.Getenv("MCGO_ENTITY_TAG_EXPORT_DIR"); export != "" {
		dest := filepath.Join(export, "entities")
		if err := os.MkdirAll(dest, 0755); err != nil {
			t.Fatal(err)
		}
		files, err := filepath.Glob(filepath.Join(dir, "*.mca"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dest, filepath.Base(path)), data, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	cold := nativeSaveTestServer(t)
	cold.WorldDir = s.WorldDir
	if err := cold.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	enforceSession(t, cold, "P")
	for uuid := range before {
		assertEntityTagSave(t, tagBodyByUUID(t, cold, uuid), cold, append(append([]string(nil), tags[uuid]...), "GoSaveWitness"))
	}
	cold.runTag("P", []string{"@e[type=creeper]", "remove", "GoSaveWitness"})
	for uuid := range before {
		assertEntityTagSave(t, tagBodyByUUID(t, cold, uuid), cold, tags[uuid])
	}
	if err := cold.SaveWorld(); err != nil {
		t.Fatal("native-loaded /tag removal save", err)
	}
	t.Log("ACTUAL_NATIVE_TAG_PROOF bodies4 add/duplicate/save/cold/remove/save PASS")
}
