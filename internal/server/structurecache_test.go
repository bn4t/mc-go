package server

import (
	"compress/gzip"
	"os"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

func writeStructureCacheForTest(t *testing.T, s *Server, name, block string) {
	t.Helper()
	f, err := s.structureTemplateFile(name, true)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	state, _ := world.BlockState(block)
	if err := nbt.WriteNamed(zw, "", oneBlockStructureForTest(state, nil)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
func structureCacheBlockForTest(value map[string]any) string {
	if value == nil {
		return ""
	}
	palette, _ := value["palette"].(nbt.List)
	if len(palette.Items) == 0 {
		return ""
	}
	state, _ := palette.Items[0].(map[string]any)
	name, _ := state["Name"].(string)
	return name
}
func TestStructureCacheMissAndReloadLifecycle(t *testing.T) {
	s := simulationTestServer(t)
	s.WorldDir = t.TempDir()
	s.simulationAction(func() {
		name := "test:cache_late"
		if s.structureTemplateGet(name) != nil {
			t.Fatal("missing file resolved")
		}
		writeStructureCacheForTest(t, s, name, "gold_block")
		if s.structureTemplateGet(name) != nil {
			t.Fatal("missing lookup was not retained")
		}
		s.runReloadCmd("", nil)
		if got := structureCacheBlockForTest(s.structureTemplateGet(name)); got != "minecraft:gold_block" {
			t.Fatalf("reload missing cache: %s", got)
		}
		writeStructureCacheForTest(t, s, name, "diamond_block")
		s.runReloadCmd("", []string{"unexpected"})
		if got := structureCacheBlockForTest(s.structureTemplateGet(name)); got != "minecraft:gold_block" {
			t.Fatalf("positive cache changed before valid reload: %s", got)
		}
		s.runReloadCmd("", nil)
		if got := structureCacheBlockForTest(s.structureTemplateGet(name)); got != "minecraft:diamond_block" {
			t.Fatalf("reload positive cache: %s", got)
		}
	})
}
func TestStructureCacheMalformedGeneratedFallsBack(t *testing.T) {
	s := simulationTestServer(t)
	s.WorldDir = t.TempDir()
	s.simulationAction(func() {
		name := "minecraft:igloo/top"
		f, err := s.structureTemplateFile(name, true)
		if err != nil {
			t.Fatal(err)
		}
		zw := gzip.NewWriter(f)
		_, _ = zw.Write([]byte{1, 2, 3})
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		got := s.structureTemplateGet(name)
		if got == nil || structureVector(got["size"]) != [3]int32{7, 5, 8} {
			t.Fatal("malformed generated source masked builtin igloo")
		}
		writeStructureCacheForTest(t, s, name, "gold_block")
		s.runReloadCmd("", nil)
		if got := structureCacheBlockForTest(s.structureTemplateGet(name)); got != "minecraft:gold_block" {
			t.Fatalf("valid generated source lost priority: %s", got)
		}
	})
}

func TestStructureCacheGzipRootReadBoundary(t *testing.T) {
	s := simulationTestServer(t)
	s.WorldDir = t.TempDir()
	s.simulationAction(func() {
		name := "minecraft:igloo/top"
		writeStructureCacheForTest(t, s, name, "gold_block")
		path, _ := s.structureTemplatePath(name)
		valid, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"valid", "crc", "footer", "body"} {
			raw := append([]byte(nil), valid...)
			switch kind {
			case "crc":
				raw[len(raw)-8] ^= 1
			case "footer":
				raw = raw[:len(raw)-8]
			case "body":
				raw = raw[:len(raw)/2]
			}
			if err := os.WriteFile(path, raw, 0644); err != nil {
				t.Fatal(err)
			}
			s.runReloadCmd("", nil)
			got := s.structureTemplateGet(name)
			if kind == "body" {
				if got == nil || structureVector(got["size"]) != [3]int32{7, 5, 8} {
					t.Errorf("incomplete compound did not fall back")
				}
			} else if structureCacheBlockForTest(got) != "minecraft:gold_block" {
				t.Errorf("complete compound rejected for gzip%s", kind)
			}
		}
	})
}
