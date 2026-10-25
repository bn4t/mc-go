package server

import (
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"mc-go/internal/protocol"
)

// These are observed packet bodies from the official 26.1 server. The adjacent
// capture_manifest records each body hash, the oracle checksum and tool inputs.
//
//go:embed testdata/vanilla-26.1/config/registries/*.bin testdata/vanilla-26.1/config/*.bin
var java775Configuration embed.FS

var ErrJavaRegistryProjection = errors.New("Java registry entry has no target representation")

// javaConfigData is immutable after construction. Entry order belongs to the
// wire profile; canonical gameplay registry handles retain the 776 catalog.
type javaConfigData struct {
	protocol     int32
	registries   []configRegistry
	entries      map[string]entryIDs
	names        map[string][]string
	toWire       map[string][]int32
	fromWire     map[string][]int32
	tags         []byte
	knownPacks   []packEntry
	featureFlags []string
}

var javaConfigurations struct {
	currentOnce  sync.Once
	current      *javaConfigData
	previousOnce sync.Once
	previous     *javaConfigData
}

func javaConfigFor(number int32) (*javaConfigData, error) {
	switch number {
	case 776:
		javaConfigurations.currentOnce.Do(func() {
			var err error
			javaConfigurations.current, err = readJavaConfig(registryBodies, "testdata/vanilla-26.2/config", 776, tagsBody)
			if err != nil {
				panic(err)
			}
			javaConfigurations.current.knownPacks = append([]packEntry(nil), knownPacksSelection...)
			javaConfigurations.current.featureFlags = []string{"minecraft:vanilla"}
			javaConfigurations.current.bindCanonical(javaConfigurations.current)
		})
		return javaConfigurations.current, nil
	case 775:
		javaConfigurations.previousOnce.Do(func() {
			base := "testdata/vanilla-26.1/config"
			tags, err := java775Configuration.ReadFile(base + "/tags.bin")
			if err != nil {
				panic(err)
			}
			data, err := readJavaConfig(java775Configuration, base, 775, tags)
			if err != nil {
				panic(err)
			}
			packs, err := java775Configuration.ReadFile(base + "/known_packs.bin")
			if err != nil {
				panic(err)
			}
			data.knownPacks, err = parseKnownPacks(packs)
			if err != nil {
				panic(err)
			}
			features, err := java775Configuration.ReadFile(base + "/feature_flags.bin")
			if err != nil {
				panic(err)
			}
			data.featureFlags, err = parseJavaFeatureFlags(features)
			if err != nil {
				panic(err)
			}
			canonical, _ := javaConfigFor(776)
			data.bindCanonical(canonical)
			javaConfigurations.previous = data
		})
		return javaConfigurations.previous, nil
	default:
		return nil, fmt.Errorf("no captured Java configuration for protocol %d", number)
	}
}

func readJavaConfig(fs embed.FS, base string, number int32, tags []byte) (*javaConfigData, error) {
	directory, err := fs.ReadDir(base + "/registries")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(directory))
	for _, file := range directory {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".bin") {
			names = append(names, file.Name())
		}
	}
	sort.Strings(names)
	data := &javaConfigData{protocol: number, entries: map[string]entryIDs{}, names: map[string][]string{}, tags: tags}
	for _, name := range names {
		body, err := fs.ReadFile(base + "/registries/" + name)
		if err != nil {
			return nil, err
		}
		key, ids, err := parseRegistryBody(body)
		if err != nil {
			return nil, fmt.Errorf("protocol%d %s: %w", number, name, err)
		}
		if _, duplicate := data.entries[key]; duplicate {
			return nil, fmt.Errorf("duplicate captured registry %s", key)
		}
		data.registries = append(data.registries, configRegistry{key: key, body: body})
		data.entries[key] = ids
		entries := make([]string, len(ids))
		for entry, id := range ids {
			entries[id] = entry
		}
		data.names[key] = entries
	}
	for _, key := range []string{"minecraft:worldgen/biome", "minecraft:dimension_type", "minecraft:chat_type", "minecraft:dialog"} {
		if len(data.entries[key]) == 0 {
			return nil, fmt.Errorf("protocol%d missing required registry %s", number, key)
		}
	}
	return data, nil
}

func (data *javaConfigData) bindCanonical(canonical *javaConfigData) {
	data.toWire = map[string][]int32{}
	data.fromWire = map[string][]int32{}
	for key, names := range canonical.names {
		values := make([]int32, len(names))
		for id, name := range names {
			values[id] = -1
			if wire, ok := data.entries[key][name]; ok {
				values[id] = wire
			}
		}
		data.toWire[key] = values
	}
	for key, names := range data.names {
		values := make([]int32, len(names))
		for id, name := range names {
			values[id] = -1
			if runtime, ok := canonical.entries[key][name]; ok {
				values[id] = runtime
			}
		}
		data.fromWire[key] = values
	}
}

func parseJavaFeatureFlags(body []byte) ([]string, error) {
	r := protocol.NewPacketReader(body)
	count, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if count < 0 || count > 1024 {
		return nil, errors.New("invalid feature flag count")
	}
	flags := make([]string, count)
	for i := range flags {
		flags[i], err = r.String()
		if err != nil {
			return nil, err
		}
	}
	if r.Remaining() != 0 {
		return nil, errors.New("trailing feature flag bytes")
	}
	return flags, nil
}

func (p *javaProfile) registryEntryID(key, name string) int32 {
	if p.config == nil {
		return -1
	}
	if id, ok := p.config.entries[key][name]; ok {
		return id
	}
	return -1
}
func (p *javaProfile) registryEntryName(key string, id int32) string {
	if p.config == nil || id < 0 || int64(id) >= int64(len(p.config.names[key])) {
		return ""
	}
	return p.config.names[key][id]
}
func (p *javaProfile) projectRegistryID(key string, runtime int32) (int32, error) {
	if p.config != nil && runtime >= 0 && int64(runtime) < int64(len(p.config.toWire[key])) {
		if wire := p.config.toWire[key][runtime]; wire >= 0 {
			return wire, nil
		}
	}
	return -1, fmt.Errorf("%w: protocol%d registry%s canonical%d", ErrJavaRegistryProjection, p.version.Protocol, key, runtime)
}
func (p *javaProfile) canonicalRegistryID(key string, wire int32) (int32, error) {
	if p.config != nil && wire >= 0 && int64(wire) < int64(len(p.config.fromWire[key])) {
		if runtime := p.config.fromWire[key][wire]; runtime >= 0 {
			return runtime, nil
		}
	}
	return -1, fmt.Errorf("%w: protocol%d registry%s wire%d", ErrJavaRegistryProjection, p.version.Protocol, key, wire)
}
