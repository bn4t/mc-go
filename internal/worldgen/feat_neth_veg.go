package worldgen

import (
	"mc-go/internal/world"
)

// Nether vegetation: huge fungi, nether forest ground cover, weeping
// and twisting vines.

func init() {
	featureBuilders["huge_fungus"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		st := func(k string) world.Block {
			var o obj
			jsonUnmarshal(cfg[k], &o)
			return stateFromJSON(o)
		}
		f := &nethFungus{
			base:  st("valid_base_block"),
			stem:  st("stem_state"),
			hat:   st("hat_state"),
			decor: st("decor_state"),
			repl:  g.compilePredicate(cfg["replaceable_blocks"]),
			depth: g.RS.settings.height,
		}
		if v, ok := cfg["planted"]; ok {
			jsonUnmarshal(v, &f.planted)
		}
		return func(c *featCtx, r Random, p BlockPos) bool { return f.place(c.rg, r, p) }
	}
	featureBuilders["nether_forest_vegetation"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		prov := g.compileStateProvider(cfg["state_provider"])
		w, h := cfg.i32("spread_width"), cfg.i32("spread_height")
		return func(c *featCtx, r Random, p BlockPos) bool { return nethForestVegetation(c.rg, r, prov, w, h, p) }
	}
	featureBuilders["weeping_vines"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return func(c *featCtx, r Random, p BlockPos) bool { return nethWeepingVines(c.rg, r, p) }
	}
	featureBuilders["twisting_vines"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		w, h, m := cfg.i32("spread_width"), cfg.i32("spread_height"), cfg.i32("max_height")
		return func(c *featCtx, r Random, p BlockPos) bool { return nethTwistingVines(c.rg, r, w, h, m, p) }
	}
	// NetherFungusBlock and NetherRootsBlock.mayPlaceOn test the block
	// below against the support tag each block registers
	// (BlockTags.SUPPORTS_<NAME>: supports_crimson_fungus, ...).
	nethSupportTag := func(rg *Region, st world.Block, p BlockPos) bool {
		name, _ := world.StateOwner(st)
		return blockInTag(rg.at(offset(p, 0, -1, 0)), "supports_"+name)
	}
	surviveByClass["NetherFungusBlock"] = nethSupportTag
	surviveByClass["NetherRootsBlock"] = nethSupportTag
	// FireBlock.canSurvive: a sturdy floor, or a flammable neighbour
	// (isValidFireLocation). No per-state flammability fact is measured;
	// the nether fire patches only ever test netherrack floors.
	surviveByClass["FireBlock"] = func(rg *Region, st world.Block, p BlockPos) bool {
		return sturdyFull(rg.at(offset(p, 0, -1, 0)), dirUp)
	}
	// SoulFireBlock.canSurvive: its base-block tag below.
	surviveByClass["SoulFireBlock"] = func(rg *Region, st world.Block, p BlockPos) bool {
		return blockInTag(rg.at(offset(p, 0, -1, 0)), "soul_fire_base_blocks")
	}
}

type nethFungus struct {
	base, stem, hat, decor world.Block
	repl                   blockPredicate
	planted                bool
	depth                  int32
}

// nethFungusReplaceable is HugeFungusFeature.isReplaceable: any
// canBeReplaced state, or (for the stem) one the config predicate
// accepts.
func (f *nethFungus) replaceable(rg *Region, p BlockPos, checkConfig bool) bool {
	if isReplaceable(rg.at(p)) {
		return true
	}
	return checkConfig && f.repl(rg, p)
}

// place is HugeFungusFeature.place: on the valid base, height
// Mth.nextInt(4, 13) doubled one time in 12; worldgen fungi must top
// out below the generator depth; 6% are huge (3x3 stems with random
// corners). The origin is cleared before stem and hat.
func (f *nethFungus) place(rg *Region, r Random, o BlockPos) bool {
	baseName, _ := world.StateOwner(f.base)
	if !isBlock(rg.at(offset(o, 0, -1, 0)), baseName) {
		return false
	}
	h := mthNextInt(r, 4, 13)
	if r.NextIntN(12) == 0 {
		h *= 2
	}
	if !f.planted && o[1]+h+1 >= f.depth {
		return false
	}
	huge := !f.planted && r.NextFloat() < 0.06
	rg.set(o, world.Air)
	f.stemAt(rg, r, o, h, huge)
	f.hatAt(rg, r, o, h, huge)
	return true
}

func (f *nethFungus) stemAt(rg *Region, r Random, o BlockPos, h int32, huge bool) {
	rad := int32(0)
	if huge {
		rad = 1
	}
	for x := -rad; x <= rad; x++ {
		for z := -rad; z <= rad; z++ {
			corner := huge && absI32(x) == rad && absI32(z) == rad
			for y := int32(0); y < h; y++ {
				q := offset(o, x, y, z)
				if !f.replaceable(rg, q, true) {
					continue
				}
				if f.planted {
					// Bone-meal growth (destroyBlock) is not a worldgen path.
					rg.set(q, f.stem)
				} else if corner {
					if r.NextFloat() < 0.1 {
						rg.set(q, f.stem)
					}
				} else {
					rg.set(q, f.stem)
				}
			}
		}
	}
}

