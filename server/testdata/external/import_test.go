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
	if !admitted[776] || admitted[775] || len(admitted) != 2 {
		t.Fatalf("profile admission = %v", admitted)
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
}
