package worldgen

import "math"

// Icebergs, measured from IcebergFeature (26.2). An iceberg is a round
// or (30%) elliptic body rising overWater blocks above the sea and
// reaching underWater blocks below it, built from the configured ice
// with an optional snow cap, then smoothed and, usually, carved through
// by a channel. All radii are float-rounded as the game computes them.

func init() {
	featureKinds["minecraft:iceberg"] = func(cfg any) func(*featureCtx) bool {
		return iceberg{blockStateOf(ffield(cfg, "state"))}.place
	}
}

type iceberg struct{ main StateID }

// icebergShape is one iceberg's random shape.
type icebergShape struct {
	ellipse, snowOnTop bool
	ellipseA, ellipseC int32
	angle              float64
}

func isIcebergState(s StateID) bool {
	return s.is("packed_ice") || s.is("snow_block") || s.is("blue_ice")
}

func (ib iceberg) place(fc *featureCtx) bool {
	r, random := fc.r, fc.rand
	o := Point{fc.origin.X, r.seaLevel, fc.origin.Z}
	var sh icebergShape
	sh.snowOnTop = random.NextDouble() > 0.7
	sh.angle = float64(random.NextDouble()*2) * math.Pi
	sh.ellipseA = 11 - random.NextIntn(5)
	sh.ellipseC = 3 + random.NextIntn(3)
	sh.ellipse = random.NextDouble() > 0.7
	var over int32
	if sh.ellipse {
		over = random.NextIntn(6) + 6
	} else {
		over = random.NextIntn(15) + 3
	}
	if !sh.ellipse && random.NextDouble() > 0.9 {
		over += random.NextIntn(19) + 7
	}
	under := min(over+random.NextIntn(11), 18)
	width := min(over+random.NextIntn(7)-random.NextIntn(5), 11)
	a := int32(11)
	if sh.ellipse {
		a = sh.ellipseA
	}
	for x := -a; x < a; x++ {
		for z := -a; z < a; z++ {
			for y := int32(0); y < over; y++ {
				var rad int32
				if sh.ellipse {
					rad = icebergRadiusEllipse(y, over, width)
				} else {
					rad = icebergRadiusRound(random, y, over, width)
				}
				if sh.ellipse || x < rad {
					ib.block(r, random, o, sh, over, x, y, z, rad, a)
				}
			}
		}
	}
	icebergSmooth(r, o, width, over, sh)
	for x := -a; x < a; x++ {
		for z := -a; z < a; z++ {
			for y := int32(-1); y > -under; y-- {
				ea := a
				if sh.ellipse {
					sq := float32(math.Pow(float64(y), 2))
					ea = mthCeilF(float32(a) * float32(1-float32(sq/float32(float32(under)*8))))
				}
				rad := icebergRadiusSteep(random, -y, under, width)
				if x < rad {
					ib.block(r, random, o, sh, under, x, y, z, rad, ea)
				}
			}
		}
	}
	var cut bool
	if sh.ellipse {
		cut = random.NextDouble() > 0.1
	} else {
		cut = random.NextDouble() > 0.7
	}
	if cut {
		icebergCutOut(r, random, width, over, o, sh)
	}
	return true
}

// mthCeilF is Mth.ceil of a float.
func mthCeilF(v float32) int32 {
	i := int32(v)
	if v > float32(i) {
		return i + 1
	}
	return i
}

func icebergRadiusRound(random Random, y, height, width int32) int32 {
	k := 3.5 - random.NextFloat()
	sq := float32(math.Pow(float64(y), 2))
	scale := float32(1-float32(sq/float32(float32(height)*k))) * float32(width)
	if height > 15+random.NextIntn(5) {
		ty := y
		if y < 3+random.NextIntn(6) {
			ty = y / 2
		}
		scale = float32(1-float32(float32(ty)/float32(float32(float32(height)*k)*0.4))) * float32(width)
	}
	return mthCeilF(scale / 2)
}

func icebergRadiusEllipse(y, height, width int32) int32 {
	sq := float32(math.Pow(float64(y), 2))
	scale := float32(1-float32(sq/float32(float32(height)*1))) * float32(width)
	return mthCeilF(scale / 2)
}

func icebergRadiusSteep(random Random, y, height, width int32) int32 {
	k := 1 + float32(random.NextFloat()/2)
	scale := float32(1-float32(float32(y)/float32(float32(height)*k))) * float32(width)
	return mthCeilF(scale / 2)
}

func icebergEllipseC(y, height, c int32) int32 {
	if y > 0 && height-y <= 3 {
		c -= 4 - (height - y)
	}
	return c
}

// signedDistanceCircle is the squared distance outside a jittered circle.
func signedDistanceCircle(x, z int32, c Point, rad int32, random Random) float64 {
	off := float32(10*clampF(random.NextFloat(), 0.2, 0.8)) / float32(rad)
	dx, dz := float64(x-c.X), float64(z-c.Z)
	return float64(off) + float64(dx*dx) + float64(dz*dz) - math.Pow(float64(rad), 2)
}

