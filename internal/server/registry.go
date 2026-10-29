package server

import (
	"bytes"
	"embed"
	"fmt"
	"sync"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

// Configuration registries and tags are served verbatim from bodies
// captured from the real 26.2 server (see tests/blackbox
// TestCaptureVanillaConfig). Captured protocol data is the exact
// ground truth: no transcription, no drift. The server sends the same
// registries in the same order vanilla does; entry order defines the
// numeric ids the play stage references.
//
//go:embed testdata/vanilla-26.2/config/registries/*.bin
var registryBodies embed.FS

//go:embed testdata/vanilla-26.2/config/tags.bin
var tagsBody []byte

type configRegistry struct {
	key  string
	body []byte
}

// entryIDs maps an entry name to its index in the registry.
type entryIDs map[string]int32

var (
	configOnce sync.Once
	configRegs []configRegistry
	// biomeIDs is the captured worldgen/biome name -> index map.
	biomeIDs entryIDs
	// dialogIDs is the captured minecraft:dialog name -> index map;
	// a show_dialog body references its dialog by index+1.
	dialogIDs entryIDs
)

// loadConfigData parses the embedded captures once. A corrupt capture
// is a build-time mistake, so it panics rather than failing sessions.
func loadConfigData() {
	configOnce.Do(func() {
		data, err := javaConfigFor(776)
		if err != nil {
			panic(err)
		}
		configRegs = data.registries
		biomeIDs = data.entries["minecraft:worldgen/biome"]
		dialogIDs = data.entries["minecraft:dialog"]
	})
}

// parseRegistryBody walks one captured Registry Data body: String key,
// VarInt count, then per entry String name, Bool data-present, and
// (when present) one unnamed NBT tag.
func parseRegistryBody(body []byte) (string, entryIDs, error) {
	br := bytes.NewReader(body)
	key, err := protocol.ReadString(br)
	if err != nil {
		return "", nil, err
	}
	count, err := protocol.ReadVarInt(br)
	if err != nil {
		return "", nil, err
	}
	if count < 0 || count > 1<<20 {
		return "", nil, fmt.Errorf("negative entry count %d", count)
	}
	ids := make(entryIDs, count)
	for i := int32(0); i < count; i++ {
		name, err := protocol.ReadString(br)
		if err != nil {
			return "", nil, err
		}
		present, err := protocol.ReadBool(br)
		if err != nil {
			return "", nil, err
		}
		if present {
			if _, err := nbt.ReadUnnamed(br); err != nil {
				return "", nil, fmt.Errorf("entry %s nbt: %w", name, err)
			}
		}
		if _, duplicate := ids[name]; duplicate {
			return "", nil, fmt.Errorf("duplicate registry entry %s", name)
		}
		ids[name] = i
	}
	if br.Len() != 0 {
		return "", nil, fmt.Errorf("registry %s has %d trailing bytes", key, br.Len())
	}
	return key, ids, nil
}

// biomeID returns the captured registry index for a biome, falling
// back to 0 for unknown names.
func biomeID(name string) int32 {
	loadConfigData()
	if id, ok := biomeIDs[name]; ok {
		return id
	}
	return 0
}

// registryEntryName returns the entry name at index id in a synced
// registry, or "" when either is unknown.
func registryEntryName(registryKey string, id int32) string {
	data, err := javaConfigFor(776)
	if err != nil || id < 0 || int64(id) >= int64(len(data.names[registryKey])) {
		return ""
	}
	return data.names[registryKey][id]
}

// registryEntryID returns the canonical 776 catalog handle. Wire encoders use
// the selected profile's accessor or projectRegistryID before emitting it.
func registryEntryID(registryKey, entryName string) int32 {
	data, err := javaConfigFor(776)
	if err != nil {
		return -1
	}
	if id, ok := data.entries[registryKey][entryName]; ok {
		return id
	}
	return -1
}

// sendRegistries writes one Registry Data packet per captured registry
// in vanilla's order.
func (s *Server) sendRegistries(sess *Session) error {
	loadConfigData()
	profile, err := sess.javaProfile()
	if err != nil {
		return err
	}
	for _, reg := range profile.config.registries {
		body, err := s.gameTestConfigBody(reg)
		if err != nil {
			return err
		}
		if err := sess.write(sess.ids().ConfigRegistryData, body); err != nil {
			return err
		}
	}
	return nil
}

// sendUpdateTags writes the captured Update Tags body.
func (s *Server) sendUpdateTags(sess *Session) error {
	profile, err := sess.javaProfile()
	if err != nil {
		return err
	}
	return sess.write(sess.ids().ConfigUpdateTags, profile.config.tags)
}
