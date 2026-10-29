package worldgen

import (
	"encoding/json"
	"math"

	"mc-go/internal/world"
)

type oreTarget struct {
	test  ruleTest
	state world.Block
}

type oreConfig struct {
	size    int32
	discard float32
	targets []oreTarget
}

func parseOreConfig(cfg obj) *oreConfig {
	c := &oreConfig{size: cfg.i32("size"), discard: parseF32(cfg["discard_chance_on_air_exposure"])}
	var ts []obj
	jsonUnmarshal(cfg["targets"], &ts)
	for _, t := range ts {
		var st obj
		jsonUnmarshal(t["state"], &st)
		c.targets = append(c.targets, oreTarget{compileRuleTest(t["target"]), stateFromJSON(st)})
	}
	return c
}

func init() {
	featureBuilders["ore"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		oc := parseOreConfig(cfg)
		return func(c *featCtx, r Random, p BlockPos) bool { return placeOre(c.rg, r, oc, p) }
	}
	featureBuilders["scattered_ore"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		oc := parseOreConfig(cfg)
		return func(c *featCtx, r Random, p BlockPos) bool { return placeScatteredOre(c.rg, r, oc, p) }
	}
}

func mthCeil(v float64) int32 { return d2i(math.Ceil(v)) }

func shouldSkipAirCheck(r Random, chance float32) bool {
	if chance <= 0 {
		return true
	}
	if chance >= 1 {
		return false
	}
	return r.NextFloat() >= chance
}

func adjacentToAir(get func(x, y, z int32) world.Block, p BlockPos) bool {
	for _, d := range dirOffsets {
		if isAir(get(p[0]+d[0], p[1]+d[1], p[2]+d[2])) {
			return true
		}
	}
	return false
}

func canPlaceOre(b world.Block, get func(x, y, z int32) world.Block, r Random, oc *oreConfig, t oreTarget, p BlockPos) bool {
	if !t.test(b, r) {
		return false
	}
	if shouldSkipAirCheck(r, oc.discard) {
		return true
	}
	return !adjacentToAir(get, p)
}

// placeOre is OreFeature.place.
func placeOre(rg *Region, r Random, oc *oreConfig, p BlockPos) bool {
	f := float32(r.NextFloat() * 3.1415927)
	g := float32(oc.size) / 8
	i := mthCeil(float64((float32(oc.size)/16*2 + 1) / 2))
	sinF := math.Sin(float64(f))
	cosF := math.Cos(float64(f))
	d := float64(p[0]) + float64(sinF*float64(g))
	e := float64(p[0]) - float64(sinF*float64(g))
	h := float64(p[2]) + float64(cosF*float64(g))
	k := float64(p[2]) - float64(cosF*float64(g))
	m := float64(p[1] + r.NextIntN(3) - 2)
	n := float64(p[1] + r.NextIntN(3) - 2)
	o := p[0] - mthCeil(float64(g)) - i
	pp := p[1] - 2 - i
	q := p[2] - mthCeil(float64(g)) - i
	rr := 2 * (mthCeil(float64(g)) + i)
	s := 2 * (2 + i)
	for t := o; t <= o+rr; t++ {
		for u := q; u <= q+rr; u++ {
			if pp <= rg.Height(HMOceanFloorWG, t, u) {
				return doPlaceOre(rg, r, oc, d, e, h, k, m, n, o, pp, q, rr, s)
			}
		}
	}
	return false
}

