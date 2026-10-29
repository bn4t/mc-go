package worldgen

import (
	"mc-go/internal/world"
)

// Ocean floor vegetation: kelp (KelpFeature), seagrass
// (SeagrassFeature), sea_pickle (SeaPickleFeature) and the three coral
// shapes (CoralFeature and its CoralTree/CoralClaw/CoralMushroom
// subclasses).

func init() {
	featureBuilders["kelp"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		kelp, plant := blockNamed("kelp"), blockNamed("kelp_plant")
		return func(c *featCtx, r Random, p BlockPos) bool {
			rg := c.rg
			q := BlockPos{p[0], rg.Height(HMOceanFloor, p[0], p[2]), p[2]}
			if !isBlock(rg.Block(q[0], q[1], q[2]), "water") {
				return false
			}
			placed := 0
			n := 1 + r.NextIntN(10)
			for i := int32(0); i <= n; i++ {
				if isBlock(rg.Block(q[0], q[1], q[2]), "water") && isBlock(rg.Block(q[0], q[1]+1, q[2]), "water") &&
					canSurvive(rg, plant, q) {
					if i == n {
						rg.SetBlock(q[0], q[1], q[2], kelpAged(kelp, r))
						placed++
					} else {
						rg.SetBlock(q[0], q[1], q[2], plant)
					}
				} else if i > 0 {
					// The column stops: cap it with a head below unless
					// that would sit on another head.
					b := offset(q, 0, -1, 0)
					if canSurvive(rg, kelp, b) && !isBlock(rg.Block(b[0], b[1]-1, b[2]), "kelp") {
						rg.SetBlock(b[0], b[1], b[2], kelpAged(kelp, r))
						placed++
					}
					break
				}
				q[1]++
			}
			return placed > 0
		}
	}
	featureBuilders["seagrass"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		prob := parseF32(cfg["probability"])
		tall, short := blockNamed("tall_seagrass"), blockNamed("seagrass")
		upper, _ := world.WithProperty(tall, "half", "upper")
		return func(c *featCtx, r Random, p BlockPos) bool {
			rg := c.rg
			dx := r.NextIntN(8) - r.NextIntN(8)
			dz := r.NextIntN(8) - r.NextIntN(8)
			x, z := p[0]+dx, p[2]+dz
			q := BlockPos{x, rg.Height(HMOceanFloor, x, z), z}
			if !isBlock(rg.Block(q[0], q[1], q[2]), "water") {
				return false
			}
			isTall := r.NextDouble() < float64(prob)
			st := short
			if isTall {
				st = tall
			}
			if !canSurvive(rg, st, q) {
				return false
			}
			if isTall {
				if isBlock(rg.Block(q[0], q[1]+1, q[2]), "water") {
					rg.SetBlock(q[0], q[1], q[2], st)
					rg.SetBlock(q[0], q[1]+1, q[2], upper)
				}
			} else {
				rg.SetBlock(q[0], q[1], q[2], st)
			}
			return true
		}
	}
	featureBuilders["sea_pickle"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		count := parseIntProvider(cfg["count"])
		pickle := blockNamed("sea_pickle")
		return func(c *featCtx, r Random, p BlockPos) bool {
			rg := c.rg
			placed := 0
			n := count(r)
			for i := int32(0); i < n; i++ {
				dx := r.NextIntN(8) - r.NextIntN(8)
				dz := r.NextIntN(8) - r.NextIntN(8)
				x, z := p[0]+dx, p[2]+dz
				q := BlockPos{x, rg.Height(HMOceanFloor, x, z), z}
				st := picklesOf(pickle, r)
				if isBlock(rg.Block(q[0], q[1], q[2]), "water") && canSurvive(rg, st, q) {
					rg.SetBlock(q[0], q[1], q[2], st)
					placed++
				}
			}
			return placed > 0
		}
	}
	coral := func(shape func(rg *Region, r Random, p BlockPos, st world.Block) bool) func(*Generator, obj) func(*featCtx, Random, BlockPos) bool {
		return func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
			return func(c *featCtx, r Random, p BlockPos) bool {
				// CoralFeature.place: a random #coral_blocks block.
				name, ok := randomTagElement("coral_blocks", r)
				if !ok {
					return false
				}
				return shape(c.rg, r, p, blockNamed(name))
			}
		}
	}
	featureBuilders["coral_tree"] = coral(coralTree)
	featureBuilders["coral_claw"] = coral(coralClaw)
	featureBuilders["coral_mushroom"] = coral(coralMushroom)
}

