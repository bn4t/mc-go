package worldgen

// posSet is a set of block positions that iterates in the order the
// game's hash set of positions does. Tree generation walks such sets —
// the leaf-distance pass pops their first element, decorators visit
// them in order — so the order is part of what gets generated.
//
// The set follows the Java hash map behind the game's sets: a position
// hashes as (y + z·31)·31 + x spread by its high half; buckets live in
// a power-of-two table of at least sixteen that doubles past three
// quarters full; a bucket is a list appended to in order, split in
// order on growth. A bucket reaching nine entries doubles a table under
// 64 instead, and otherwise becomes a red-black tree ordered by hash
// whose iteration order is its own linked order: built from the list,
// new entries go right after their tree parent, and the tree's root is
// moved to the front after every change. Buckets of six or fewer turn
// back into lists when split, and a tree too shallow to keep turns back
// into a list on removal through the set (not through its iterator,
// which leaves the tree as it is). Two distinct positions with the same hash in
// one tree would be ordered by identity in the game, which nothing can
// reproduce, so that case is reported as unsupported.
type posSet struct {
	table  []*psNode
	size   int
	thresh int
}

type psNode struct {
	p                   Point
	h                   int32
	next, prev          *psNode // bucket order (prev only kept in trees)
	parent, left, right *psNode
	red, tree           bool
}

const (
	psTreeify   = 8
	psUntreeify = 6
	psMinTree   = 64
)

func posHash(p Point) int32 {
	h := uint32((p.Y+p.Z*31)*31 + p.X)
	return int32(h ^ h>>16)
}

func (s *posSet) index(h int32) int { return int(uint32(h) & uint32(len(s.table)-1)) }

func (s *posSet) resize() {
	old := len(s.table)
	if old == 0 {
		s.table = make([]*psNode, 16)
		s.thresh = 12
		return
	}
	next := make([]*psNode, old*2)
	bit := uint32(old)
	for j, e := range s.table {
		if e == nil {
			continue
		}
		tree := e.tree
		var lo, loT, hi, hiT *psNode
		lc, hc := 0, 0
		for e != nil {
			n := e.next
			e.next = nil
			if uint32(e.h)&bit == 0 {
				e.prev = loT
				if loT == nil {
					lo = e
				} else {
					loT.next = e
				}
				loT = e
				lc++
			} else {
				e.prev = hiT
				if hiT == nil {
					hi = e
				} else {
					hiT.next = e
				}
				hiT = e
				hc++
			}
			e = n
		}
		next[j], next[j+old] = lo, hi
		if !tree {
			continue
		}
		// A tree bucket split in two keeps each half that stays large
		// as a tree, rebuilt unless the other half is empty.
		if lo != nil {
			if lc <= psUntreeify {
				untreeify(lo)
			} else if hi != nil {
				s.treeifyInto(next, j, lo)
			}
		}
		if hi != nil {
			if hc <= psUntreeify {
				untreeify(hi)
			} else if lo != nil {
				s.treeifyInto(next, j+old, hi)
			}
		}
	}
	s.table = next
	s.thresh *= 2
}

func untreeify(first *psNode) {
	for e := first; e != nil; e = e.next {
		e.tree, e.prev, e.parent, e.left, e.right, e.red = false, nil, nil, nil, nil, false
	}
}

func samePosHashTie() { unsupported("position set tree with equal hashes") }

// treeifyInto builds a tree over the bucket list at tab[i] in its order,
// then moves the root to the front.
func (s *posSet) treeifyInto(tab []*psNode, i int, first *psNode) {
	var root *psNode
	var prev *psNode
	for x := first; x != nil; x = x.next {
		x.tree, x.prev, x.left, x.right = true, prev, nil, nil
		prev = x
		if root == nil {
			x.parent, x.red, root = nil, false, x
			continue
		}
		for p := root; ; {
			var left bool
			switch {
			case p.h > x.h:
				left = true
			case p.h < x.h:
				left = false
			default:
				samePosHashTie()
			}
			xp := p
			if left {
				p = p.left
			} else {
				p = p.right
			}
			if p == nil {
				x.parent = xp
				if left {
					xp.left = x
				} else {
					xp.right = x
				}
				root = psBalanceInsert(root, x)
				break
			}
		}
	}
	psRootToFront(tab, i, root)
}

