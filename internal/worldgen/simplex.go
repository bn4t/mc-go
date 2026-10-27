package worldgen

import "math"

// Simplex is 2D simplex noise over a shuffled permutation with a random
// origin, as the legacy temperature and frozen-ocean noises use it.
type Simplex struct {
	perm       [256]int32
	Xo, Yo, Zo float64
}

var (
	sqrt3      = math.Sqrt(3)
	simplexF2  = 0.5 * (sqrt3 - 1)
	simplexG2  = (3 - sqrt3) / 6
	simplexG22 = 2 * simplexG2
)

// NewSimplex draws the origin and shuffles the permutation the same way
// improved noise does.
func NewSimplex(r Random) *Simplex {
	n := &Simplex{}
	n.Xo = r.NextDouble() * 256
	n.Yo = r.NextDouble() * 256
	n.Zo = r.NextDouble() * 256
	for i := range n.perm {
		n.perm[i] = int32(i)
	}
	for i := 0; i < 256; i++ {
		j := i + int(r.NextIntn(int32(256-i)))
		n.perm[i], n.perm[j] = n.perm[j], n.perm[i]
	}
	return n
}

func (n *Simplex) p(i int32) int32 { return n.perm[i&255] }

func simplexCorner(g int32, x, y float64) float64 {
	t := 0.5 - float64(x*x) - float64(y*y)
	if t < 0 {
		return 0
	}
	t *= t
	gr := &gradients[g]
	return t * t * (float64(gr[0]*x) + float64(gr[1]*y))
}

// Value samples the 2D noise.
func (n *Simplex) Value(x, y float64) float64 {
	s := (x + y) * simplexF2
	i, j := floorInt(x+s), floorInt(y+s)
	t := float64(float64(i+j) * simplexG2)
	x0 := x - (float64(i) - t)
	y0 := y - (float64(j) - t)
	var i1, j1 int32
	if x0 > y0 {
		i1 = 1
	} else {
		j1 = 1
	}
	x1 := x0 - float64(i1) + simplexG2
	y1 := y0 - float64(j1) + simplexG2
	x2 := x0 - 1 + simplexG22
	y2 := y0 - 1 + simplexG22
	ii, jj := i&255, j&255
	g0 := n.p(ii+n.p(jj)) % 12
	g1 := n.p(ii+i1+n.p(jj+j1)) % 12
	g2 := n.p(ii+1+n.p(jj+1)) % 12
	return 70 * (simplexCorner(g0, x0, y0) + simplexCorner(g1, x1, y1) + simplexCorner(g2, x2, y2))
}

// PerlinSimplex sums simplex octaves, highest frequency first, with the
// legacy sequential seeding. Only octave sets whose highest octave is 0
// are supported — all the vanilla uses.
type PerlinSimplex struct {
	levels      []*Simplex
	inputFactor float64
	valueFactor float64
}

// NewPerlinSimplex builds the octaves (all <= 0, including 0) from r.
func NewPerlinSimplex(r Random, octaves []int) *PerlinSimplex {
	lowest, highest := octaves[0], octaves[0]
	set := map[int]bool{}
	for _, o := range octaves {
		lowest, highest = min(lowest, o), max(highest, o)
		set[o] = true
	}
	if highest != 0 || !set[0] {
		panic("worldgen: perlin simplex octaves must top out at 0")
	}
	n := -lowest + highest + 1
	ps := &PerlinSimplex{levels: make([]*Simplex, n)}
	ps.levels[0] = NewSimplex(r)
	for i := 1; i < n; i++ {
		if set[-i] {
			ps.levels[i] = NewSimplex(r)
		} else {
			r.ConsumeCount(legacyOctaveSkip)
		}
	}
	ps.inputFactor = 1
	ps.valueFactor = 1 / (math.Pow(2, float64(n)) - 1)
	return ps
}

// Value samples the octaves at (x, y), without the origin offsets.
func (ps *PerlinSimplex) Value(x, y float64) float64 {
	var sum float64
	f, vf := ps.inputFactor, ps.valueFactor
	for _, l := range ps.levels {
		if l != nil {
			sum += float64(l.Value(float64(x*f), float64(y*f)) * vf)
		}
		f /= 2
		vf *= 2
	}
	return sum
}
