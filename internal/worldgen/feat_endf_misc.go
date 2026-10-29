package worldgen

import (
	"mc-go/internal/world"
)

// Overworld odds and ends of the group: ice spikes, block piles (village
// hay, melons, snow, ice, pumpkins), the bonus chest and the void
// world's start platform.

func init() {
	featureBuilders["spike"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		var st obj
		jsonUnmarshal(cfg["state"], &st)
		sp := &endfIceSpike{
			state:   stateFromJSON(st),
			on:      g.compilePredicate(cfg["can_place_on"]),
			replace: g.compilePredicate(cfg["can_replace"]),
		}
		return func(c *featCtx, r Random, p BlockPos) bool { return sp.place(c.rg, r, p) }
	}
	featureBuilders["block_pile"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		prov := g.compileStateProvider(cfg["state_provider"])
		return func(c *featCtx, r Random, p BlockPos) bool { return endfBlockPile(c.rg, r, prov, p) }
	}
	featureBuilders["bonus_chest"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return func(c *featCtx, r Random, p BlockPos) bool { return endfBonusChest(c.rg, r, p) }
	}
	featureBuilders["void_start_platform"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return func(c *featCtx, r Random, p BlockPos) bool { return endfVoidPlatform(c.rg, p) }
	}
}

type endfIceSpike struct {
	state       world.Block
	on, replace blockPredicate
}

// place is SpikeFeature.place: sink through air (down to minY+2) to a
// can_place_on floor, rise nextInt(4), height nextInt(4)+7 and width
// height/4+nextInt(2); wide spikes lift 10+nextInt(30) one time in 60.
// Each layer i has float radius (1-i/h)*w over a ceil(radius) square
// of cells within the radius (|d|-0.25 per axis), rim cells kept 3 in
// 4, mirrored below the base for i > 0 when the square exceeds 1;
// cells take the state when air or replaceable. A 3x3 (or 1x1) root
// then runs down from one below the base to y 50, jumping
// 1+nextInt(5) every 50 cells (corners every nextInt(5)).
func (s *endfIceSpike) place(rg *Region, r Random, o BlockPos) bool {
	p := o
	for isAir(rg.at(p)) && p[1] > rg.MinY()+2 {
		p[1]--
	}
	if !s.on(rg, p) {
		return false
	}
	p[1] += r.NextIntN(4)
	h := r.NextIntN(4) + 7
	w := h/4 + r.NextIntN(2)
	if w > 1 && r.NextIntN(60) == 0 {
		p[1] += 10 + r.NextIntN(30)
	}
	put := func(q BlockPos) {
		if isAir(rg.at(q)) || s.replace(rg, q) {
			rg.set(q, s.state)
		}
	}
	for i := int32(0); i < h; i++ {
		f := float32(1-float32(i)/float32(h)) * float32(w)
		rad := mthCeil(float64(f))
		ff := float32(f * f)
		for x := -rad; x <= rad; x++ {
			fx := float32(absI32(x)) - 0.25
			for z := -rad; z <= rad; z++ {
				fz := float32(absI32(z)) - 0.25
				if (x != 0 || z != 0) && float32(fx*fx)+float32(fz*fz) > ff {
					continue
				}
				if (x == -rad || x == rad || z == -rad || z == rad) && r.NextFloat() > 0.75 {
					continue
				}
				put(offset(p, x, i, z))
				if i != 0 && rad > 1 {
					put(offset(p, x, -i, z))
				}
			}
		}
	}
	m := min(max(w-1, 0), 1)
	for x := -m; x <= m; x++ {
		for z := -m; z <= m; z++ {
			q := offset(p, x, -1, z)
			k := int32(50)
			if absI32(x) == 1 && absI32(z) == 1 {
				k = r.NextIntN(5)
			}
			for q[1] > 50 {
				b := rg.at(q)
				if !isAir(b) && !s.replace(rg, q) && b != s.state {
					break
				}
				rg.set(q, s.state)
				q[1]--
				k--
				if k <= 0 {
					q[1] -= r.NextIntN(5) + 1
					k = r.NextIntN(5)
				}
			}
		}
	}
	return true
}