func psRootToFront(tab []*psNode, i int, root *psNode) {
	first := tab[i]
	if root == nil || root == first {
		return
	}
	tab[i] = root
	rp, rn := root.prev, root.next
	if rn != nil {
		rn.prev = rp
	}
	if rp != nil {
		rp.next = rn
	}
	if first != nil {
		first.prev = root
	}
	root.next, root.prev = first, nil
}

func psRotateLeft(root, p *psNode) *psNode {
	if p == nil || p.right == nil {
		return root
	}
	r := p.right
	p.right = r.left
	if p.right != nil {
		p.right.parent = p
	}
	r.parent = p.parent
	switch pp := p.parent; {
	case pp == nil:
		root = r
		r.red = false
	case pp.left == p:
		pp.left = r
	default:
		pp.right = r
	}
	r.left, p.parent = p, r
	return root
}

func psRotateRight(root, p *psNode) *psNode {
	if p == nil || p.left == nil {
		return root
	}
	l := p.left
	p.left = l.right
	if p.left != nil {
		p.left.parent = p
	}
	l.parent = p.parent
	switch pp := p.parent; {
	case pp == nil:
		root = l
		l.red = false
	case pp.right == p:
		pp.right = l
	default:
		pp.left = l
	}
	l.right, p.parent = p, l
	return root
}

func isRed(n *psNode) bool { return n != nil && n.red }

func psBalanceInsert(root, x *psNode) *psNode {
	x.red = true
	for {
		xp := x.parent
		if xp == nil {
			x.red = false
			return x
		}
		xpp := xp.parent
		if !xp.red || xpp == nil {
			return root
		}
		if xp == xpp.left {
			if xppr := xpp.right; isRed(xppr) {
				xppr.red, xp.red, xpp.red = false, false, true
				x = xpp
				continue
			}
			if x == xp.right {
				x = xp
				root = psRotateLeft(root, x)
				xp = x.parent
				xpp = nil
				if xp != nil {
					xpp = xp.parent
				}
			}
			if xp != nil {
				xp.red = false
				if xpp != nil {
					xpp.red = true
					root = psRotateRight(root, xpp)
				}
			}
		} else {
			if xppl := xpp.left; isRed(xppl) {
				xppl.red, xp.red, xpp.red = false, false, true
				x = xpp
				continue
			}
			if x == xp.left {
				x = xp
				root = psRotateRight(root, x)
				xp = x.parent
				xpp = nil
				if xp != nil {
					xpp = xp.parent
				}
			}
			if xp != nil {
				xp.red = false
				if xpp != nil {
					xpp.red = true
					root = psRotateLeft(root, xpp)
				}
			}
		}
	}
}

