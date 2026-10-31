package server

import (
	"bytes"
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"os"
	"path/filepath"
	"testing"
)

func TestCreeperNativeFixtureEntityExport(t *testing.T) {
	seed := nativeSaveTestServer(t)
	if err := seed.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	input := os.Getenv("MCGO_COLD_NATIVE_ENTITY_FILE")
	if input == "" {
		t.Skip("set MCGO_COLD_NATIVE_ENTITY_FILE to measured closed native entity region")
	}
	raw, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	dir := world.DimensionDir(seed.WorldDir, dimOverworld)
	if err := os.MkdirAll(filepath.Join(dir, "entities"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "entities", "r.0.0.mca"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	loaded := nativeSaveTestServer(t)
	loaded.WorldDir = seed.WorldDir
	if err := loaded.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, copy := range loaded.entities.All() {
		if name, _ := EntityTypeName(copy.Type); name != "creeper" {
			continue
		}
		count++
		e, _ := loaded.entities.GetPtr(copy.ID)
		if e.Swell != 0 {
			t.Error("fresh native fixture retained transient swell")
		}
		// A changed modeled field proves this is a newly captured entity export.
		e.AirSupply = 299
		view := entitySaveMap(e, loaded.clock.count, loaded.entities).m
		var b bytes.Buffer
		if err := nbt.WriteNamed(&b, "", view); err != nil {
			t.Errorf("actual native fixture UUID%x entity compound encode: %v", e.UUID, err)
			for k, v := range view {
				var field bytes.Buffer
				if err := nbt.WriteNamed(&field, "", map[string]any{k: v}); err != nil {
					t.Errorf("unencodable actual field %s type%T value%#v: %v", k, v, v, err)
				}
			}
		}
	}
	if count != 4 {
		t.Fatalf("actual fixture restored %d creepers", count)
	}
	if t.Failed() {
		return
	}
	if err := loaded.saveEntityRegions(loaded.dims[dimOverworld]); err != nil {
		t.Fatal("entity-only native export", err)
	}
	rows, err := world.LoadEntityRegions(world.DimensionDir(loaded.WorldDir, dimOverworld))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatal("entity-only output count", len(rows))
	}
	for _, row := range rows {
		if row["Air"] != int16(299) {
			t.Fatal("entity-only fresh capture witness absent")
		}
	}
	if out := os.Getenv("MCGO_COLD_ENTITY_EXPORT_FILE"); out != "" {
		if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(world.DimensionDir(loaded.WorldDir, dimOverworld), "entities", "r.0.0.mca"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}

}
