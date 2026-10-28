package worldgen

// Lakes: a blob of overlapping ellipsoids in a 16×8×16 box, cave air
// above its waterline and fluid below, walled by a barrier block.

func init() {
	featureKinds["minecraft:lake"] = decodeLake
}

var caveAirState = InternState("minecraft:cave_air")

func decodeLake(cfg any) func(*featureCtx) bool {
	fluid := decodeStateProvider(ffield(cfg, "fluid"))
	barrier := decodeStateProvider(ffield(cfg, "barrier"))
	canPlace := decodeBlockPredicate(ffield(cfg, "can_place_feature"))
	canReplace := decodeBlockPredicate(ffield(cfg, "can_replace_with_air_or_fluid"))
	canBarrier := decodeBlockPredicate(ffield(cfg, "can_replace_with_barrier"))
	return func(fc *featureCtx) bool {
		r, rand := fc.r, fc.rand
		if fc.origin.Y <= r.minY+4 {
			return false
		}
		o := Point{fc.origin.X - 8, fc.origin.Y - 4, fc.origin.Z - 8}
		var grid [2048]bool
		at := func(x, y, z int32) bool { return grid[(x*16+z)*8+y] }
		blobs := rand.NextIntn(4) + 4
		for i := int32(0); i < blobs; i++ {
			// Products are rounded before the sums, as the game computes.
			sx := float64(rand.NextDouble()*6) + 3
			sy := float64(rand.NextDouble()*4) + 2
			sz := float64(rand.NextDouble()*6) + 3
			cx := float64(rand.NextDouble()*(16-sx-2)) + 1 + sx/2
			cy := float64(rand.NextDouble()*(8-sy-4)) + 2 + sy/2
			cz := float64(rand.NextDouble()*(16-sz-2)) + 1 + sz/2
			for x := int32(1); x < 15; x++ {
				for z := int32(1); z < 15; z++ {
					for y := int32(1); y < 7; y++ {
						dx := (float64(x) - cx) / (sx / 2)
						dy := (float64(y) - cy) / (sy / 2)
						dz := (float64(z) - cz) / (sz / 2)
						if float64(dx*dx)+float64(dy*dy)+float64(dz*dz) < 1 {
							grid[(x*16+z)*8+y] = true
						}
					}
				}
			}
		}
		// A cell borders the lake when it is outside it and touches it.
		edge := func(x, y, z int32) bool {
			return !at(x, y, z) && (x < 15 && at(x+1, y, z) || x > 0 && at(x-1, y, z) ||
				z < 15 && at(x, y, z+1) || z > 0 && at(x, y, z-1) ||
				y < 7 && at(x, y+1, z) || y > 0 && at(x, y-1, z))
		}
		f := fluid.get(r, rand, o)
		for x := int32(0); x < 16; x++ {
			for z := int32(0); z < 16; z++ {
				for y := int32(0); y < 8; y++ {
					if !edge(x, y, z) {
						continue
					}
					p := Point{o.X + x, o.Y + y, o.Z + z}
					s := r.get(p)
					if y >= 4 && s.isLiquidBlock() || y < 4 && !s.isSolid() && s != f || !canPlace(r, p) {
						return false
					}
				}
			}
		}
		for x := int32(0); x < 16; x++ {
			for z := int32(0); z < 16; z++ {
				for y := int32(0); y < 8; y++ {
					p := Point{o.X + x, o.Y + y, o.Z + z}
					if !at(x, y, z) || !canReplace(r, p) {
						continue
					}
					if y >= 4 {
						r.set(p, caveAirState)
					} else {
						r.set(p, f)
					}
				}
			}
		}
		if b := barrier.get(r, rand, o); !b.isAir() {
			for x := int32(0); x < 16; x++ {
				for z := int32(0); z < 16; z++ {
					for y := int32(0); y < 8; y++ {
						if !edge(x, y, z) || y >= 4 && rand.NextIntn(2) == 0 {
							continue
						}
						p := Point{o.X + x, o.Y + y, o.Z + z}
						if r.get(p).isSolid() && canBarrier(r, p) {
							r.set(p, b)
						}
					}
				}
			}
		}
		if f.fluidInTag("water") {
			ice := defaultState("ice")
			for x := int32(0); x < 16; x++ {
				for z := int32(0); z < 16; z++ {
					p := Point{o.X + x, o.Y + 4, o.Z + z}
					if r.shouldFreeze(r.biome(p), p) && canReplace(r, p) {
						r.set(p, ice)
					}
				}
			}
		}
		return true
	}
}
