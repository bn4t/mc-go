package worldgen

import (
	"math"

	"mc-go/internal/world"
)

func init() {
	featureBuilders["iceberg"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		var st obj
		jsonUnmarshal(cfg["state"], &st)
		state := stateFromJSON(st)
		return func(c *featCtx, r Random, p BlockPos) bool { return placeIceberg(c.rg, r, state, p) }
	}
}

// ceilF is Mth.ceil(float).
func ceilF(f float32) int32 {
	i := int32(f)
	if f > float32(i) {
		return i + 1
	}
	return i
}

func isIcebergState(b world.Block) bool {
	return isBlock(b, "packed_ice") || isBlock(b, "snow_block") || isBlock(b, "blue_ice")
}

type iceberg struct {
	rg      *Region
	r       Random
	origin  BlockPos
	state   world.Block
	snowTop bool
	ellipse bool
	angle   float64
	shapeA  int32
}

// placeIceberg is IcebergFeature.place: the shape parameters are drawn
// up front (snow cap, angle, ellipse size, ellipse-or-round, above and
// below water heights, width), the part above sea level is filled,
// smoothed, the underwater part added, and a cut-out maybe carved.
func placeIceberg(rg *Region, r Random, state world.Block, p BlockPos) bool {
	ib := &iceberg{rg: rg, r: r, origin: BlockPos{p[0], rg.seaLevel(), p[2]}, state: state}
	ib.snowTop = r.NextDouble() > 0.7
	ib.angle = float64(r.NextDouble()*2) * math.Pi
	ellC := 11 - r.NextIntN(5)
	ib.shapeA = 3 + r.NextIntN(3)
	ib.ellipse = r.NextDouble() > 0.7
	var over int32
	if ib.ellipse {
		over = r.NextIntN(6) + 6
	} else {
		over = r.NextIntN(15) + 3
	}
	if !ib.ellipse && r.NextDouble() > 0.9 {
		over += r.NextIntN(19) + 7
	}
	under := min(over+r.NextIntN(11), 18)
	width := min(over+r.NextIntN(7)-r.NextIntN(5), 11)
	a := int32(11)
	if ib.ellipse {
		a = ellC
	}
	for x := -a; x < a; x++ {
		for z := -a; z < a; z++ {
			for y := int32(0); y < over; y++ {
				var rad int32
				if ib.ellipse {
					rad = ellipseRadius(y, over, width)
				} else {
					rad = roundRadius(r, y, over, width)
				}
				if ib.ellipse || x < rad {
					ib.block(over, x, y, z, rad, a)
				}
			}
		}
	}
	ib.smooth(width, over, ellC)
	for x := -a; x < a; x++ {
		for z := -a; z < a; z++ {
			for y := int32(-1); y > -under; y-- {
				ea := a
				if ib.ellipse {
					ea = ceilF(float32(a) * (1 - float32(math.Pow(float64(y), 2))/float32(float32(under)*8)))
				}
				rad := steepRadius(r, -y, under, width)
				if x < rad {
					ib.block(under, x, y, z, rad, ea)
				}
			}
		}
	}
	var cut bool
	if ib.ellipse {
		cut = r.NextDouble() > 0.1
	} else {
		cut = r.NextDouble() > 0.7
	}
	if cut {
		ib.cutOut(width, over, ellC)
	}
	return true
}

// roundRadius is IcebergFeature.heightDependentRadiusRound.
func roundRadius(r Random, y, height, width int32) int32 {
	f := 3.5 - r.NextFloat()
	g := (1 - float32(math.Pow(float64(y), 2))/float32(float32(height)*f)) * float32(width)
	if height > 15+r.NextIntN(5) {
		yy := y
		if y < 3+r.NextIntN(6) {
			yy = y / 2
		}
		g = (1 - float32(yy)/float32(float32(float32(height)*f)*0.4)) * float32(width)
	}
	return ceilF(g / 2)
}

// ellipseRadius is IcebergFeature.heightDependentRadiusEllipse.
func ellipseRadius(y, height, width int32) int32 {
	f := (1 - float32(math.Pow(float64(y), 2))/float32(float32(height)*1)) * float32(width)
	return ceilF(f / 2)
}

// steepRadius is IcebergFeature.heightDependentRadiusSteep.
func steepRadius(r Random, y, height, width int32) int32 {
	f := 1 + r.NextFloat()/2
	g := (1 - float32(y)/float32(float32(height)*f)) * float32(width)
	return ceilF(g / 2)
}

// ellipseDist is IcebergFeature.signedDistanceEllipse.
func ellipseDist(x, z int32, c BlockPos, a, b int32, angle float64) float64 {
	dx, dz := float64(x-c[0]), float64(z-c[2])
	cs, sn := math.Cos(angle), math.Sin(angle)
	u := float64(float64(dx*cs)-float64(dz*sn)) / float64(a)
	v := float64(float64(dx*sn)+float64(dz*cs)) / float64(b)
	return float64(u*u) + float64(v*v) - 1
}

// circleDist is IcebergFeature.signedDistanceCircle.
func circleDist(x, z, radius int32, r Random) float64 {
	f := float32(10*clampF(r.NextFloat(), 0.2, 0.8)) / float32(radius)
	return float64(f) + math.Pow(float64(x), 2) + math.Pow(float64(z), 2) - math.Pow(float64(radius), 2)
}

