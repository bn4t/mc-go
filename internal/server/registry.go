package server

import (
	"bytes"
	"embed"
	"fmt"
	"sort"
	"strings"
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
)

// loadConfigData parses the embedded captures once. A corrupt capture
// is a build-time mistake, so it panics rather than failing sessions.
func loadConfigData() {
	configOnce.Do(func() {
		entries, err := registryBodies.ReadDir("testdata/vanilla-26.2/config/registries")
		if err != nil {
			panic(fmt.Sprintf("mc-go: embedded registry captures missing: %v", err))
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".bin") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			body, err := registryBodies.ReadFile("testdata/vanilla-26.2/config/registries/" + name)
			if err != nil {
				panic(fmt.Sprintf("mc-go: registry capture %s: %v", name, err))
			}
			key, _, err := parseRegistryBody(body)
			if err != nil {
				panic(fmt.Sprintf("mc-go: registry capture %s: %v", name, err))
			}
			configRegs = append(configRegs, configRegistry{key: key, body: body})
		}
		for _, r := range configRegs {
			if r.key != "minecraft:worldgen/biome" {
				continue
			}
			_, ids, err := parseRegistryBody(r.body)
			if err != nil {
				panic(fmt.Sprintf("mc-go: biome capture: %v", err))
			}
			biomeIDs = ids
		}
		if biomeIDs == nil {
			panic("mc-go: worldgen/biome capture missing")
		}
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
	if count < 0 {
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
		ids[name] = i
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

// registryEntryID returns the captured index of an entry in a synced
// registry, or -1 when either is unknown.
func registryEntryID(registryKey, entryName string) int32 {
	loadConfigData()
	for _, reg := range configRegs {
		if reg.key != registryKey {
			continue
		}
		_, ids, err := parseRegistryBody(reg.body)
		if err != nil {
			return -1
		}
		if id, ok := ids[entryName]; ok {
			return id
		}
		return -1
	}
	return -1
}

// sendRegistries writes one Registry Data packet per captured registry
// in vanilla's order.
func (s *Server) sendRegistries(sess *Session) error {
	loadConfigData()
	for _, reg := range configRegs {
		if err := sess.write(sess.ids().ConfigRegistryData, reg.body); err != nil {
			return err
		}
	}
	return nil
}

// sendUpdateTags writes the captured Update Tags body.
func (s *Server) sendUpdateTags(sess *Session) error {
	return sess.write(sess.ids().ConfigUpdateTags, tagsBody)
}
