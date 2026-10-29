package worldgen

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"sync"
)

// Data resolves worldgen resources by path ("noise/temperature",
// "density_function/overworld/offset", "biome_parameters/overworld").
type Data interface {
	Get(path string) (json.RawMessage, bool)
}

type blobData map[string]json.RawMessage

func (d blobData) Get(path string) (json.RawMessage, bool) {
	v, ok := d[path]
	return v, ok
}

var (
	vanillaOnce sync.Once
	vanillaData blobData
)

// Vanilla is the bundled 26.2 worldgen data (decoded once).
func Vanilla() Data {
	vanillaOnce.Do(func() {
		raw, err := base64.StdEncoding.DecodeString(worldgenBlob)
		if err != nil {
			panic(err)
		}
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			panic(err)
		}
		plain, err := io.ReadAll(zr)
		if err != nil {
			panic(err)
		}
		m := map[string]json.RawMessage{}
		if err := json.Unmarshal(plain, &m); err != nil {
			panic(err)
		}
		vanillaData = m
	})
	return vanillaData
}

// overlayData lets a datapack-style override shadow bundled entries.
type overlayData struct {
	top  map[string]json.RawMessage
	base Data
}

func (d overlayData) Get(path string) (json.RawMessage, bool) {
	if v, ok := d.top[path]; ok {
		return v, true
	}
	return d.base.Get(path)
}

// Overlay returns base with the given entries replacing or adding paths.
func Overlay(base Data, top map[string]json.RawMessage) Data {
	return overlayData{top: top, base: base}
}

// VanillaKeys lists the bundled entries under prefix (e.g. "biome/"),
// sorted.
func VanillaKeys(prefix string) []string {
	d := Vanilla().(blobData)
	var out []string
	for k := range d {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