func psBalanceDelete(root, x *psNode) *psNode {
	for {
		if x == nil || x == root {
			return root
		}
		xp := x.parent
		if xp == nil {
			x.red = false
			return x
		}
		if x.red {
			x.red = false
			return root
		}
		if xp.left == x {
			xpr := xp.right
			if isRed(xpr) {
				xpr.red, xp.red = false, true
				root = psRotateLeft(root, xp)
				xp = x.parent
				xpr = nil
				if xp != nil {
					xpr = xp.right
				}
			}
			if xpr == nil {
				x = xp
				continue
			}
			sl, sr := xpr.left, xpr.right
			if !isRed(sr) && !isRed(sl) {
				xpr.red = true
				x = xp
				continue
			}
			if !isRed(sr) {
				if sl != nil {
					sl.red = false
				}
				xpr.red = true
				root = psRotateRight(root, xpr)
				xp = x.parent
				xpr = nil
				if xp != nil {
					xpr = xp.right
				}
			}
			if xpr != nil {
				xpr.red = xp != nil && xp.red
				if sr = xpr.right; sr != nil {
					sr.red = false
				}
			}
			if xp != nil {
				xp.red = false
				root = psRotateLeft(root, xp)
			}
			x = root
		} else {
			xpl := xp.left
			if isRed(xpl) {
				xpl.red, xp.red = false, true
				root = psRotateRight(root, xp)
				xp = x.parent
				xpl = nil
				if xp != nil {
					xpl = xp.left
				}
			}
			if xpl == nil {
				x = xp
				continue
			}
			sl, sr := xpl.left, xpl.right
			if !isRed(sl) && !isRed(sr) {
				xpl.red = true
				x = xp
				continue
			}
			if !isRed(sl) {
				if sr != nil {
					sr.red = false
				}
				xpl.red = true
				root = psRotateLeft(root, xpl)
				xp = x.parent
				xpl = nil
				if xp != nil {
					xpl = xp.left
				}
			}
			if xpl != nil {
				xpl.red = xp != nil && xp.red
				if sl = xpl.left; sl != nil {
					sl.red = false
				}
			}
			if xp != nil {
				xp.red = false
				root = psRotateRight(root, xp)
			}
			x = root
		}
	}
}

func (n *psNode) root() *psNode {
	for n.parent != nil {
		n = n.parent
	}
	return n
}

// findTree finds p in the tree holding first.
func findTree(first *psNode, p Point, h int32) *psNode {
	for n := first.root(); n != nil; {
		switch {
		case n.h > h:
			n = n.left
		case n.h < h:
			n = n.right
		case n.p == p:
			return n
		default:
			samePosHashTie()
			return nil
		}
	}
	return nil
}

func (s *posSet) find(p Point) *psNode {
	if s.table == nil {
		return nil
	}
	h := posHash(p)
	first := s.table[s.index(h)]
	if first == nil {
		return nil
	}
	if first.h == h && first.p == p {
		return first
	}
	if first.tree {
		return findTree(first, p, h)
	}
	for e := first.next; e != nil; e = e.next {
		if e.h == h && e.p == p {
			return e
		}
	}
	return nil
}

func (s *posSet) contains(p Point) bool { return s.find(p) != nil }

func (s *posSet) add(p Point) bool {
	if s.table == nil {
		s.resize()
	}
	h := posHash(p)
	i := s.index(h)
	first := s.table[i]
	switch {
	case first == nil:
		s.table[i] = &psNode{p: p, h: h}
	case first.h == h && first.p == p:
		return false
	case first.tree:
		if !s.putTree(i, first, p, h) {
			return false
		}
	default:
		e, count := first, 1
		for ; e.next != nil; e = e.next {
			if e.next.h == h && e.next.p == p {
				return false
			}
			count++
		}
		e.next = &psNode{p: p, h: h}
		if count >= psTreeify {
			if len(s.table) < psMinTree {
				s.resize()
			} else {
				s.treeifyInto(s.table, i, s.table[i])
			}
		}
	}
	s.size++
	if s.size > s.thresh {
		s.resize()
	}
	return true
}

func (s *posSet) putTree(i int, first *psNode, p Point, h int32) bool {
	root := first.root()
	for n := root; ; {
		var left bool
		switch {
		case n.h > h:
			left = true
		case n.h < h:
			left = false
		case n.p == p:
			return false
		default:
			samePosHashTie()
		}
		xp := n
		if left {
			n = n.left
		} else {
			n = n.right
		}
		if n != nil {
			continue
		}
		xpn := xp.next
		x := &psNode{p: p, h: h, next: xpn, tree: true}
		if left {
			xp.left = x
		} else {
			xp.right = x
		}
		xp.next = x
		x.parent, x.prev = xp, xp
		if xpn != nil {
			xpn.prev = x
		}
		psRootToFront(s.table, i, psBalanceInsert(root, x))
		return true
	}
}

