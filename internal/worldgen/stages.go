package worldgen

import (
	"encoding/json"
	"strings"
)

// PlacedFeatureType is the configured feature type a placed feature
// places ("ore", "tree", ...).
func PlacedFeatureType(data Data, placed string) string {
	raw, ok := data.Get("placed_feature/" + placed)
	if !ok {
		return ""
	}
	var o obj
	jsonUnmarshal(raw, &o)
	var name string
	if json.Unmarshal(o["feature"], &name) == nil {
		cr, ok := data.Get("configured_feature/" + trimNS(name))
		if !ok {
			return ""
		}
		var c obj
		jsonUnmarshal(cr, &c)
		return trimNS(c.str("type"))
	}
	var c obj
	jsonUnmarshal(o["feature"], &c)
	return trimNS(c.str("type"))
}

// StageOverrides rewrites every biome for a staged comparison: features
// filtered by keep (nil keeps all, a func returning false strips all),
// carvers kept or cleared, and optionally the overworld surface rule
// emptied. The same entries serve as mc-go's data overlay and as the
// oracle's data pack, so feature indices line up on both sides.
func StageOverrides(data Data, keep func(placed string) bool, carvers, surface bool) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, k := range VanillaKeys("biome/") {
		raw, _ := data.Get(k)
		var b map[string]json.RawMessage
		jsonUnmarshal(raw, &b)
		if keep != nil {
			var steps []json.RawMessage
			jsonUnmarshal(b["features"], &steps)
			fs := make([][]string, len(steps))
			for i, st := range steps {
				var names []string
				var one string
				if json.Unmarshal(st, &one) == nil {
					names = []string{one}
				} else {
					jsonUnmarshal(st, &names)
				}
				fs[i] = []string{}
				for _, n := range names {
					if keep(strings.TrimPrefix(n, "minecraft:")) {
						fs[i] = append(fs[i], n)
					}
				}
			}
			b["features"], _ = json.Marshal(fs)
		}
		if !carvers {
			b["carvers"] = json.RawMessage(`[]`)
		}
		enc, _ := json.Marshal(b)
		out[k] = enc
	}
	if !surface {
		raw, _ := data.Get("noise_settings/overworld")
		var ns map[string]json.RawMessage
		jsonUnmarshal(raw, &ns)
		ns["surface_rule"] = json.RawMessage(`{"type":"minecraft:sequence","sequence":[]}`)
		enc, _ := json.Marshal(ns)
		out["noise_settings/overworld"] = enc
	}
	return out
}

// GenerateArea runs terrain for every chunk within one of the requested
// chunks and decorates the requested chunks in the given order,
// returning the finished chunks keyed by position.
func (g *Generator) GenerateArea(order [][2]int32) map[[2]int32]*ProtoChunk {
	chunks := map[[2]int32]*ProtoChunk{}
	need := func(cx, cz int32) {
		k := [2]int32{cx, cz}
		if _, ok := chunks[k]; !ok {
			chunks[k] = g.Terrain(cx, cz)
		}
	}
	for _, c := range order {
		for dz := int32(-1); dz <= 1; dz++ {
			for dx := int32(-1); dx <= 1; dx++ {
				need(c[0]+dx, c[1]+dz)
			}
		}
	}
	for _, c := range order {
		g.Decorate(g.NewRegion(chunks[c], chunks))
	}
	return chunks
}
