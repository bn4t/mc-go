package server

import "testing"

func TestJavaBuiltinDomainsProjectMeasured775IDs(t *testing.T) {
	profile, _ := javaProfileFor(775)
	for _, tc := range []struct {
		key, name    string
		current, old int32
	}{
		{"sound_event", "entity.generic.explode", 699, 697},
		{"particle_type", "block", 1, 1},
		{"block_entity_type", "chest", 1, 1},
		{"block_entity_type", "barrel", 26, 27},
	} {
		t.Run(tc.key+"/"+tc.name, func(t *testing.T) {
			key := "minecraft:" + tc.key
			got, err := profile.projectBuiltinRegistryID(key, tc.current)
			if err != nil || got != tc.old {
				t.Fatalf("projection=%d,%v want%d", got, err, tc.old)
			}
			if name := profile.builtinRegistryName(key, got); name != "minecraft:"+tc.name {
				t.Fatalf("wire name=%q", name)
			}
			canonical, err := profile.canonicalBuiltinRegistryID(key, got)
			if err != nil || canonical != tc.current {
				t.Fatalf("reverse=%d,%v", canonical, err)
			}
		})
	}
}

func TestJavaBuiltinDomainsMissingEntriesAreExplicit(t *testing.T) {
	old, _ := javaProfileFor(775)
	current, _ := javaProfileFor(776)
	for _, tc := range []struct{ key, name string }{
		{"minecraft:particle_type", "minecraft:geyser"},
		{"minecraft:block_entity_type", "minecraft:potent_sulfur"},
		{"minecraft:command_argument_type", "minecraft:team_color"},
	} {
		id, err := current.builtinRegistryID(tc.key, tc.name)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := old.projectBuiltinRegistryID(tc.key, id); err == nil || got != -1 {
			t.Fatalf("missing entry got numeric fallback: %d,%v", got, err)
		}
	}
	bed, err := old.builtinRegistryID("minecraft:block_entity_type", "minecraft:bed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.canonicalBuiltinRegistryID("minecraft:block_entity_type", bed); err == nil {
		t.Fatal("775-only bed falsely assigned a776 type")
	}
	if _, err := old.projectBuiltinRegistryID("minecraft:sound_event", -1); err == nil {
		t.Fatal("negative canonical ID accepted")
	}
	if _, err := old.projectBuiltinRegistryID("minecraft:no_registry", 0); err == nil {
		t.Fatal("unknown registry got identity fallback")
	}
}

func TestJavaBuiltinDomainsNamesAreDetached(t *testing.T) {
	names := builtinRegistryNames(775, "minecraft:particle_type")
	if len(names) != 117 {
		t.Fatalf("775 particles=%d", len(names))
	}
	names[0] = "mutated"
	if builtinRegistryNames(775, "minecraft:particle_type")[0] == "mutated" {
		t.Fatal("catalogbootstrap aliases immutable projection names")
	}
}