// hatAt is placeHat: the hat spans the top min(nextInt(1+h/3)+5, h)
// layers with radius 2 (1 for the top layers past a nextInt(3) cut, 3
// for the lowest four of a tall hat, +1 when huge). The lowest three
// layers drop blocks on their rim only; above, interior, corner and
// edge cells roll decor/hat with chances (0.1/0.2, 0.01/0.7,
// 0.0005/0.98) and nether-wart hats add weeping vines.
func (f *nethFungus) hatAt(rg *Region, r Random, o BlockPos, h int32, huge bool) {
	wart := isBlock(f.hat, "nether_wart_block")
	k := min(r.NextIntN(1+h/3)+5, h)
	start := h - k
	for y := start; y <= h; y++ {
		rad := int32(1)
		if y < h-r.NextIntN(3) {
			rad = 2
		}
		if k > 8 && y < start+4 {
			rad = 3
		}
		if huge {
			rad++
		}
		for x := -rad; x <= rad; x++ {
			for z := -rad; z <= rad; z++ {
				edgeX := x == -rad || x == rad
				edgeZ := z == -rad || z == rad
				inner := !edgeX && !edgeZ && y != h
				corner := edgeX && edgeZ
				lower := y < start+3
				q := offset(o, x, y, z)
				if !f.replaceable(rg, q, false) {
					continue
				}
				switch {
				case lower:
					if !inner {
						f.dropBlock(rg, r, q, wart)
					}
				case inner:
					f.hatBlock(rg, r, q, 0.1, 0.2, nethIfWart(wart, 0.1))
				case corner:
					f.hatBlock(rg, r, q, 0.01, 0.7, nethIfWart(wart, 0.083))
				default:
					f.hatBlock(rg, r, q, 5.0e-4, 0.98, nethIfWart(wart, 0.07))
				}
			}
		}
	}
}

func nethIfWart(wart bool, v float32) float32 {
	if wart {
		return v
	}
	return 0
}

// hatBlock is placeHatBlock.
func (f *nethFungus) hatBlock(rg *Region, r Random, p BlockPos, decor, hat, vine float32) {
	if r.NextFloat() < decor {
		rg.set(p, f.decor)
	} else if r.NextFloat() < hat {
		rg.set(p, f.hat)
		if r.NextFloat() < vine {
			nethFungusVines(rg, r, p)
		}
	}
}

// dropBlock is placeHatDropBlock: hat continues straight under hat,
// otherwise a (double) 0.15 roll, wart hats then 1 in 11 vines.
func (f *nethFungus) dropBlock(rg *Region, r Random, p BlockPos, wart bool) {
	hatName, _ := world.StateOwner(f.hat)
	if isBlock(rg.at(offset(p, 0, -1, 0)), hatName) {
		rg.set(p, f.hat)
	} else if float64(r.NextFloat()) < 0.15 {
		rg.set(p, f.hat)
		if wart && r.NextIntN(11) == 0 {
			nethFungusVines(rg, r, p)
		}
	}
}

// nethFungusVines is tryPlaceWeepingVines: under an empty cell, a
// column of Mth.nextInt(1, 5) (doubled one time in 7), head age 23..25.
func nethFungusVines(rg *Region, r Random, p BlockPos) {
	m := offset(p, 0, -1, 0)
	if !isAir(rg.at(m)) {
		return
	}
	n := mthNextInt(r, 1, 5)
	if r.NextIntN(7) == 0 {
		n *= 2
	}
	nethVineColumn(rg, r, m, n, 23, 25, -1, "weeping_vines")
}

// nethVineColumn is Weeping/TwistingVinesFeature.placeWeepingVinesColumn:
// walking dy per step from the start, empty cells become the plant body
// until the last step or the next cell is occupied, which takes the
// head with a random age; occupied cells are skipped. Weeping columns
// run steps 0..n, twisting ones 1..n.
func nethVineColumn(rg *Region, r Random, m BlockPos, n, minAge, maxAge, dy int32, head string) {
	first := int32(0)
	if dy > 0 {
		first = 1
	}
	headState := blockNamed(head)
	body := blockNamed(head + "_plant")
	for i := first; i <= n; i++ {
		if isAir(rg.at(m)) {
			if i == n || !isAir(rg.at(offset(m, 0, dy, 0))) {
				age := mthNextInt(r, minAge, maxAge)
				st, _ := world.WithProperty(headState, "age", itoa32(age))
				rg.set(m, st)
				return
			}
			rg.set(m, body)
		}
		m[1] += dy
	}
}

