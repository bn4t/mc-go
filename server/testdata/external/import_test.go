package publicconsumer_test

import (
	"testing"

	"mc-go/api/registry"
	"mc-go/server"
)

func TestPublicRuntimeProfilesAndCanonicalRegistry(t *testing.T) {
	runtime := server.New("unused")
	t.Cleanup(runtime.Stop)
	profiles := server.JavaProfiles()
	admitted := map[int32]bool{}
	for _, profile := range profiles {
		admitted[profile.Protocol] = profile.Admission
	}
	if !admitted[776] || !admitted[775] || len(admitted) != 2 {
		t.Fatalf("profile admission = %v", admitted)
	}
	for _, profile := range profiles {
		if len(profile.Missing) != 0 {
			t.Fatalf("admitted profile has readiness gates: %+v", profile)
		}
		if profile.Protocol == 775 && (profile.BlockProjectionComplete || profile.MissingBlockStates != 2493) {
			t.Fatalf("775 must expose strict newer-content exclusions: %+v", profile)
		}
	}
	key, err := registry.NewStateKey("example:stone", nil)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := registry.NewBlockCatalog([]registry.BlockDefinition{{State: key, Default: true}})
	if err != nil {
		t.Fatal(err)
	}
	id, ok := catalog.Lookup(key)
	if !ok {
		t.Fatal("public canonical identity was not resolved")
	}
	mapping, err := registry.NewBlockMapping(900, catalog, []registry.BlockWireEntry{{State: key, WireID: 77}}, registry.MissingBlockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if wire, err := mapping.Encode(id); err != nil || wire != 77 {
		t.Fatalf("public mapping = %d, %v", wire, err)
	}
	for _, entry := range []struct {
		catalog *registry.NameCatalog
		name    string
	}{
		{runtime.ItemCatalog(), "minecraft:crafting_table"},
		{runtime.ComponentCatalog(), "minecraft:damage"},
	} {
		handle, ok := entry.catalog.Lookup(entry.name)
		if !ok {
			t.Fatalf("public named catalog lacks %s", entry.name)
		}
		if name, ok := entry.catalog.Name(handle); !ok || name != entry.name {
			t.Fatalf("public named handle roundtrip = %q, %v", name, ok)
		}
	}
}