// ellipseC is IcebergFeature.getEllipseC.
func ellipseC(y, height, c int32) int32 {
	if y > 0 && height-y <= 3 {
		c -= 4 - (height - y)
	}
	return c
}

// block is IcebergFeature.generateIcebergBlock.
func (ib *iceberg) block(height, x, y, z, rad, a int32) {
	var d float64
	if ib.ellipse {
		d = ellipseDist(x, z, BlockPos{}, a, ellipseC(y, height, ib.shapeA), ib.angle)
	} else {
		d = circleDist(x, z, rad, ib.r)
	}
	if d >= 0 {
		return
	}
	p := offset(ib.origin, x, y, z)
	edge := -0.5
	if !ib.ellipse {
		edge = float64(-6 - ib.r.NextIntN(3))
	}
	if d > edge && ib.r.NextDouble() > 0.9 {
		return
	}
	ib.set(p, height-y, height)
}

// set is IcebergFeature.setIcebergBlock: open cells (air, snow block,
// ice, water) take the iceberg state, or a snow block near the top of
// a snow-capped berg.
func (ib *iceberg) set(p BlockPos, diff, height int32) {
	st := ib.rg.at(p)
	if !isAir(st) && !isBlock(st, "snow_block") && !isBlock(st, "ice") && !isBlock(st, "water") {
		return
	}
	keep := !ib.ellipse || ib.r.NextDouble() > 0.05
	div := int32(2)
	if ib.ellipse {
		div = 3
	}
	if ib.snowTop && !isBlock(st, "water") &&
		float64(diff) <= float64(ib.r.NextIntN(max(1, height/div)))+float64(float64(height)*0.6) && keep {
		ib.rg.set(p, blockNamed("snow_block"))
		return
	}
	ib.rg.set(p, ib.state)
}

// smooth is IcebergFeature.smooth: overhanging cells (air below) and
// iceberg cells with three or more open horizontal sides are cleared.
func (ib *iceberg) smooth(width, height, c int32) {
	n := width / 2
	if ib.ellipse {
		n = c
	}
	for x := -n; x <= n; x++ {
		for z := -n; z <= n; z++ {
			for y := int32(0); y <= height; y++ {
				p := offset(ib.origin, x, y, z)
				st := ib.rg.at(p)
				if !isIcebergState(st) && !isBlock(st, "snow") {
					continue
				}
				if isAir(ib.rg.at(offset(p, 0, -1, 0))) {
					ib.rg.set(p, world.Air)
					ib.rg.set(offset(p, 0, 1, 0), world.Air)
					continue
				}
				if !isIcebergState(st) {
					continue
				}
				open := 0
				for _, q := range [4]BlockPos{offset(p, -1, 0, 0), offset(p, 1, 0, 0), offset(p, 0, 0, -1), offset(p, 0, 0, 1)} {
					if !isIcebergState(ib.rg.at(q)) {
						open++
					}
				}
				if open >= 3 {
					ib.rg.set(p, world.Air)
				}
			}
		}
	}
}

// cutOut is IcebergFeature.generateCutOut: an off-center elliptical
// channel carved through the berg, air above sea level and water below.
func (ib *iceberg) cutOut(width, height, c int32) {
	r := ib.r
	sx := int32(1)
	if r.NextBool() {
		sx = -1
	}
	sz := int32(1)
	if r.NextBool() {
		sz = -1
	}
	ox := r.NextIntN(max(width/2-2, 1))
	if r.NextBool() {
		ox = width/2 + 1 - r.NextIntN(max(width-width/2-1, 1))
	}
	oz := r.NextIntN(max(width/2-2, 1))
	if r.NextBool() {
		oz = width/2 + 1 - r.NextIntN(max(width-width/2-1, 1))
	}
	if ib.ellipse {
		ox = r.NextIntN(max(c-5, 1))
		oz = ox
	}
	center := BlockPos{sx * ox, 0, sz * oz}
	var angle float64
	if ib.ellipse {
		angle = ib.angle + math.Pi/2
	} else {
		angle = float64(r.NextDouble()*2) * math.Pi
	}
	for y := int32(0); y < height-3; y++ {
		rad := roundRadius(r, y, height, width)
		ib.carve(rad, y, false, angle, center, c)
	}
	for y := int32(-1); y > -height+r.NextIntN(5); y-- {
		rad := steepRadius(r, -y, height, width)
		ib.carve(rad, y, true, angle, center, c)
	}
}

// carve is IcebergFeature.carve.
func (ib *iceberg) carve(rad, y int32, underwater bool, angle float64, center BlockPos, c int32) {
	a := rad + 1 + c/3
	b := min(rad-3, 3) + ib.shapeA/2 - 1
	for x := -a; x < a; x++ {
		for z := -a; z < a; z++ {
			if ellipseDist(x, z, center, a, b, angle) >= 0 {
				continue
			}
			p := offset(ib.origin, x, y, z)
			st := ib.rg.at(p)
			if !isIcebergState(st) && !isBlock(st, "snow_block") {
				continue
			}
			if underwater {
				ib.rg.set(p, blockNamed("water"))
				continue
			}
			ib.rg.set(p, world.Air)
			// removeFloatingSnowLayer.
			up := offset(p, 0, 1, 0)
			if isBlock(ib.rg.at(up), "snow") {
				ib.rg.set(up, world.Air)
			}
		}
	}
}