func itoa32(v int32) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [12]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// nethForestVegetation is NetherForestVegetationFeature.place: on
// nylium, inside the build range, width² tries at offsets
// (nw-nw, nh-nh, nw-nw); the provider is sampled before the cell is
// tested, empty cells above the floor where the state survives take it.
func nethForestVegetation(rg *Region, r Random, prov stateProvider, w, h int32, o BlockPos) bool {
	if !blockInTag(rg.at(offset(o, 0, -1, 0)), "nylium") {
		return false
	}
	if o[1] < rg.MinY()+1 || o[1]+1 > rg.MaxY() {
		return false
	}
	n := 0
	for i := int32(0); i < w*w; i++ {
		dx := r.NextIntN(w) - r.NextIntN(w)
		dy := r.NextIntN(h) - r.NextIntN(h)
		dz := r.NextIntN(w) - r.NextIntN(w)
		q := offset(o, dx, dy, dz)
		st := prov.getState(rg, r, q)
		if isAir(rg.at(q)) && q[1] > rg.MinY() && canSurvive(rg, st, q) {
			rg.set(q, st)
			n++
		}
	}
	return n > 0
}

// nethWeepingVines is WeepingVinesFeature.place: an empty cell under
// netherrack or wart block gets a wart block, 200 tries grow the wart
// patch (n6-n6, n2-n5, n6-n6) on cells touching exactly one
// netherrack/wart face, then 100 tries (n8-n8, n2-n7, n8-n8) hang vines
// of Mth.nextInt(1, 8) (doubled one in 6, reset to 1 one in 5) from
// netherrack/wart ceilings.
func nethWeepingVines(rg *Region, r Random, o BlockPos) bool {
	if !isAir(rg.at(o)) {
		return false
	}
	roof := func(b world.Block) bool { return nethIs(b, "netherrack", "nether_wart_block") }
	if !roof(rg.at(offset(o, 0, 1, 0))) {
		return false
	}
	wart := blockNamed("nether_wart_block")
	rg.set(o, wart)
	for i := 0; i < 200; i++ {
		dx := r.NextIntN(6) - r.NextIntN(6)
		dy := r.NextIntN(2) - r.NextIntN(5)
		dz := r.NextIntN(6) - r.NextIntN(6)
		q := offset(o, dx, dy, dz)
		if !isAir(rg.at(q)) {
			continue
		}
		n := 0
		for _, d := range dirOffsets {
			if roof(rg.at(offset(q, d[0], d[1], d[2]))) {
				n++
			}
			if n > 1 {
				break
			}
		}
		if n == 1 {
			rg.set(q, wart)
		}
	}
	for i := 0; i < 100; i++ {
		dx := r.NextIntN(8) - r.NextIntN(8)
		dy := r.NextIntN(2) - r.NextIntN(7)
		dz := r.NextIntN(8) - r.NextIntN(8)
		q := offset(o, dx, dy, dz)
		if !isAir(rg.at(q)) || !roof(rg.at(offset(q, 0, 1, 0))) {
			continue
		}
		n := mthNextInt(r, 1, 8)
		if r.NextIntN(6) == 0 {
			n *= 2
		}
		if r.NextIntN(5) == 0 {
			n = 1
		}
		nethVineColumn(rg, r, q, n, 17, 25, -1, "weeping_vines")
	}
	return true
}

// nethTwistBad is TwistingVinesFeature.isInvalidPlacementLocation: the
// cell must be empty over netherrack, warped nylium or warped wart.
func nethTwistBad(rg *Region, p BlockPos) bool {
	if !isAir(rg.at(p)) {
		return true
	}
	return !nethIs(rg.at(offset(p, 0, -1, 0)), "netherrack", "warped_nylium", "warped_wart_block")
}

// nethTwistingVines is TwistingVinesFeature.place: width² tries at
// Mth.nextInt(-w..w, -h..h, -w..w) offsets drop to the first air cell
// above ground, and valid ones grow a column of Mth.nextInt(1, max)
// (doubled one in 6, reset to 1 one in 5) upwards.
func nethTwistingVines(rg *Region, r Random, w, h, maxH int32, o BlockPos) bool {
	if nethTwistBad(rg, o) {
		return false
	}
	for i := int32(0); i < w*w; i++ {
		dx := mthNextInt(r, -w, w)
		dy := mthNextInt(r, -h, h)
		dz := mthNextInt(r, -w, w)
		q := offset(o, dx, dy, dz)
		var ok bool
		if q, ok = nethFirstAirAboveGround(rg, q); !ok {
			continue
		}
		if nethTwistBad(rg, q) {
			continue
		}
		n := mthNextInt(r, 1, maxH)
		if r.NextIntN(6) == 0 {
			n *= 2
		}
		if r.NextIntN(5) == 0 {
			n = 1
		}
		nethVineColumn(rg, r, q, n, 17, 25, 1, "twisting_vines")
	}
	return true
}

// nethFirstAirAboveGround is findFirstAirBlockAboveGround: step down
// until a non-air cell (failing outside the build range), then back up
// one.
func nethFirstAirAboveGround(rg *Region, p BlockPos) (BlockPos, bool) {
	for {
		p[1]--
		if !rg.inBuildHeight(p[1]) {
			return p, false
		}
		if !isAir(rg.at(p)) {
			break
		}
	}
	p[1]++
	return p, true
}