// remove is the set's own removal: a tree bucket may turn back into a
// list or move its new root to the front.
func (s *posSet) remove(p Point) { s.delete(p, true) }

// pop removes the first position as the set's iterator does, which
// leaves a tree bucket's shape and order otherwise alone.
func (s *posSet) pop() (Point, bool) {
	p, ok := s.first()
	if ok {
		s.delete(p, false)
	}
	return p, ok
}

func (s *posSet) delete(p Point, movable bool) {
	if s.table == nil {
		return
	}
	h := posHash(p)
	i := s.index(h)
	first := s.table[i]
	if first == nil {
		return
	}
	if first.tree {
		if node := s.find(p); node != nil {
			s.removeTree(i, node, movable)
			s.size--
		}
		return
	}
	var prev *psNode
	for e := first; e != nil; prev, e = e, e.next {
		if e.h == h && e.p == p {
			if prev == nil {
				s.table[i] = e.next
			} else {
				prev.next = e.next
			}
			s.size--
			return
		}
	}
}

func (s *posSet) removeTree(i int, p *psNode, movable bool) {
	first := s.table[i]
	root := first
	succ, pred := p.next, p.prev
	if pred == nil {
		s.table[i] = succ
		first = succ
	} else {
		pred.next = succ
	}
	if succ != nil {
		succ.prev = pred
	}
	if first == nil {
		return
	}
	if root.parent != nil {
		root = root.root()
	}
	if root == nil || movable && (root.right == nil || root.left == nil || root.left.left == nil) {
		untreeify(first)
		return
	}
	pl, pr := p.left, p.right
	var replacement *psNode
	switch {
	case pl != nil && pr != nil:
		sn := pr
		for sn.left != nil {
			sn = sn.left
		}
		sn.red, p.red = p.red, sn.red
		sr := sn.right
		pp := p.parent
		if sn == pr {
			p.parent = sn
			sn.right = p
		} else {
			sp := sn.parent
			p.parent = sp
			if sp != nil {
				if sn == sp.left {
					sp.left = p
				} else {
					sp.right = p
				}
			}
			sn.right = pr
			pr.parent = sn
		}
		p.left = nil
		p.right = sr
		if sr != nil {
			sr.parent = p
		}
		sn.left = pl
		pl.parent = sn
		sn.parent = pp
		switch {
		case pp == nil:
			root = sn
		case p == pp.left:
			pp.left = sn
		default:
			pp.right = sn
		}
		if sr != nil {
			replacement = sr
		} else {
			replacement = p
		}
	case pl != nil:
		replacement = pl
	case pr != nil:
		replacement = pr
	default:
		replacement = p
	}
	if replacement != p {
		pp := p.parent
		replacement.parent = pp
		switch {
		case pp == nil:
			root = replacement
			replacement.red = false
		case p == pp.left:
			pp.left = replacement
		default:
			pp.right = replacement
		}
		p.left, p.right, p.parent = nil, nil, nil
	}
	r := root
	if !p.red {
		r = psBalanceDelete(root, replacement)
	}
	if replacement == p {
		pp := p.parent
		p.parent = nil
		if pp != nil {
			if p == pp.left {
				pp.left = nil
			} else if p == pp.right {
				pp.right = nil
			}
		}
	}
	if movable {
		psRootToFront(s.table, i, r)
	}
}

// first is the set's first position in iteration order.
func (s *posSet) first() (Point, bool) {
	for _, b := range s.table {
		if b != nil {
			return b.p, true
		}
	}
	return Point{}, false
}

func (s *posSet) each(f func(Point)) {
	for _, b := range s.table {
		for e := b; e != nil; e = e.next {
			f(e.p)
		}
	}
}

// list is the set in iteration order.
func (s *posSet) list() []Point {
	out := make([]Point, 0, s.size)
	s.each(func(p Point) { out = append(out, p) })
	return out
}
