package worldgen

import (
	"encoding/json"
	"math"

	"mc-go/internal/world"
)

// large_dripstone (LargeDripstoneFeature): a stalactite/stalagmite pair
// spanning a cave of at least four cells, each a cone of dripstone
// blocks whose profile is SpeleothemUtils.getSpeleothemHeight, sunk
// until its base circle sits in stone and optionally bent by wind.

// intProviderBounds is IntProvider.minInclusive/maxInclusive.
func intProviderBounds(raw json.RawMessage) (int32, int32) {
	if isNumber(raw) {
		v := int32(obj{"v": raw}.f64("v"))
		return v, v
	}
	var o obj
	jsonUnmarshal(raw, &o)
	switch trimNS(o.str("type")) {
	case "constant":
		v := o.i32("value")
		return v, v
	case "clamped":
		lo, hi := intProviderBounds(o["source"])
		return max(o.i32("min_inclusive"), lo), min(o.i32("max_inclusive"), hi)
	case "clamped_normal":
		return o.i32("min_inclusive"), o.i32("max_inclusive")
	case "trapezoid":
		return o.i32("min"), o.i32("max")
	case "weighted_list":
		var entries []obj
		jsonUnmarshal(o["distribution"], &entries)
		lo, hi := int32(math.MaxInt32), int32(math.MinInt32)
		for _, e := range entries {
			a, b := intProviderBounds(e["data"])
			lo, hi = min(lo, a), max(hi, b)
		}
		return lo, hi
	}
	return o.i32("min_inclusive"), o.i32("max_inclusive")
}

// speleothemHeight is SpeleothemUtils.getSpeleothemHeight.
func speleothemHeight(at, radius, scale, bluntness float64) float64 {
	if at < bluntness {
		at = bluntness
	}
	f := float64(float64(at/radius) * 0.384)
	g := float64(0.75 * math.Pow(f, 1.3333333333333333))
	h := math.Pow(f, 0.6666666666666666)
	i := float64(0.3333333333333333 * math.Log(f))
	j := float64(scale * float64(float64(g-h)-i))
	j = math.Max(j, 0)
	return float64(float64(j/0.384) * radius)
}

type windOffsetter struct {
	on       bool
	originY  int32
	wx, wz   float64
	maxShift int32
}

func (w windOffsetter) offset(p BlockPos) BlockPos {
	if !w.on {
		return p
	}
	i := float64(w.originY - p[1])
	x := clampI(floorI(float64(w.wx*i)), -w.maxShift, w.maxShift)
	z := clampI(floorI(float64(w.wz*i)), -w.maxShift, w.maxShift)
	return BlockPos{p[0] + x, p[1], p[2] + z}
}

type largeDripstone struct {
	root      BlockPos
	up        bool
	radius    int32
	bluntness float64
	scale     float64
}

func (d *largeDripstone) heightAt(r float32) int32 {
	return d2i(speleothemHeight(float64(r), float64(d.radius), d.scale, d.bluntness))
}

// circleInStone is SpeleothemUtils.isCircleMostlyEmbeddedInStone.
func circleInStone(rg *Region, p BlockPos, radius int32) bool {
	if emptyOrWaterOrLava(rg.Block(p[0], p[1], p[2])) {
		return false
	}
	step := float32(6.0) / float32(radius)
	for a := float32(0); a < 6.2831855; a += step {
		i := int32(float32(mthCos(float64(a)) * float32(radius)))
		j := int32(float32(mthSin(float64(a)) * float32(radius)))
		if emptyOrWaterOrLava(rg.Block(p[0]+i, p[1], p[2]+j)) {
			return false
		}
	}
	return true
}

// settle is moveBackUntilBaseIsInsideStoneAndShrinkRadiusIfNecessary.
func (d *largeDripstone) settle(rg *Region, w windOffsetter) bool {
	for d.radius > 1 {
		q := d.root
		n := min(10, d.heightAt(0))
		for i := int32(0); i < n; i++ {
			if isBlock(rg.Block(q[0], q[1], q[2]), "lava") {
				return false
			}
			if circleInStone(rg, w.offset(q), d.radius) {
				d.root = q
				return true
			}
			if d.up {
				q[1]--
			} else {
				q[1]++
			}
		}
		d.radius /= 2
	}
	return false
}

