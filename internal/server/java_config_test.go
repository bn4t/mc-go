package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestJava775ConfigurationMatchesCapturedManifest(t *testing.T) {
	base := "testdata/vanilla-26.1/config"
	manifest, err := os.ReadFile(filepath.Join(base, "capture_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Protocol     int32  `json:"protocol"`
		OracleSHA256 string `json:"oracle_sha256"`
		Packets      []struct {
			File     string `json:"file"`
			Hash     string `json:"sha256"`
			ID       int32  `json:"packet_id"`
			Registry string `json:"registry"`
			Entries  int    `json:"entries"`
		} `json:"packets"`
	}
	if err = json.Unmarshal(manifest, &source); err != nil {
		t.Fatal(err)
	}
	if source.Protocol != 775 || len(source.OracleSHA256) != 64 || len(source.Packets) != 31 {
		t.Fatalf("incomplete capture provenance: %+v", source)
	}
	s := New("unused")
	defer s.Stop()
	p, _ := javaProfileFor(775)
	// Only exercise candidate codecs; this test does not bypass server login
	// admission. Packet IDs in configuration were measured identical on both.
	sess := &Session{server: s, ver: Version{Protocol: 775, IDs: LatestVersion().IDs}, profile: p, sendCh: make(chan sendPkt, 64), done: make(chan struct{})}
	if err = s.sendFeatureFlags(sess); err != nil {
		t.Fatal(err)
	}
	if err = s.sendKnownPacks(sess); err != nil {
		t.Fatal(err)
	}
	if err = s.sendRegistries(sess); err != nil {
		t.Fatal(err)
	}
	if err = s.sendUpdateTags(sess); err != nil {
		t.Fatal(err)
	}
	expected := map[string]struct {
		ID   int32
		Hash string
	}{}
	for _, packet := range source.Packets {
		expected[packet.File] = struct {
			ID   int32
			Hash string
		}{packet.ID, packet.Hash}
	}
	ordered := []string{"feature_flags.bin", "known_packs.bin"}
	for _, reg := range p.config.registries {
		for _, packet := range source.Packets {
			if packet.Registry == reg.key {
				ordered = append(ordered, packet.File)
			}
		}
	}
	ordered = append(ordered, "tags.bin")
	if len(ordered) != 31 || len(sess.sendCh) != 31 {
		t.Fatalf("config burst files=%d packets=%d", len(ordered), len(sess.sendCh))
	}
	for _, name := range ordered {
		packet := materializeOutboundPacket(t, sess, <-sess.sendCh)
		want := expected[name]
		hash := sha256.Sum256(packet.data)
		if packet.id != want.ID || hex.EncodeToString(hash[:]) != want.Hash {
			t.Fatalf("%s selected wrong profile payload: id=%d hash=%x", name, packet.id, hash)
		}
	}
	if len(p.config.registries) != 28 || p.registryEntryID("minecraft:sulfur_cube_archetype", "minecraft:default") != -1 {
		t.Fatal("775 contains a 776-only registry")
	}
	if p.config.knownPacks[0].version != "26.1" {
		t.Fatal("775 advertised another pack version")
	}
}

func TestJavaSyncedRegistryProjectionsPreserveNames(t *testing.T) {
	current, _ := javaProfileFor(776)
	previous, _ := javaProfileFor(775)
	moved, missing := 0, 0
	for key, names := range current.config.names {
		for runtime, name := range names {
			id, err := previous.projectRegistryID(key, int32(runtime))
			expected := previous.registryEntryID(key, name)
			if expected < 0 {
				missing++
				if !errors.Is(err, ErrJavaRegistryProjection) {
					t.Fatalf("%s %s silently projected missing entry", key, name)
				}
				continue
			}
			if err != nil || id != expected || previous.registryEntryName(key, id) != name {
				t.Fatalf("%s %s projection=%d,%v", key, name, id, err)
			}
			back, err := previous.canonicalRegistryID(key, id)
			if err != nil || back != int32(runtime) {
				t.Fatalf("%s %s reverse=%d,%v", key, name, back, err)
			}
			if id != int32(runtime) {
				moved++
			}
			identity, err := current.projectRegistryID(key, int32(runtime))
			if err != nil || identity != int32(runtime) {
				t.Fatalf("776 %s %s identity=%d,%v", key, name, identity, err)
			}
		}
	}
	if moved == 0 || missing == 0 {
		t.Fatalf("vacuous projection: moved=%d missing=%d", moved, missing)
	}
	for _, id := range []int32{-1, 1 << 30} {
		if _, err := previous.projectRegistryID("minecraft:worldgen/biome", id); !errors.Is(err, ErrJavaRegistryProjection) {
			t.Fatalf("invalid canonical ID%d: %v", id, err)
		}
		if _, err := previous.canonicalRegistryID("minecraft:worldgen/biome", id); !errors.Is(err, ErrJavaRegistryProjection) {
			t.Fatalf("invalid wire ID%d: %v", id, err)
		}
	}
	t.Logf("synced registries: %d renumbered shared entries, %d canonical entries absent from775", moved, missing)
}
