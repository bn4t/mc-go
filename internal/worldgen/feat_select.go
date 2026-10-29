package worldgen

import "encoding/json"

// placedList parses a HolderSet<PlacedFeature>: a list of references or
// inline placed features, or a single one.
func (g *Generator) placedList(raw json.RawMessage) []*placedFeature {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return []*placedFeature{g.placedRef(raw)}
	}
	out := make([]*placedFeature, len(list))
	for i, l := range list {
		out[i] = g.placedRef(l)
	}
	return out
}

// placeNested is PlacedFeature.place: a sub-feature placed with no top
// feature (no biome check).
func (pf *placedFeature) placeNested(c *featCtx, r Random, p BlockPos) bool {
	return pf.place(&placeCtx{g: c.g, rg: c.rg}, r, p)
}

func init() {
	featureBuilders["random_selector"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		var entries []obj
		jsonUnmarshal(cfg["features"], &entries)
		type wf struct {
			chance float32
			pf     *placedFeature
		}
		var fs []wf
		for _, e := range entries {
			fs = append(fs, wf{parseF32(e["chance"]), g.placedRef(e["feature"])})
		}
		def := g.placedRef(cfg["default"])
		return func(c *featCtx, r Random, p BlockPos) bool {
			for _, f := range fs {
				if r.NextFloat() < f.chance {
					return f.pf.placeNested(c, r, p)
				}
			}
			return def.placeNested(c, r, p)
		}
	}
	featureBuilders["simple_random_selector"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		fs := g.placedList(cfg["features"])
		return func(c *featCtx, r Random, p BlockPos) bool {
			return fs[r.NextIntN(int32(len(fs)))].placeNested(c, r, p)
		}
	}
	featureBuilders["random_boolean_selector"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		t := g.placedRef(cfg["feature_true"])
		f := g.placedRef(cfg["feature_false"])
		return func(c *featCtx, r Random, p BlockPos) bool {
			if r.NextBool() {
				return t.placeNested(c, r, p)
			}
			return f.placeNested(c, r, p)
		}
	}
	featureBuilders["sequence"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		fs := g.placedList(cfg["features"])
		return func(c *featCtx, r Random, p BlockPos) bool {
			for _, f := range fs {
				if !f.placeNested(c, r, p) {
					return false
				}
			}
			return true
		}
	}
	featureBuilders["weighted_random_selector"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		w := parseWeighted(cfg["features"], func(raw json.RawMessage) *placedFeature { return g.placedRef(raw) })
		return func(c *featCtx, r Random, p BlockPos) bool {
			if w.total == 0 {
				return false
			}
			return w.pick(r).placeNested(c, r, p)
		}
	}
}
