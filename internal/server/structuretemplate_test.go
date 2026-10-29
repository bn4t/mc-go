package server

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

func TestStructureTemplateNativeDiskAndPlacement(t *testing.T) {
	s := simulationTestServer(t)
	s.WorldDir = t.TempDir()
	d := s.dims[dimOverworld]
	s.simulationAction(func() {
		controller := world.Pos{Y: -60}
		state, _ := world.BlockStateOf("structure_block", map[string]string{"mode": "save"})
		s.setSilentBlock(d, controller, state)
		g := s.structureBlockAt(d, controller)
		g.fields["name"], g.fields["author"] = "test:roundtrip", "Keeper"
		g.fields["posX"], g.fields["posY"], g.fields["posZ"] = int32(1), int32(0), int32(0)
		g.fields["sizeX"], g.fields["sizeY"], g.fields["sizeZ"] = int32(3), int32(1), int32(1)
		g.fields["ignoreEntities"] = int8(0)
		stone, _ := world.BlockState("stone")
		s.setSilentBlock(d, world.Pos{X: 1, Y: -60}, stone)
		bepos := world.Pos{X: 2, Y: -60}
		s.setSilentBlock(d, bepos, state)
		marker := s.structureBlockAt(d, bepos)
		marker.fields["name"], marker.fields["mode"], marker.fields["metadata"] = "test:marker", "CORNER", "retained"
		void, _ := world.BlockState("structure_void")
		s.setSilentBlock(d, world.Pos{X: 3, Y: -60}, void)
		mob, ok := s.summonMobRaw(d, "villager", 1.5, -60, .5, true, false)
		if !ok {
			t.Fatal("villager spawn")
		}
		mob.NoGravity = true
		if !s.structureBlockSave(d, controller, g, true) {
			t.Fatal("native template save failed")
		}
		path := filepath.Join(s.WorldDir, "generated", "test", "structure", "roundtrip.nbt")
		f, e := os.Open(path)
		if e != nil {
			t.Fatal(e)
		}
		zr, e := gzip.NewReader(f)
		if e != nil {
			t.Fatal(e)
		}
		_, value, e := nbt.ReadNamed(zr)
		_ = zr.Close()
		_ = f.Close()
		if e != nil {
			t.Fatal(e)
		}
		disk := value.(map[string]any)
		if disk["author"] != nil || disk["DataVersion"] != int32(world.DataVersion26_2) {
			t.Fatalf("native root fields%+v", disk)
		}
		blocks := disk["blocks"].(nbt.List)
		if len(blocks.Items) != 2 {
			t.Fatalf("captured%d blocks, structure_void should be absent", len(blocks.Items))
		}
		tag := blocks.Items[1].(map[string]any)["nbt"].(map[string]any)
		if tag["id"] != "minecraft:structure_block" || tag["x"] != nil || tag["metadata"] != "retained" {
			t.Fatalf("block entity saveWithId%+v", tag)
		}
		s.structureTemplates = nil // Exercise native compressed-file loading, not cache.
		target := world.Pos{X: 10, Y: -60}
		s.setSilentBlock(d, target, state)
		load := s.structureBlockAt(d, target)
		load.fields["mode"], load.fields["name"], load.fields["rotation"] = "LOAD", "test:roundtrip", "CLOCKWISE_90"
		load.fields["ignoreEntities"] = int8(0)
		preserved := world.Pos{X: 10, Y: -59, Z: 2}
		s.setSilentBlock(d, preserved, stone)
		if key := s.structureBlockLoad(d, target, load); key != "structure_block.load_prepare" {
			t.Fatal(key)
		}
		if load.fields["author"] != "?" {
			t.Fatalf("disk-loaded template author%v", load.fields["author"])
		}
		if key := s.structureBlockLoad(d, target, load); key != "structure_block.load_success" {
			t.Fatal(key)
		}
		if d.w.GetBlock(world.Pos{X: 10, Y: -59}) != stone || d.w.GetBlock(preserved) != stone {
			t.Fatal("rotation/structure_void placement mismatch")
		}
		placed := s.structureBlockAt(d, world.Pos{X: 10, Y: -59, Z: 1})
		if placed == nil || placed.fields["metadata"] != "retained" || placed.fields["mode"] != "CORNER" {
			t.Fatalf("placed block entity%+v", placed)
		}
		found := false
		for _, entity := range s.entities.All() {
			if entity.ID == mob.ID {
				continue
			}
			if name, _ := EntityTypeName(entity.Type); name == "villager" {
				found = true
				if entity.UUID == mob.UUID || entity.X != 10.5 || entity.Y != -59 || entity.Z != .5 || entity.Yaw != 90 || !entity.NoAI {
					t.Fatalf("placed native entity%+v", entity)
				}
			}
		}
		if !found {
			t.Fatal("included native entity was not placed")
		}
	})
}

func TestStructureTemplateIdentifierPathsAndContainment(t *testing.T) {
	s := simulationTestServer(t)
	s.WorldDir = t.TempDir()
	for _, tc := range []struct {
		raw, canonical string
		path           bool
	}{
		{"piece", "minecraft:piece", true}, {":piece", "minecraft:piece", true}, {"example:nested/piece", "example:nested/piece", true}, {".:piece", ".:piece", true},
		{"..:piece", "", false}, {"UPPER:piece", "", false}, {"bad!:piece", "", false}, {"example:../escape", "example:../escape", false}, {"example:/absolute", "example:/absolute", false}, {"example:a//b", "example:a//b", false}, {"example:a/./b", "example:a/./b", false}, {"example:a/../b", "example:a/../b", false},
		{"example:.", "example:.", true}, {"example:..", "example:..", true}, {"example:a/..", "example:a/..", true}, {"example:a/", "example:a/", true}, {"example:", "example:", true},
		{"example:con", "example:con", false}, {"example:com1", "example:com1", false}, {"example:lpt9/data", "example:lpt9/data", false}, {"example:com10", "example:com10", true}, {"example:a./data", "example:a./data", false},
	} {
		if got := structureName(tc.raw); got != tc.canonical {
			t.Errorf("name%q=>%q want%q", tc.raw, got, tc.canonical)
		}
		path, ok := s.structureTemplatePath(tc.canonical)
		if ok != tc.path {
			t.Errorf("path%q=>%q valid%v want%v", tc.raw, path, ok, tc.path)
		}
		if ok {
			root := filepath.Join(s.WorldDir, "generated")
			rel, e := filepath.Rel(root, path)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Fatalf("path escaped generated root%q", path)
			}
		}
	}
	outside := t.TempDir()
	generated := filepath.Join(s.WorldDir, "generated")
	if e := os.MkdirAll(generated, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(generated, "linked")); e != nil {
		t.Fatal(e)
	}
	if f, e := s.structureTemplateFile("linked:escape", true); e == nil {
		_ = f.Close()
		t.Fatal("symlink escaped generated root")
	}
	if entries, e := os.ReadDir(outside); e != nil || len(entries) != 0 {
		t.Fatalf("outside root changed entries%v err%v", entries, e)
	}
}
