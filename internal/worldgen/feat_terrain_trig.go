package worldgen

import "math"

// java.lang.Math.sin/cos stand-ins. HotSpot evaluates them with its
// libm intrinsics, which are correctly rounded for all but a fraction
// of a percent of arguments; Go's math.Sin/Cos miss the correctly
// rounded result for roughly two in five arguments, often enough to
// flip IcebergFeature's ellipse edge tests (measured against java's
// Math.sin/cos over 200k arguments in [0, 2.5pi): correctly rounded
// values match java on 99.6%, math.Sin/Cos on 42%). These evaluate in
// double-double arithmetic and round once.

type ddv struct{ hi, lo float64 }

func twoSum(a, b float64) (float64, float64) {
	s := a + b
	bb := s - a
	return s, (a - (s - bb)) + (b - bb)
}

func quickTwoSum(a, b float64) ddv {
	s := a + b
	return ddv{s, b - (s - a)}
}

func twoProd(a, b float64) (float64, float64) {
	p := a * b
	return p, math.FMA(a, b, -p)
}

func ddAdd(a, b ddv) ddv {
	s, e := twoSum(a.hi, b.hi)
	e += a.lo + b.lo
	return quickTwoSum(s, e)
}

func ddNeg(a ddv) ddv { return ddv{-a.hi, -a.lo} }

func ddMul(a, b ddv) ddv {
	p, e := twoProd(a.hi, b.hi)
	e += float64(a.hi*b.lo) + float64(a.lo*b.hi)
	return quickTwoSum(p, e)
}

func ddDivD(a ddv, k float64) ddv {
	q1 := a.hi / k
	p, e := twoProd(q1, k)
	q2 := ((a.hi - p) - e + a.lo) / k
	return quickTwoSum(q1, q2)
}

// pi/2 as an unevaluated triple sum.
const (
	ddPio2a = 1.5707963267948966
	ddPio2b = 6.123233995736766e-17
	ddPio2c = -1.4973849048591698e-33
)

// ddSinCos returns sin and cos of the reduced argument's quadrant.
func ddSinCos(x float64) (float64, float64) {
	n := math.Round(x * 0.6366197723675814)
	p, pe := twoProd(n, ddPio2a)
	r := ddAdd(ddv{x, 0}, ddNeg(ddv{p, pe}))
	q, qe := twoProd(n, ddPio2b)
	r = ddAdd(r, ddNeg(ddv{q, qe}))
	r = ddAdd(r, ddv{-float64(n * ddPio2c), 0})
	r2 := ddMul(r, r)
	s, term := r, r
	for k := 1; k < 30; k++ {
		term = ddNeg(ddDivD(ddMul(term, r2), float64((2*k)*(2*k+1))))
		s = ddAdd(s, term)
		if math.Abs(term.hi) < 1e-40 {
			break
		}
	}
	c, term := ddv{1, 0}, ddv{1, 0}
	for k := 1; k < 30; k++ {
		term = ddNeg(ddDivD(ddMul(term, r2), float64((2*k-1)*(2*k))))
		c = ddAdd(c, term)
		if math.Abs(term.hi) < 1e-40 {
			break
		}
	}
	sn, cs := s.hi+s.lo, c.hi+c.lo
	switch int64(n) & 3 {
	case 0:
		return sn, cs
	case 1:
		return cs, -sn
	case 2:
		return -sn, -cs
	}
	return -cs, sn
}

// javaSin is Math.sin.
func javaSin(x float64) float64 {
	if math.Abs(x) > 1e6 || math.IsNaN(x) {
		return math.Sin(x)
	}
	s, _ := ddSinCos(x)
	return s
}

// javaCos is Math.cos.
func javaCos(x float64) float64 {
	if math.Abs(x) > 1e6 || math.IsNaN(x) {
		return math.Cos(x)
	}
	_, c := ddSinCos(x)
	return c
}