// signedDistanceEllipse is below zero inside the rotated ellipse.
func signedDistanceEllipse(x, z int32, c Point, a, b int32, angle float64) float64 {
	dx, dz := float64(x-c.X), float64(z-c.Z)
	cos, sin := jcos(angle), jsin(angle)
	u := (float64(dx*cos) - float64(dz*sin)) / float64(a)
	v := (float64(dx*sin) + float64(dz*cos)) / float64(b)
	return float64(u*u) + float64(v*v) - 1
}

func (ib iceberg) block(r *decoRegion, random Random, o Point, sh icebergShape, height, x, y, z, rad, a int32) {
	var dist float64
	if sh.ellipse {
		dist = signedDistanceEllipse(x, z, Point{}, a, icebergEllipseC(y, height, sh.ellipseC), sh.angle)
	} else {
		dist = signedDistanceCircle(x, z, Point{}, rad, random)
	}
	// A zero axis makes the distance NaN, which is outside.
	if !(dist < 0) {
		return
	}
	p := o.add(Point{x, y, z})
	edge := -0.5
	if !sh.ellipse {
		edge = float64(-6 - random.NextIntn(3))
	}
	if dist > edge && random.NextDouble() > 0.9 {
		return
	}
	ib.set(r, random, p, height-y, height, sh)
}

func (ib iceberg) set(r *decoRegion, random Random, p Point, depth, height int32, sh icebergShape) {
	s := r.get(p)
	if !s.isAir() && !s.is("snow_block") && !s.is("ice") && !s.is("water") {
		return
	}
	jitter := !sh.ellipse || random.NextDouble() > 0.05
	div := int32(2)
	if sh.ellipse {
		div = 3
	}
	if sh.snowOnTop && !s.is("water") &&
		float64(depth) <= float64(random.NextIntn(max(1, height/div)))+float64(float64(height)*0.6) && jitter {
		r.set(p, defaultState("snow_block"))
	} else {
		r.set(p, ib.main)
	}
}

// icebergSmooth clears ice over air and ice with three or more open
// sides.
func icebergSmooth(r *decoRegion, o Point, width, height int32, sh icebergShape) {
	a := width / 2
	if sh.ellipse {
		a = sh.ellipseA
	}
	for x := -a; x <= a; x++ {
		for z := -a; z <= a; z++ {
			for y := int32(0); y <= height; y++ {
				p := o.add(Point{x, y, z})
				s := r.get(p)
				if !isIcebergState(s) && !s.is("snow") {
					continue
				}
				if r.get(p.offset(dirDown)).isAir() {
					r.set(p, stAir)
					r.set(p.offset(dirUp), stAir)
				} else if isIcebergState(s) {
					open := 0
					for _, d := range [4]int{dirWest, dirEast, dirNorth, dirSouth} {
						if !isIcebergState(r.get(p.offset(d))) {
							open++
						}
					}
					if open >= 3 {
						r.set(p, stAir)
					}
				}
			}
		}
	}
}

func icebergCutOut(r *decoRegion, random Random, width, height int32, o Point, sh icebergShape) {
	sx, sz := int32(1), int32(1)
	if random.NextBool() {
		sx = -1
	}
	if random.NextBool() {
		sz = -1
	}
	dx := random.NextIntn(max(width/2-2, 1))
	if random.NextBool() {
		dx = width/2 + 1 - random.NextIntn(max(width-width/2-1, 1))
	}
	dz := random.NextIntn(max(width/2-2, 1))
	if random.NextBool() {
		dz = width/2 + 1 - random.NextIntn(max(width-width/2-1, 1))
	}
	if sh.ellipse {
		dx = random.NextIntn(max(sh.ellipseA-5, 1))
		dz = dx
	}
	local := Point{sx * dx, 0, sz * dz}
	var angle float64
	if sh.ellipse {
		angle = sh.angle + math.Pi/2
	} else {
		angle = float64(random.NextDouble()*2) * math.Pi
	}
	for y := int32(0); y < height-3; y++ {
		rad := icebergRadiusRound(random, y, height, width)
		icebergCarve(r, rad, y, o, false, angle, local, sh)
	}
	for y := int32(-1); y > -height+random.NextIntn(5); y-- {
		rad := icebergRadiusSteep(random, -y, height, width)
		icebergCarve(r, rad, y, o, true, angle, local, sh)
	}
}

func icebergCarve(r *decoRegion, rad, y int32, o Point, underWater bool, angle float64, local Point, sh icebergShape) {
	a := rad + 1 + sh.ellipseA/3
	c := min(rad-3, 3) + sh.ellipseC/2 - 1
	for x := -a; x < a; x++ {
		for z := -a; z < a; z++ {
			if !(signedDistanceEllipse(x, z, local, a, c, angle) < 0) {
				continue
			}
			p := o.add(Point{x, y, z})
			s := r.get(p)
			if !isIcebergState(s) && !s.is("snow_block") {
				continue
			}
			if underWater {
				r.set(p, stWater)
			} else {
				r.set(p, stAir)
				if up := p.offset(dirUp); r.get(up).is("snow") {
					r.set(up, stAir)
				}
			}
		}
	}
}