func kelpAged(kelp world.Block, r Random) world.Block {
	b, _ := world.WithProperty(kelp, "age", itoa(int(20+r.NextIntN(4))))
	return b
}

func picklesOf(pickle world.Block, r Random) world.Block {
	b, _ := world.WithProperty(pickle, "pickles", itoa(int(r.NextIntN(4)+1)))
	return b
}

// placeCoralBlock is CoralFeature.placeCoralBlock: the block goes into
// water (or over a coral) with water above; then a coral or a sea
// pickle may top it and wall fans may hang off its sides.
func placeCoralBlock(rg *Region, r Random, p BlockPos, st world.Block) bool {
	cur := rg.Block(p[0], p[1], p[2])
	if !(isBlock(cur, "water") || blockInTag(cur, "corals")) || !isBlock(rg.Block(p[0], p[1]+1, p[2]), "water") {
		return false
	}
	rg.SetBlock(p[0], p[1], p[2], st)
	if r.NextFloat() < 0.25 {
		if name, ok := randomTagElement("corals", r); ok {
			rg.SetBlock(p[0], p[1]+1, p[2], blockNamed(name))
		}
	} else if r.NextFloat() < 0.05 {
		rg.SetBlock(p[0], p[1]+1, p[2], picklesOf(blockNamed("sea_pickle"), r))
	}
	for _, d := range horizontalDirs {
		if r.NextFloat() >= 0.2 {
			continue
		}
		o := dirOffsets[d]
		q := offset(p, o[0], o[1], o[2])
		if !isBlock(rg.Block(q[0], q[1], q[2]), "water") {
			continue
		}
		if name, ok := randomTagElement("wall_corals", r); ok {
			b := blockNamed(name)
			if world.HasProperty(b, "facing") {
				b = carpetWith(b, "facing", multifaceFaceProps[d])
			}
			rg.SetBlock(q[0], q[1], q[2], b)
		}
	}
	return true
}

// coralTree is CoralTreeFeature.placeFeature: a 1-3 block trunk, then
// two to four of the horizontal directions (shuffled) grow branches
// that climb and step outward.
func coralTree(rg *Region, r Random, p BlockPos, st world.Block) bool {
	q := p
	h := r.NextIntN(3) + 1
	for i := int32(0); i < h; i++ {
		if !placeCoralBlock(rg, r, q, st) {
			return true
		}
		q[1]++
	}
	top := q
	k := r.NextIntN(3) + 2
	dirs := shuffledDirs(horizontalDirs[:], r)
	for _, d := range dirs[:k] {
		o := dirOffsets[d]
		q = offset(top, o[0], o[1], o[2])
		l := r.NextIntN(5) + 2
		n := 0
		for i := int32(0); i < l; i++ {
			if !placeCoralBlock(rg, r, q, st) {
				break
			}
			n++
			q[1]++
			if i == 0 || (n >= 2 && r.NextFloat() < 0.25) {
				q = offset(q, o[0], o[1], o[2])
				n = 0
			}
		}
	}
	return true
}