func doPlaceOre(rg *Region, r Random, oc *oreConfig, d, e, h, k, m, n float64, x0, y0, z0, w, ht int32) bool {
	placed := 0
	used := make([]bool, w*ht*w)
	size := oc.size
	ds := make([]float64, size*4)
	for t := int32(0); t < size; t++ {
		f := float32(t) / float32(size)
		dx := lerp(float64(f), d, e)
		dy := lerp(float64(f), m, n)
		dz := lerp(float64(f), h, k)
		g := float64(r.NextDouble()*float64(size)) / 16
		rad := float64(float64(float32(mthSin(float64(float32(3.1415927*f)))+1))*g+1) / 2
		ds[t*4], ds[t*4+1], ds[t*4+2], ds[t*4+3] = dx, dy, dz, rad
	}
	for t := int32(0); t < size-1; t++ {
		if ds[t*4+3] <= 0 {
			continue
		}
		for u := t + 1; u < size; u++ {
			if ds[u*4+3] <= 0 {
				continue
			}
			a := ds[t*4] - ds[u*4]
			b := ds[t*4+1] - ds[u*4+1]
			c := ds[t*4+2] - ds[u*4+2]
			dd := ds[t*4+3] - ds[u*4+3]
			if float64(dd*dd) > float64(a*a)+float64(b*b)+float64(c*c) {
				if dd > 0 {
					ds[u*4+3] = -1
				} else {
					ds[t*4+3] = -1
				}
			}
		}
	}
	get := func(x, y, z int32) world.Block { return rg.Block(x, y, z) }
	for t := int32(0); t < size; t++ {
		rad := ds[t*4+3]
		if rad < 0 {
			continue
		}
		cx, cy, cz := ds[t*4], ds[t*4+1], ds[t*4+2]
		ax := max(floorI(cx-rad), x0)
		ay := max(floorI(cy-rad), y0)
		az := max(floorI(cz-rad), z0)
		bx := max(floorI(cx+rad), ax)
		by := max(floorI(cy+rad), ay)
		bz := max(floorI(cz+rad), az)
		for x := ax; x <= bx; x++ {
			fx := (float64(x) + 0.5 - cx) / rad
			if float64(fx*fx) >= 1 {
				continue
			}
			for y := ay; y <= by; y++ {
				fy := (float64(y) + 0.5 - cy) / rad
				if float64(fx*fx)+float64(fy*fy) >= 1 {
					continue
				}
				for z := az; z <= bz; z++ {
					fz := (float64(z) + 0.5 - cz) / rad
					if float64(fx*fx)+float64(fy*fy)+float64(fz*fz) >= 1 {
						continue
					}
					if y < rg.MinY() || y > rg.MaxY() {
						continue
					}
					bi := (x - x0) + (y-y0)*w + (z-z0)*w*ht
					if used[bi] {
						continue
					}
					used[bi] = true
					pos := BlockPos{x, y, z}
					if !rg.canWrite(pos) {
						continue
					}
					cur := rg.Block(x, y, z)
					for _, tg := range oc.targets {
						if canPlaceOre(cur, get, r, oc, tg, pos) {
							rg.setRaw(x, y, z, tg.state)
							placed++
							break
						}
					}
				}
			}
		}
	}
	return placed > 0
}

func placeScatteredOre(rg *Region, r Random, oc *oreConfig, p BlockPos) bool {
	n := r.NextIntN(oc.size + 1)
	get := func(x, y, z int32) world.Block { return rg.Block(x, y, z) }
	for i := int32(0); i < n; i++ {
		spread := min(i, 7)
		q := scatterOffset(r, p, spread)
		cur := rg.Block(q[0], q[1], q[2])
		for _, tg := range oc.targets {
			if canPlaceOre(cur, get, r, oc, tg, q) {
				rg.SetBlock(q[0], q[1], q[2], tg.state)
				break
			}
		}
	}
	return true
}

// scatterOffset is ScatteredOreFeature.offsetTargetPos.
func scatterOffset(r Random, p BlockPos, spread int32) BlockPos {
	dx := scatterDelta(r, spread)
	dy := scatterDelta(r, spread)
	dz := scatterDelta(r, spread)
	return BlockPos{p[0] + dx, p[1] + dy, p[2] + dz}
}

func scatterDelta(r Random, spread int32) int32 {
	return javaRoundF((r.NextFloat() - r.NextFloat()) * float32(spread))
}

// javaRoundF is Math.round(float): floor(f + 1/2) as an int.
func javaRoundF(f float32) int32 {
	fl := float32(math.Floor(float64(f)))
	if f-fl >= 0.5 {
		fl++
	}
	return int32(fl)
}

var _ = json.RawMessage(nil)
