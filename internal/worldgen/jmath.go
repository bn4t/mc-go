package worldgen

import "math"

// jsin and jcos stand in for java.lang.Math.sin and cos. The game's
// server runs them as HotSpot's x86_64 intrinsics, whose results are the
// correctly rounded ones for all but about one input in a thousand
// (measured against a 90-digit evaluation; fdlibm, which StrictMath and
// Go's math follow more closely, misses about one in thirty). Features
// test signed distances that land exactly on zero, so the last bit
// matters: these reduce the argument and sum the series in double-double
// and round once.

// ddouble is an unevaluated sum hi+lo with |lo| at most half an ulp of hi.
type ddouble struct{ hi, lo float64 }

func twoSum(a, b float64) ddouble {
	s := a + b
	bb := s - a
	return ddouble{s, (a - (s - bb)) + (b - bb)}
}

func quickTwoSum(a, b float64) ddouble {
	s := a + b
	return ddouble{s, b - (s - a)}
}

func twoProd(a, b float64) ddouble {
	p := a * b
	return ddouble{p, math.FMA(a, b, -p)}
}

func (a ddouble) add(b ddouble) ddouble {
	s, t := twoSum(a.hi, b.hi), twoSum(a.lo, b.lo)
	s = quickTwoSum(s.hi, s.lo+t.hi)
	return quickTwoSum(s.hi, s.lo+t.lo)
}

func (a ddouble) mul(b ddouble) ddouble {
	p := twoProd(a.hi, b.hi)
	return quickTwoSum(p.hi, p.lo+(a.hi*b.lo+a.lo*b.hi))
}

func (a ddouble) divInt(n float64) ddouble {
	q := a.hi / n
	r := ddouble{a.hi, a.lo}.add(twoProd(q, -n))
	return quickTwoSum(q, r.hi/n)
}

// halfPi is π/2 split in four doubles.
var halfPi = [4]float64{
	1.5707963267948966, 6.123233995736766e-17, -1.4973849048591698e-33, 5.562271104316826e-50,
}

// invFact[n] is 1/n! in double-double.
var invFact = func() (f [32]ddouble) {
	f[0] = ddouble{1, 0}
	for n := 1; n < len(f); n++ {
		f[n] = f[n-1].divInt(float64(n))
	}
	return
}()

// jmathLimit bounds the arguments reduced here; others are Go's.
const jmathLimit = 1 << 20

// reduceHalfPi returns x - k·π/2 in double-double and k mod 4.
func reduceHalfPi(x float64) (ddouble, int) {
	k := math.RoundToEven(x * (2 / math.Pi))
	r := ddouble{x, 0}
	for _, p := range halfPi {
		r = r.add(twoProd(-k, p))
	}
	return r, int(int64(k) & 3)
}

// sinSeries and cosSeries sum the Taylor series for |r| ≤ π/4.
func sinSeries(r ddouble) ddouble {
	r2 := r.mul(r)
	s := invFact[29]
	for n := 27; n >= 1; n -= 2 {
		t := s.mul(r2)
		s = ddouble{-t.hi, -t.lo}.add(invFact[n])
	}
	return s.mul(r)
}

func cosSeries(r ddouble) ddouble {
	r2 := r.mul(r)
	s := invFact[30]
	for n := 28; n >= 0; n -= 2 {
		t := s.mul(r2)
		s = ddouble{-t.hi, -t.lo}.add(invFact[n])
	}
	return s
}

func jsin(x float64) float64 {
	if !(math.Abs(x) < jmathLimit) {
		return math.Sin(x)
	}
	if x == 0 {
		return x
	}
	r, q := reduceHalfPi(x)
	var v ddouble
	switch q {
	case 0:
		v = sinSeries(r)
	case 1:
		v = cosSeries(r)
	case 2:
		v = sinSeries(r)
		v = ddouble{-v.hi, -v.lo}
	default:
		v = cosSeries(r)
		v = ddouble{-v.hi, -v.lo}
	}
	return v.hi + v.lo
}

func jcos(x float64) float64 {
	if !(math.Abs(x) < jmathLimit) {
		return math.Cos(x)
	}
	r, q := reduceHalfPi(x)
	var v ddouble
	switch q {
	case 0:
		v = cosSeries(r)
	case 1:
		v = sinSeries(r)
		v = ddouble{-v.hi, -v.lo}
	case 2:
		v = cosSeries(r)
		v = ddouble{-v.hi, -v.lo}
	default:
		v = sinSeries(r)
	}
	return v.hi + v.lo
}