// endfBlockPile is BlockPileFeature.place: above minY+5, a box of
// half-widths 2+nextInt(2) over two layers (betweenClosed order) whose
// cells fill when x²+z² <= nextFloat*10 - nextFloat*6, else on a
// (double) 0.031 roll; a fill needs an empty cell on a sturdy top (dirt
// path on a coin flip).
func endfBlockPile(rg *Region, r Random, prov stateProvider, o BlockPos) bool {
	if o[1] < rg.MinY()+5 {
		return false
	}
	ax := 2 + r.NextIntN(2)
	az := 2 + r.NextIntN(2)
	try := func(q BlockPos) {
		if !isAir(rg.at(q)) {
			return
		}
		below := rg.at(offset(q, 0, -1, 0))
		var ok bool
		if isBlock(below, "dirt_path") {
			ok = r.NextBool()
		} else {
			ok = sturdyFull(below, dirUp)
		}
		if ok {
			rg.set(q, prov.getState(rg, r, q))
		}
	}
	for z := o[2] - az; z <= o[2]+az; z++ {
		for y := o[1]; y <= o[1]+1; y++ {
			for x := o[0] - ax; x <= o[0]+ax; x++ {
				q := BlockPos{x, y, z}
				dx, dz := o[0]-x, o[2]-z
				a := float32(r.NextFloat() * 10)
				b := float32(r.NextFloat() * 6)
				if float32(dx*dx+dz*dz) <= a-b {
					try(q)
				} else if float64(r.NextFloat()) < 0.031 {
					try(q)
				}
			}
		}
	}
	return true
}

// endfShuffledRange is Util.toShuffledList over an int range: the
// Fisher-Yates walk from the top swapping with nextInt(i).
func endfShuffledRange(lo, hi int32, r Random) []int32 {
	out := make([]int32, 0, hi-lo+1)
	for v := lo; v <= hi; v++ {
		out = append(out, v)
	}
	for i := int32(len(out)); i > 1; i-- {
		j := r.NextIntN(i)
		out[i-1], out[j] = out[j], out[i-1]
	}
	return out
}

// endfCollisionEmpty approximates getCollisionShape().isEmpty() for
// the cell just above the MOTION_BLOCKING_NO_LEAVES surface: no
// per-state collision fact is measured, so non-motion-blocking,
// non-leaf states without a full collision box count as empty.
func endfCollisionEmpty(b world.Block) bool {
	return isAir(b) || (!blocksMotionState(b) && !isLeaves(b) && !fullCollision(b))
}

// endfBonusChest is BonusChestFeature.place: shuffled x and z lists of
// the chunk; the first column whose surface cell is empty (or has no
// collision) gets the chest (its loot table seeds from one nextLong)
// and torches on the horizontal sides where a torch survives.
func endfBonusChest(rg *Region, r Random, o BlockPos) bool {
	cx, cz := o[0]>>4<<4, o[2]>>4<<4
	xs := endfShuffledRange(cx, cx+15, r)
	zs := endfShuffledRange(cz, cz+15, r)
	for _, x := range xs {
		for _, z := range zs {
			p := BlockPos{x, rg.Height(HMMotionBlockingNoLeaves, x, z), z}
			b := rg.at(p)
			if !isAir(b) && !endfCollisionEmpty(b) {
				continue
			}
			rg.set(p, blockNamed("chest"))
			setBlockEntityLootTable(rg, r, p, "minecraft:chests/spawn_bonus_chest")
			torch := blockNamed("torch")
			for _, d := range horizontalDirs {
				off := dirOffsets[d]
				q := offset(p, off[0], off[1], off[2])
				// TorchBlock.canSurvive: canSupportCenter on the floor.
				if sturdyCenter(rg.at(offset(q, 0, -1, 0)), dirUp) {
					rg.set(q, torch)
				}
			}
			return true
		}
	}
	return false
}

// endfVoidPlatform is VoidStartPlatformFeature.place: chunks within one
// of chunk (0, 0) lay the stone square of checkerboard radius 16 around
// (8, y+3, 8), cobblestone at its centre.
func endfVoidPlatform(rg *Region, o BlockPos) bool {
	ccx, ccz := o[0]>>4, o[2]>>4
	if max(absI32(ccx), absI32(ccz)) > 1 {
		return true
	}
	y := o[1] + 3
	stone, cobble := blockNamed("stone"), blockNamed("cobblestone")
	for z := ccz * 16; z <= ccz*16+15; z++ {
		for x := ccx * 16; x <= ccx*16+15; x++ {
			if max(absI32(8-x), absI32(8-z)) > 16 {
				continue
			}
			if x == 8 && z == 8 {
				rg.set(BlockPos{x, y, z}, cobble)
			} else {
				rg.set(BlockPos{x, y, z}, stone)
			}
		}
	}
	return true
}
