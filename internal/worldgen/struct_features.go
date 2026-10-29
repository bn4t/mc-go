package worldgen

// Features that only structure pool elements place (village decor).

func init() {
	featureBuilders["block_pile"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		sp := g.compileStateProvider(cfg["state_provider"])
		return func(c *featCtx, r Random, p BlockPos) bool { return placeBlockPile(c.rg, r, sp, p) }
	}
}

// placeBlockPile is BlockPileFeature.place: a 2-3 radius square, one
// layer above the origin included, cells kept by a random falloff of
// the horizontal distance (or a 3.1% chance), each placed on air over a
// sturdy top (dirt path: a coin flip).
func placeBlockPile(rg *Region, r Random, sp stateProvider, o BlockPos) bool {
	if o[1] < rg.MinY()+5 {
		return false
	}
	rx := 2 + r.NextIntN(2)
	rz := 2 + r.NextIntN(2)
	try := func(p BlockPos) {
		if !isAir(rg.at(p)) {
			return
		}
		below := rg.at(offset(p, 0, -1, 0))
		ok := false
		if isBlock(below, "dirt_path") {
			ok = r.NextBool()
		} else {
			ok = sturdyFull(below, dirUp)
		}
		if ok {
			rg.set(p, sp.getState(rg, r, p))
		}
	}
	for z := o[2] - rz; z <= o[2]+rz; z++ {
		for y := o[1]; y <= o[1]+1; y++ {
			for x := o[0] - rx; x <= o[0]+rx; x++ {
				dx, dz := o[0]-x, o[2]-z
				p := BlockPos{x, y, z}
				if float32(dx*dx+dz*dz) <= float32(r.NextFloat()*10)-float32(r.NextFloat()*6) {
					try(p)
				} else if float64(r.NextFloat()) < 0.031 {
					try(p)
				}
			}
		}
	}
	return true
}
