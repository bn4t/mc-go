package worldgen

import "mc-go/internal/world"

// bamboo (BambooFeature): a stalk of 5..16 cells on a bamboo-capable
// floor, optionally ringed by a podzol disc, topped with leaves when it
// grew at least three cells.

func init() {
	featureBuilders["bamboo"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		prob := parseF32(cfg["probability"])
		base := blockNamed("bamboo")
		set := func(b world.Block, kv ...string) world.Block {
			for i := 0; i < len(kv); i += 2 {
				b, _ = world.WithProperty(b, kv[i], kv[i+1])
			}
			return b
		}
		trunk := set(base, "age", "1", "leaves", "none", "stage", "0")
		finalLarge := set(trunk, "leaves", "large", "stage", "1")
		topLarge := set(trunk, "leaves", "large")
		topSmall := set(trunk, "leaves", "small")
		podzol := blockNamed("podzol")
		return func(c *featCtx, r Random, p BlockPos) bool {
			rg := c.rg
			if !isAir(rg.Block(p[0], p[1], p[2])) {
				return false
			}
			// An air origin reports placed even when bamboo cannot stand.
			if !canSurvive(rg, base, p) {
				return true
			}
			height := r.NextIntN(12) + 5
			if r.NextFloat() < prob {
				rad := r.NextIntN(4) + 1
				for x := p[0] - rad; x <= p[0]+rad; x++ {
					for z := p[2] - rad; z <= p[2]+rad; z++ {
						dx, dz := x-p[0], z-p[2]
						if dx*dx+dz*dz > rad*rad {
							continue
						}
						y := rg.Height(HMWorldSurface, x, z) - 1
						if blockInTag(rg.Block(x, y, z), "beneath_bamboo_podzol_replaceable") {
							rg.SetBlock(x, y, z, podzol)
						}
					}
				}
			}
			q := p
			for i := int32(0); i < height && isAir(rg.Block(q[0], q[1], q[2])); i++ {
				rg.SetBlock(q[0], q[1], q[2], trunk)
				q[1]++
			}
			if q[1]-p[1] >= 3 {
				rg.SetBlock(q[0], q[1], q[2], finalLarge)
				rg.SetBlock(q[0], q[1]-1, q[2], topLarge)
				rg.SetBlock(q[0], q[1]-2, q[2], topSmall)
			}
			return true
		}
	}
}
