package worldgen

import (
	"math"
	"sort"
)

// The climate search tree. Vanilla answers biome lookups from an R-tree
// of up to six children per node, and seeds each search with the
// previous answer: a candidate is only replaced by a strictly nearer
// one, so exact distance ties go to the earlier answer, then to the
// first leaf in tree order. Ties are common at shared range edges, so
// the tree's shape is part of the observable behavior.

const rtreeDims = 7

// rspan is a node's parameter space: per axis [min, max], the seventh
// axis the offset.
type rspan [rtreeDims][2]int64

type rnode struct {
	space    rspan
	children []*rnode // nil for a leaf
	biome    uint8
}

func (n *rnode) distance(target *[rtreeDims]int64) int64 {
	var d int64
	for i := 0; i < rtreeDims; i++ {
		g := axisGap(target[i], n.space[i][0], n.space[i][1])
		d += g * g
	}
	return d
}

func newSubtree(children []*rnode) *rnode {
	n := &rnode{children: append([]*rnode(nil), children...)}
	for i := 0; i < rtreeDims; i++ {
		n.space[i] = children[0].space[i]
		for _, c := range children[1:] {
			n.space[i][0] = min(n.space[i][0], c.space[i][0])
			n.space[i][1] = max(n.space[i][1], c.space[i][1])
		}
	}
	return n
}

func axisMid(n *rnode, axis int, abs bool) int64 {
	m := (n.space[axis][0] + n.space[axis][1]) / 2
	if abs && m < 0 {
		m = -m
	}
	return m
}

// sortByAxis orders nodes by their midpoints on axis, then on each
// following axis in turn (wrapping), keeping equal nodes in order.
func sortByAxis(nodes []*rnode, axis int, abs bool) {
	sort.SliceStable(nodes, func(i, j int) bool {
		for k := 0; k < rtreeDims; k++ {
			a := (axis + k) % rtreeDims
			if x, y := axisMid(nodes[i], a, abs), axisMid(nodes[j], a, abs); x != y {
				return x < y
			}
		}
		return false
	})
}

// bucketize cuts sorted nodes into runs of the largest power of six
// below their count.
func bucketize(nodes []*rnode) []*rnode {
	size := int(math.Pow(6, math.Floor(math.Log(float64(len(nodes))-0.01)/math.Log(6))))
	var out []*rnode
	var cur []*rnode
	for _, n := range nodes {
		cur = append(cur, n)
		if len(cur) >= size {
			out = append(out, newSubtree(cur))
			cur = nil
		}
	}
	if len(cur) > 0 {
		out = append(out, newSubtree(cur))
	}
	return out
}

func spanCost(s *rspan) int64 {
	var c int64
	for i := 0; i < rtreeDims; i++ {
		d := s[i][1] - s[i][0]
		if d < 0 {
			d = -d
		}
		c += d
	}
	return c
}

// buildRTree builds a node over nodes: one node as is; up to six as a
// node ordered by their total distance from the origin; more, split
// along the axis whose bucketing gives the tightest buckets.
func buildRTree(nodes []*rnode) *rnode {
	if len(nodes) == 1 {
		return nodes[0]
	}
	if len(nodes) <= 6 {
		sorted := append([]*rnode(nil), nodes...)
		key := func(n *rnode) int64 {
			var s int64
			for i := 0; i < rtreeDims; i++ {
				s += axisMid(n, i, true)
			}
			return s
		}
		sort.SliceStable(sorted, func(i, j int) bool { return key(sorted[i]) < key(sorted[j]) })
		return newSubtree(sorted)
	}
	best := int64(math.MaxInt64)
	var bestBuckets []*rnode
	bestAxis := -1
	for axis := 0; axis < rtreeDims; axis++ {
		sortByAxis(nodes, axis, false)
		buckets := bucketize(nodes)
		var cost int64
		for _, b := range buckets {
			cost += spanCost(&b.space)
		}
		if best > cost {
			best, bestAxis, bestBuckets = cost, axis, buckets
		}
	}
	sortByAxis(bestBuckets, bestAxis, true)
	children := make([]*rnode, len(bestBuckets))
	for i, b := range bestBuckets {
		children[i] = buildRTree(b.children)
	}
	return newSubtree(children)
}

func (n *rnode) search(target *[rtreeDims]int64, candidate *rnode) *rnode {
	if n.children == nil {
		return n
	}
	best := int64(math.MaxInt64)
	if candidate != nil {
		best = candidate.distance(target)
	}
	result := candidate
	for _, c := range n.children {
		d := c.distance(target)
		if best <= d {
			continue
		}
		leaf := c.search(target, result)
		if leaf != c {
			d = leaf.distance(target)
		}
		if best > d {
			best, result = d, leaf
		}
	}
	return result
}

func (m *MultiNoiseBiomes) tree() *rnode {
	m.treeOnce.Do(func() {
		leaves := make([]*rnode, len(m.points))
		for i := range m.points {
			p := &m.points[i]
			l := &rnode{biome: p.biome}
			for a := 0; a < 6; a++ {
				l.space[a] = [2]int64{p.lo[a], p.hi[a]}
			}
			l.space[6] = [2]int64{p.offset, p.offset}
			leaves[i] = l
		}
		m.root = buildRTree(leaves)
	})
	return m.root
}

// BiomeSearcher answers climate lookups the way one vanilla worker
// thread does, remembering its previous answer. It is not safe for
// concurrent use; give each worker its own.
type BiomeSearcher struct {
	m    *MultiNoiseBiomes
	last *rnode
}

// NewSearcher starts a searcher with no previous answer.
func (m *MultiNoiseBiomes) NewSearcher() *BiomeSearcher { return &BiomeSearcher{m: m} }

// Find returns the biome for a climate sample.
func (s *BiomeSearcher) Find(t ClimateTarget) string {
	target := [rtreeDims]int64{t[0], t[1], t[2], t[3], t[4], t[5], 0}
	leaf := s.m.tree().search(&target, s.last)
	s.last = leaf
	return climateBiomeNames[leaf.biome]
}
