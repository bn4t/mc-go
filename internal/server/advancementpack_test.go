package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdvancementPacksAreServerOwnedAndKeepUnattachedDefinitions(t *testing.T) {
	directory := t.TempDir()
	definitions := map[string]string{
		"good":            `{"criteria":{"manual":{"trigger":"minecraft:impossible"}}}`,
		"unknown_trigger": `{"criteria":{"manual":{"trigger":"test:unknown"}}}`,
		"bad_requirement": `{"criteria":{"manual":{"trigger":"minecraft:impossible"}},"requirements":[["other"]]}`,
		"missing_parent":  `{"parent":"packtest:missing","criteria":{"manual":{"trigger":"minecraft:impossible"}}}`,
		"cycle_a":         `{"parent":"packtest:cycle_b","criteria":{"manual":{"trigger":"minecraft:impossible"}}}`,
		"cycle_b":         `{"parent":"packtest:cycle_a","criteria":{"manual":{"trigger":"minecraft:impossible"}}}`,
		"bad_json":        `{`,
	}
	root := filepath.Join(directory, "datapacks", "test")
	if err := os.MkdirAll(filepath.Join(root, "data", "packtest", "advancement"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.mcmeta"), []byte(`{"pack":{"min_format":107,"max_format":107,"description":"test"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	for name, body := range definitions {
		if err := os.WriteFile(filepath.Join(root, "data", "packtest", "advancement", name+".json"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	first, second := &Server{WorldDir: directory}, &Server{WorldDir: t.TempDir()}
	first.loadAdvancementPacks()
	second.loadAdvancementPacks()
	if got := first.advAncestors("packtest:cycle_a"); len(got) != 1 || got[0].id != "packtest:cycle_a" {
		t.Fatalf("unattached command traversal %v", got)
	}
	if first.gameAdvTable()["packtest:good"] == nil {
		t.Fatal("valid definition not loaded")
	}
	for name := range definitions {
		id := "packtest:" + name
		if second.gameAdvTable()[id] != nil || gameAdvs[id] != nil {
			t.Fatalf("definition leaked between servers: %s", id)
		}
		attachedDefinition := name == "good" || name == "missing_parent" || name == "cycle_a" || name == "cycle_b"
		if !attachedDefinition && first.gameAdvTable()[id] != nil {
			t.Errorf("invalid schema registered: %s", id)
		} else if attachedDefinition && first.gameAdvTable()[id] == nil {
			t.Errorf("valid resource definition missing: %s", id)
		}
	}
	if visible := computeAdvVisibilityIn(map[string]bool{"packtest:removed": true}, first.gameAdvTable(), first.gameAdvChildren(), first.gameAdvRoots()); visible["packtest:removed"] {
		t.Fatal("unknown persisted progress became visible")
	}
}

func TestDisplaylessAdvancementCommandName(t *testing.T) {
	if got := advChatComponent(&advMeta{id: "packtest:manual"}).plain(); got != "packtest:manual" {
		t.Fatalf("displayless advancement name %q", got)
	}
}
