package server

import (
	"bytes"
	"mc-go/internal/nbt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTestInstancePackCanonicalCodec(t *testing.T) {
	raw, err := decodeGameTestJSON([]byte(`{"type":"function","environment":"default","structure":"test:room","function":"always_pass","max_ticks":4294967301,"setup_ticks":0,"required":false,"ignored":"field"}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseGameTestDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"type": "minecraft:function", "environment": "minecraft:default", "structure": "test:room", "function": "minecraft:always_pass", "max_ticks": int32(5), "required": int8(0)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical definition %#v", got)
	}
	for _, body := range []string{
		`{"type":"function","environment":"default","structure":"test:room","function":"always_pass","max_ticks":0}`,
		`{"type":"block_based","environment":"default","structure":"test:room","max_ticks":1,"setup_ticks":-1}`,
		`{"type":"block_based","environment":"default","structure":"test:room","max_ticks":1,"padding":129}`,
		`{"type":"block_based","environment":"default","structure":"test:room","max_ticks":1,"required":1}`,
		`{"type":"block_based","environment":"default","structure":"test:room","max_ticks":1,"rotation":"bad"}`,
	} {
		raw, err := decodeGameTestJSON([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseGameTestDefinition(raw); err == nil {
			t.Fatal("invalid definition accepted", body)
		}
	}
	raw, err = decodeGameTestJSON([]byte(`{"type":"all_of","definitions":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	env, err := parseGameTestEnvironment(raw)
	if err != nil || !reflect.DeepEqual(env, map[string]any{"type": "minecraft:all_of", "definitions": nbt.List{Elem: nbt.TagEnd}}) {
		t.Fatal("environment", env, err)
	}
}

func TestTestInstancePackIsolationAndRegistryRows(t *testing.T) {
	first, second := New("unused"), New("unused")
	first.WorldDir = t.TempDir()
	second.WorldDir = t.TempDir()
	resources := map[string]string{
		"pack.mcmeta": `{"pack":{"min_format":107,"max_format":107,"description":"unit"}}`,
		"data/testpin/test_environment/empty.json": `{"type":"minecraft:all_of","definitions":[]}`,
		"data/testpin/test_instance/block.json":    `{"type":"minecraft:block_based","environment":"testpin:empty","structure":"testpin:shared","max_ticks":5}`,
		"data/testpin/test_instance/function.json": `{"type":"minecraft:function","environment":"testpin:empty","structure":"testpin:shared","function":"minecraft:always_pass","max_ticks":1,"required":false}`,
	}
	for name, body := range resources {
		path := filepath.Join(first.WorldDir, "datapacks", "pin", name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.loadTestInstancePacks(); err != nil {
		t.Fatal(err)
	}
	if err := second.loadTestInstancePacks(); err != nil {
		t.Fatal(err)
	}
	if first.testInstanceDefinition("testpin:block") == nil || second.testInstanceDefinition("testpin:block") != nil || testInstanceDefinition("testpin:block") != nil {
		t.Fatal("test registry escaped server ownership")
	}
	for _, version := range []int32{775, 776} {
		cfg, err := javaConfigFor(version)
		if err != nil {
			t.Fatal(err)
		}
		for _, reg := range cfg.registries {
			if reg.key != "minecraft:test_instance" && reg.key != "minecraft:test_environment" {
				continue
			}
			before := append([]byte(nil), reg.body...)
			baseRows, err := gameTestRegistryRows(reg.body)
			if err != nil {
				t.Fatal(err)
			}
			body, err := first.gameTestConfigBody(reg)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := gameTestRegistryRows(body)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) <= len(baseRows) || rows[0].name != baseRows[0].name || !bytes.Equal(rows[0].data, baseRows[0].data) {
				t.Fatalf("builtin row changed %s %#v", reg.key, rows)
			}
			if reg.key == "minecraft:test_instance" {
				if len(rows) != 3 || rows[1].name != "testpin:block" || rows[2].name != "testpin:function" {
					t.Fatal("test indices", rows)
				}
			} else {
				if len(rows) != 2 || rows[1].name != "testpin:empty" {
					t.Fatal("environment indices", rows)
				}
			}
			unaffected, err := second.gameTestConfigBody(reg)
			if err != nil || !bytes.Equal(unaffected, before) || !bytes.Equal(reg.body, before) {
				t.Fatal("global/second-server registry changed", err)
			}
		}
	}
}
