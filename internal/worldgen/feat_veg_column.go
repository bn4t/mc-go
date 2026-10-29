package worldgen

import "encoding/json"

// block_column (BlockColumnFeature): stacked layers of sampled heights
// along a direction, shortened to the run of allowed cells ahead of the
// origin.

func init() {
	featureBuilders["block_column"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		type layer struct {
			height intProvider
			state  stateProvider
		}
		var raw []obj
		jsonUnmarshal(cfg["layers"], &raw)
		layers := make([]layer, len(raw))
		for i, l := range raw {
			layers[i] = layer{parseIntProvider(l["height"]), g.compileStateProvider(l["provider"])}
		}
		dir := dirNames[cfg.str("direction")]
		allowed := g.compilePredicate(cfg["allowed_placement"])
		var tip bool
		json.Unmarshal(cfg["prioritize_tip"], &tip)
		d := dirOffsets[dir]
		return func(c *featCtx, r Random, p BlockPos) bool {
			// Heights are drawn for every layer first, in layer order.
			sizes := make([]int32, len(layers))
			total := int32(0)
			for i, l := range layers {
				sizes[i] = l.height(r)
				total += sizes[i]
			}
			if total == 0 {
				return false
			}
			// The allowed run is counted from the cell one step past the
			// origin; the first refused cell trims the column.
			q := offset(p, d[0], d[1], d[2])
			for n := int32(0); n < total; n++ {
				if !allowed(c.rg, q) {
					trimColumn(sizes, total-n, tip)
					break
				}
				q = offset(q, d[0], d[1], d[2])
			}
			q = p
			for i, l := range layers {
				for k := int32(0); k < sizes[i]; k++ {
					c.rg.SetBlock(q[0], q[1], q[2], l.state.getState(c.rg, r, q))
					q = offset(q, d[0], d[1], d[2])
				}
			}
			return true
		}
	}
}

// trimColumn is BlockColumnFeature.truncate: removes excess cells from
// the base layers first when the tip is prioritized, else from the tip.
func trimColumn(sizes []int32, excess int32, tip bool) {
	i, step, end := len(sizes)-1, -1, -1
	if tip {
		i, step, end = 0, 1, len(sizes)
	}
	for ; i != end && excess > 0; i += step {
		take := min(sizes[i], excess)
		excess -= take
		sizes[i] -= take
	}
}