// coralClaw is CoralClawFeature.placeFeature: a base block, then two or
// three of {dir, clockwise, counter-clockwise} (shuffled) grow a short
// arm and a finger reaching along dir.
func coralClaw(rg *Region, r Random, p BlockPos, st world.Block) bool {
	if !placeCoralBlock(rg, r, p, st) {
		return false
	}
	dir := horizontalDirs[r.NextIntN(4)]
	i := r.NextIntN(2) + 2
	arms := shuffledDirs([]int{dir, clockWise(dir), counterClockWise(dir)}, r)
	do := dirOffsets[dir]
	for _, d := range arms[:i] {
		o := dirOffsets[d]
		q := offset(p, o[0], o[1], o[2])
		j := r.NextIntN(2) + 1
		var step int
		var k int32
		if d == dir {
			step = dir
			k = r.NextIntN(3) + 2
		} else {
			q[1]++
			step = [2]int{d, dirUp}[r.NextIntN(2)]
			k = r.NextIntN(3) + 3
		}
		so := dirOffsets[step]
		for l := int32(0); l < j; l++ {
			if !placeCoralBlock(rg, r, q, st) {
				break
			}
			q = offset(q, so[0], so[1], so[2])
		}
		q = offset(q, -so[0], -so[1]+1, -so[2])
		for l := int32(0); l < k; l++ {
			q = offset(q, do[0], do[1], do[2])
			if !placeCoralBlock(rg, r, q, st) {
				break
			}
			if r.NextFloat() < 0.25 {
				q[1]++
			}
		}
	}
	return true
}

// coralMushroom is CoralMushroomFeature.placeFeature: the shell of a
// box sunk 1-3 blocks, edges and corners left out, each face cell
// skipped one time in ten.
func coralMushroom(rg *Region, r Random, p BlockPos, st world.Block) bool {
	ny := r.NextIntN(3) + 3
	nx := r.NextIntN(3) + 3
	nz := r.NextIntN(3) + 3
	sink := r.NextIntN(3) + 1
	for x := int32(0); x <= nx; x++ {
		ex := x == 0 || x == nx
		for y := int32(0); y <= ny; y++ {
			ey := y == 0 || y == ny
			for z := int32(0); z <= nz; z++ {
				ez := z == 0 || z == nz
				if (ex && ey) || (ez && ey) || (ex && ez) || (!ex && !ey && !ez) {
					continue
				}
				if r.NextFloat() < 0.1 {
					continue
				}
				placeCoralBlock(rg, r, BlockPos{p[0] + x, p[1] + y - sink, p[2] + z}, st)
			}
		}
	}
	return true
}

// shuffledDirs is Util.shuffle over a copy (Fisher-Yates from the end).
func shuffledDirs(in []int, r Random) []int {
	out := append([]int(nil), in...)
	for i := int32(len(out)); i > 1; i-- {
		j := r.NextIntN(i)
		out[i-1], out[j] = out[j], out[i-1]
	}
	return out
}

func clockWise(d int) int {
	for i, h := range horizontalDirs {
		if h == d {
			return horizontalDirs[(i+1)%4]
		}
	}
	return d
}

func counterClockWise(d int) int {
	for i, h := range horizontalDirs {
		if h == d {
			return horizontalDirs[(i+3)%4]
		}
	}
	return d
}

// randomTagElement is Registry.getRandomElementOf: a uniform pick from
// the tag's entries in load order (nested tags expanded in place).
func randomTagElement(tag string, r Random) (string, bool) {
	list := orderedBlockTags[tag]
	if len(list) == 0 {
		return "", false
	}
	return list[r.NextIntN(int32(len(list)))], true
}

// orderedBlockTags holds the tags the coral features draw from, in the
// 26.2 data pack's load order (data/minecraft/tags/block/*.json, nested
// #coral_plants expanded first in #corals). data.BlockTags is sorted by
// name and the bundled worldgen data carries no block tags, so the
// draw order is listed here.
var orderedBlockTags = map[string][]string{
	"coral_blocks": {"tube_coral_block", "brain_coral_block", "bubble_coral_block", "fire_coral_block", "horn_coral_block"},
	"corals": {"tube_coral", "brain_coral", "bubble_coral", "fire_coral", "horn_coral",
		"tube_coral_fan", "brain_coral_fan", "bubble_coral_fan", "fire_coral_fan", "horn_coral_fan"},
	"wall_corals": {"tube_coral_wall_fan", "brain_coral_wall_fan", "bubble_coral_wall_fan", "fire_coral_wall_fan", "horn_coral_wall_fan"},
}