// place is LargeDripstone.placeBlocks.
func (d *largeDripstone) place(rg *Region, r Random, w windOffsetter) {
	block := blockNamed("dripstone_block")
	for dx := -d.radius; dx <= d.radius; dx++ {
		for dz := -d.radius; dz <= d.radius; dz++ {
			f := float32(math.Sqrt(float64(float32(dx*dx + dz*dz))))
			if f > float32(d.radius) {
				continue
			}
			n := d.heightAt(f)
			if n <= 0 {
				continue
			}
			if float64(r.NextFloat()) < 0.2 {
				// Mth.randomBetween(0.8f, 1.0f), float arithmetic.
				lo, hi := float32(0.8), float32(1.0)
				n = int32(float32(float32(n) * float32(float32(r.NextFloat()*(hi-lo))+lo)))
			}
			q := offset(d.root, dx, 0, dz)
			lim := int32(math.MaxInt32)
			if d.up {
				lim = rg.Height(HMWorldSurfaceWG, q[0], q[2])
			}
			placed := false
			for k := int32(0); k < n; k++ {
				if q[1] >= lim {
					break
				}
				p := w.offset(q)
				b := rg.Block(p[0], p[1], p[2])
				if emptyOrWaterOrLava(b) {
					placed = true
					rg.SetBlock(p[0], p[1], p[2], block)
				} else if placed && blockInTag(b, "base_stone_overworld") {
					break
				}
				if d.up {
					q[1]++
				} else {
					q[1]--
				}
			}
		}
	}
}

func init() {
	featureBuilders["large_dripstone"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		search := int32(30)
		if _, ok := cfg["floor_to_ceiling_search_range"]; ok {
			search = cfg.i32("floor_to_ceiling_search_range")
		}
		replace := blockNameSet(cfg["replaceable_blocks"])
		ratio := parseF32(cfg["max_column_radius_to_cave_height_ratio"])
		rmin, rmax := intProviderBounds(cfg["column_radius"])
		heightScale := parseFloatProvider(cfg["height_scale"])
		stalactiteBlunt := parseFloatProvider(cfg["stalactite_bluntness"])
		stalagmiteBlunt := parseFloatProvider(cfg["stalagmite_bluntness"])
		windSpeed := parseFloatProvider(cfg["wind_speed"])
		minWindRadius := cfg.i32("min_radius_for_wind")
		minWindBlunt := parseF32(cfg["min_bluntness_for_wind"])
		edge := func(b world.Block) bool {
			return isBlock(b, "dripstone_block") || inTag(b, replace) || isBlock(b, "lava")
		}
		scan := func(rg *Region, p BlockPos, step int32) (int32, bool) {
			y := p[1]
			for i := int32(1); i < search && emptyOrWater(rg.Block(p[0], y, p[2])); i++ {
				y += step
			}
			return y, edge(rg.Block(p[0], y, p[2]))
		}
		return func(c *featCtx, r Random, p BlockPos) bool {
			rg := c.rg
			if !emptyOrWater(rg.Block(p[0], p[1], p[2])) {
				return false
			}
			ceil, okC := scan(rg, p, 1)
			floor, okF := scan(rg, p, -1)
			if !okC || !okF {
				return false
			}
			height := ceil - floor - 1
			if height < 4 {
				return false
			}
			i := int32(float32(float32(height) * ratio))
			j := clampI(i, rmin, rmax)
			k := randBetweenInclusive(r, rmin, j)
			mk := func(y int32, up bool, blunt floatProvider) *largeDripstone {
				b := float64(blunt(r))
				s := float64(heightScale(r))
				return &largeDripstone{root: BlockPos{p[0], y, p[2]}, up: up, radius: k, bluntness: b, scale: s}
			}
			top := mk(ceil-1, false, stalactiteBlunt)
			bottom := mk(floor+1, true, stalagmiteBlunt)
			suits := func(d *largeDripstone) bool {
				return d.radius >= minWindRadius && d.bluntness >= float64(minWindBlunt)
			}
			var w windOffsetter
			if suits(top) && suits(bottom) {
				f := windSpeed(r)
				a := float32(r.NextFloat()*3.1415927) + 0
				w = windOffsetter{on: true, originY: p[1], maxShift: 16 - k,
					wx: float64(float32(mthCos(float64(a)) * f)), wz: float64(float32(mthSin(float64(a)) * f))}
			}
			okTop := top.settle(rg, w)
			okBottom := bottom.settle(rg, w)
			if okTop {
				top.place(rg, r, w)
			}
			if okBottom {
				bottom.place(rg, r, w)
			}
			return true
		}
	}
}
