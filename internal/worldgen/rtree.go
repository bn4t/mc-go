package worldgen

import (
	"math"
	"sort"
)

// rtree is Climate.RTree: the parameter-space search tree vanilla builds
// over a biome parameter list. Its construction order decides which of
// several equally fit biomes a lookup returns, so it is rebuilt exactly:
// stable sorts on cell centers, buckets of the largest power of six
// below the child count, and the split axis of least summed extent.
type rtree struct{ root *rnode }

type rnode struct {
	space    [7]climParam
	children []*rnode
	leaf     *biomeEntry
}

const rtreeDims = 7

func newRTree(entries []biomeEntry) *rtree {
	if len(entries) == 0 {
		return nil
	}
	leaves := make([]*rnode, len(entries))
	for i := range entries {
		e := &entries[i]
		n := &rnode{leaf: e}
		copy(n.space[:6], e.params[:])
		n.space[6] = climParam{e.offset, e.offset}
		leaves[i] = n
	}
	return &rtree{root: rtreeBuild(leaves)}
}

func center(p climParam) int64 { return (p.min + p.max) / 2 }

func absI64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func rtreeBuild(children []*rnode) *rnode {
	if len(children) == 1 {
		return children[0]
	}
	if len(children) <= 6 {
		sort.SliceStable(children, func(i, j int) bool {
			return centerSum(children[i]) < centerSum(children[j])
		})
		return newSubTree(children)
	}
	bestCost := int64(math.MaxInt64)
	bestDim := -1
	var best []*rnode
	for d := 0; d < rtreeDims; d++ {
		rtreeSort(children, d, false)
		buckets := bucketize(children)
		var cost int64
		for _, b := range buckets {
			cost += spaceCost(&b.space)
		}
		if bestCost > cost {
			bestCost, bestDim, best = cost, d, buckets
		}
	}
	rtreeSort(best, bestDim, true)
	kids := make([]*rnode, len(best))
	for i, b := range best {
		kids[i] = rtreeBuild(append([]*rnode(nil), b.children...))
	}
	return newSubTree(kids)
}

func centerSum(n *rnode) int64 {
	var s int64
	for d := 0; d < rtreeDims; d++ {
		s += absI64(center(n.space[d]))
	}
	return s
}

func rtreeSort(nodes []*rnode, dim int, abs bool) {
	key := func(n *rnode, d int) int64 {
		c := center(n.space[d])
		if abs {
			return absI64(c)
		}
		return c
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		for k := 0; k < rtreeDims; k++ {
			d := (dim + k) % rtreeDims
			a, b := key(nodes[i], d), key(nodes[j], d)
			if a != b {
				return a < b
			}
		}
		return false
	})
}

func bucketize(nodes []*rnode) []*rnode {
	size := int(math.Pow(6, math.Floor(math.Log(float64(len(nodes))-0.01)/math.Log(6))))
	var out []*rnode
	var cur []*rnode
	for _, n := range nodes {
		cur = append(cur, n)
		if len(cur) >= size {
			out = append(out, newSubTree(cur))
			cur = nil
		}
	}
	if len(cur) > 0 {
		out = append(out, newSubTree(cur))
	}
	return out
}

func newSubTree(children []*rnode) *rnode {
	n := &rnode{children: children}
	for d := 0; d < rtreeDims; d++ {
		p := children[0].space[d]
		for _, c := range children[1:] {
			p.min = min(p.min, c.space[d].min)
			p.max = max(p.max, c.space[d].max)
		}
		n.space[d] = p
	}
	return n
}

func spaceCost(s *[7]climParam) int64 {
	var c int64
	for _, p := range s {
		c += absI64(p.max - p.min)
	}
	return c
}

func (n *rnode) distance(t *[7]int64) int64 {
	var s int64
	for d := 0; d < rtreeDims; d++ {
		v := n.space[d].distance(t[d])
		s += v * v
	}
	return s
}

func (n *rnode) search(t *[7]int64, cand *rnode) *rnode {
	if n.leaf != nil {
		return n
	}
	best := int64(math.MaxInt64)
	if cand != nil {
		best = cand.distance(t)
	}
	res := cand
	for _, c := range n.children {
		m := c.distance(t)
		if best > m {
			r := c.search(t, res)
			d := m
			if r != c {
				d = r.distance(t)
			}
			if best > d {
				best, res = d, r
			}
		}
	}
	return res
}

func (tr *rtree) search(t *[climAxes]int64) *biomeEntry {
	return tr.searchFrom(t, nil).leaf
}

// searchFrom starts from a previous result, as vanilla's per-thread
// lastResult does: on an exact fitness tie the previous biome wins.
func (tr *rtree) searchFrom(t *[climAxes]int64, last *rnode) *rnode {
	var full [7]int64
	copy(full[:6], t[:])
	return tr.root.search(&full, last)
}
