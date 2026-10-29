package worldgen

import "sync/atomic"

// jset reproduces java.util.HashSet<BlockPos> iteration order. Tree
// generation keeps its log, leaf and root positions (and the leaf
// distance layers) in HashSets, and both the decorator lists
// (TreeDecorator.Context copies them before a stable sort by y) and the
// leaf-distance pass (TreeFeature.updateLeaves pops iterator().next())
// depend on that order.
//
// The model follows java.util.HashMap's documented structure: keys
// hash as BlockPos.hashCode ((y + z*31)*31 + x) spread by h ^ (h >>>
// 16); the table starts at 16 buckets and doubles when the size passes
// 3/4 of it; list bins append at the tail; a bin that reaches 9 nodes
// resizes the table below 64 buckets and otherwise becomes a red-black
// tree bin ordered by hash, whose root is moved to the bin's front and
// whose new nodes are linked right after their tree parent; a resize
// splits every bin in order (tree halves of 6 or fewer nodes go back to
// lists); iterator removal unlinks without moving the root. Equal hashes
// inside a tree bin fall back to an identity-hash tie break in Java,
// which is not reproducible (counted in JSetTieBreaks). The model is
// pinned against the JDK by TestJSetMatchesJava.
type jset struct {
	tab  []*jnode
	has  map[BlockPos]*jnode
	n    int
	hint int // no bucket below hint is non-empty
}

type jnode struct {
	p                   BlockPos
	h                   int32
	next, prev          *jnode
	parent, left, right *jnode
	red, tree           bool
}

// JSetTieBreaks counts equal-hash comparisons inside tree bins (Java
// resolves them by identity hash code).
var JSetTieBreaks atomic.Int64

func newJSet() *jset { return &jset{has: map[BlockPos]*jnode{}} }

func jhash(p BlockPos) int32 {
	h := uint32((p[1]+p[2]*31)*31 + p[0])
	return int32(h ^ h>>16)
}

func (s *jset) contains(p BlockPos) bool { return s.has[p] != nil }

func (s *jset) idx(h int32) int { return int(uint32(h) & uint32(len(s.tab)-1)) }

func (s *jset) add(p BlockPos) bool {
	if s.has[p] != nil {
		return false
	}
	if s.tab == nil {
		s.resize()
	}
	x := &jnode{p: p, h: jhash(p)}
	s.has[p] = x
	i := s.idx(x.h)
	if i < s.hint {
		s.hint = i
	}
	switch f := s.tab[i]; {
	case f == nil:
		s.tab[i] = x
	case f.tree:
		s.putTreeVal(f, x)
	default:
		bin := 0
		e := f
		for e.next != nil {
			e = e.next
			bin++
		}
		e.next = x
		if bin >= 7 {
			s.treeifyBin(i)
		}
	}
	s.n++
	if s.n > len(s.tab)*3/4 {
		s.resize()
	}
	return true
}

func (s *jset) resize() {
	old := s.tab
	if old == nil {
		s.tab = make([]*jnode, 16)
		return
	}
	bit := len(old)
	nt := make([]*jnode, bit*2)
	s.tab = nt
	s.hint = 0
	for j, e := range old {
		if e == nil {
			continue
		}
		if e.next == nil {
			nt[int(uint32(e.h)&uint32(len(nt)-1))] = e
			continue
		}
		var loH, loT, hiH, hiT *jnode
		lc, hc := 0, 0
		tree := e.tree
		for x := e; x != nil; {
			nx := x.next
			x.next = nil
			if uint32(x.h)&uint32(bit) == 0 {
				if tree {
					x.prev = loT
				}
				if loT == nil {
					loH = x
				} else {
					loT.next = x
				}
				loT = x
				lc++
			} else {
				if tree {
					x.prev = hiT
				}
				if hiT == nil {
					hiH = x
				} else {
					hiT.next = x
				}
				hiT = x
				hc++
			}
			x = nx
		}
		if !tree {
			nt[j], nt[j+bit] = loH, hiH
			continue
		}
		if loH != nil {
			if lc <= 6 {
				nt[j] = untreeify(loH)
			} else {
				nt[j] = loH
				if hiH != nil {
					s.treeify(loH)
				}
			}
		}
		if hiH != nil {
			if hc <= 6 {
				nt[j+bit] = untreeify(hiH)
			} else {
				nt[j+bit] = hiH
				if loH != nil {
					s.treeify(hiH)
				}
			}
		}
	}
}

func untreeify(h *jnode) *jnode {
	for x := h; x != nil; x = x.next {
		x.tree, x.prev, x.parent, x.left, x.right, x.red = false, nil, nil, nil, nil, false
	}
	return h
}

func (s *jset) treeifyBin(i int) {
	if len(s.tab) < 64 {
		s.resize()
		return
	}
	var prev *jnode
	for x := s.tab[i]; x != nil; x = x.next {
		x.tree = true
		x.prev = prev
		prev = x
	}
	s.treeify(s.tab[i])
}

func tieBreak() int {
	JSetTieBreaks.Add(1)
	return -1
}

func (s *jset) treeify(hd *jnode) {
	var root *jnode
	for x := hd; x != nil; x = x.next {
		x.left, x.right = nil, nil
		if root == nil {
			x.parent = nil
			x.red = false
			root = x
			continue
		}
		for p := root; ; {
			var dir int
			switch {
			case p.h > x.h:
				dir = -1
			case p.h < x.h:
				dir = 1
			default:
				dir = tieBreak()
			}
			xp := p
			if dir <= 0 {
				p = p.left
			} else {
				p = p.right
			}
			if p == nil {
				x.parent = xp
				if dir <= 0 {
					xp.left = x
				} else {
					xp.right = x
				}
				root = balanceInsertion(root, x)
				break
			}
		}
	}
	s.moveRootToFront(root)
}

func (s *jset) moveRootToFront(root *jnode) {
	i := s.idx(root.h)
	first := s.tab[i]
	if root == first {
		return
	}
	rn, rp := root.next, root.prev
	if rn != nil {
		rn.prev = rp
	}
	if rp != nil {
		rp.next = rn
	}
	if first != nil {
		first.prev = root
	}
	root.next = first
	root.prev = nil
	s.tab[i] = root
}

func treeRoot(x *jnode) *jnode {
	for x.parent != nil {
		x = x.parent
	}
	return x
}

func (s *jset) putTreeVal(first, x *jnode) {
	x.tree = true
	root := first
	if root.parent != nil {
		root = treeRoot(root)
	}
	for p := root; ; {
		var dir int
		switch {
		case p.h > x.h:
			dir = -1
		case p.h < x.h:
			dir = 1
		default:
			dir = tieBreak()
		}
		xp := p
		if dir <= 0 {
			p = p.left
		} else {
			p = p.right
		}
		if p == nil {
			xpn := xp.next
			x.next = xpn
			if dir <= 0 {
				xp.left = x
			} else {
				xp.right = x
			}
			xp.next = x
			x.parent, x.prev = xp, xp
			if xpn != nil {
				xpn.prev = x
			}
			s.moveRootToFront(balanceInsertion(root, x))
			return
		}
	}
}

func rotateLeft(root, p *jnode) *jnode {
	if p != nil && p.right != nil {
		r := p.right
		p.right = r.left
		if p.right != nil {
			p.right.parent = p
		}
		r.parent = p.parent
		if pp := r.parent; pp == nil {
			root = r
			r.red = false
		} else if pp.left == p {
			pp.left = r
		} else {
			pp.right = r
		}
		r.left = p
		p.parent = r
	}
	return root
}

func rotateRight(root, p *jnode) *jnode {
	if p != nil && p.left != nil {
		l := p.left
		p.left = l.right
		if p.left != nil {
			p.left.parent = p
		}
		l.parent = p.parent
		if pp := l.parent; pp == nil {
			root = l
			l.red = false
		} else if pp.right == p {
			pp.right = l
		} else {
			pp.left = l
		}
		l.right = p
		p.parent = l
	}
	return root
}

func isRed(x *jnode) bool { return x != nil && x.red }

func balanceInsertion(root, x *jnode) *jnode {
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
		if xppl := xpp.left; xp == xppl {
			if xppr := xpp.right; xppr != nil && xppr.red {
				xppr.red = false
				xp.red = false
				xpp.red = true
				x = xpp
			} else {
				if x == xp.right {
					x = xp
					root = rotateLeft(root, x)
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
						root = rotateRight(root, xpp)
					}
				}
			}
		} else {
			if xppl != nil && xppl.red {
				xppl.red = false
				xp.red = false
				xpp.red = true
				x = xpp
			} else {
				if x == xp.left {
					x = xp
					root = rotateRight(root, x)
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
						root = rotateLeft(root, xpp)
					}
				}
			}
		}
	}
}

func balanceDeletion(root, x *jnode) *jnode {
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
		if xpl := xp.left; xpl == x {
			xpr := xp.right
			if xpr != nil && xpr.red {
				xpr.red = false
				xp.red = true
				root = rotateLeft(root, xp)
				xp = x.parent
				xpr = nil
				if xp != nil {
					xpr = xp.right
				}
			}
			if xpr == nil {
				x = xp
			} else {
				sl, sr := xpr.left, xpr.right
				if !isRed(sr) && !isRed(sl) {
					xpr.red = true
					x = xp
				} else {
					if !isRed(sr) {
						if sl != nil {
							sl.red = false
						}
						xpr.red = true
						root = rotateRight(root, xpr)
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
						root = rotateLeft(root, xp)
					}
					x = root
				}
			}
		} else {
			if xpl != nil && xpl.red {
				xpl.red = false
				xp.red = true
				root = rotateRight(root, xp)
				xp = x.parent
				xpl = nil
				if xp != nil {
					xpl = xp.left
				}
			}
			if xpl == nil {
				x = xp
			} else {
				sl, sr := xpl.left, xpl.right
				if !isRed(sl) && !isRed(sr) {
					xpl.red = true
					x = xp
				} else {
					if !isRed(sl) {
						if sr != nil {
							sr.red = false
						}
						xpl.red = true
						root = rotateLeft(root, xpl)
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
						root = rotateRight(root, xp)
					}
					x = root
				}
			}
		}
	}
}

// removeTreeNode is TreeNode.removeTreeNode; the iterator's remove
// passes movable=false, Set.remove movable=true (which also turns a
// sparse tree back into a list and moves the root to the front).
func (s *jset) removeTreeNode(p *jnode, movable bool) {
	i := s.idx(p.h)
	first := s.tab[i]
	root := first
	succ, pred := p.next, p.prev
	if pred == nil {
		s.tab[i] = succ
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
		root = treeRoot(root)
	}
	if movable && (root.right == nil || root.left == nil || root.left.left == nil) {
		s.tab[i] = untreeify(first)
		return
	}
	pl, pr := p.left, p.right
	var replacement *jnode
	if pl != nil && pr != nil {
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
			if pr != nil {
				pr.parent = sn
			}
		}
		p.left = nil
		p.right = sr
		if sr != nil {
			sr.parent = p
		}
		sn.left = pl
		if pl != nil {
			pl.parent = sn
		}
		sn.parent = pp
		if pp == nil {
			root = sn
		} else if p == pp.left {
			pp.left = sn
		} else {
			pp.right = sn
		}
		if sr != nil {
			replacement = sr
		} else {
			replacement = p
		}
	} else if pl != nil {
		replacement = pl
	} else if pr != nil {
		replacement = pr
	} else {
		replacement = p
	}
	if replacement != p {
		pp := p.parent
		replacement.parent = pp
		if pp == nil {
			root = replacement
			replacement.red = false
		} else if p == pp.left {
			pp.left = replacement
		} else {
			pp.right = replacement
		}
		p.left, p.right, p.parent = nil, nil, nil
	}
	r := root
	if !p.red {
		r = balanceDeletion(root, replacement)
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
		s.moveRootToFront(r)
	}
}

// remove is the iterator's remove of p.
func (s *jset) remove(p BlockPos) { s.removeNode(p, false) }

// removeKey is Set.remove.
func (s *jset) removeKey(p BlockPos) { s.removeNode(p, true) }

func (s *jset) removeNode(p BlockPos, movable bool) {
	x := s.has[p]
	if x == nil {
		return
	}
	delete(s.has, p)
	i := s.idx(x.h)
	if x.tree {
		s.removeTreeNode(x, movable)
	} else if s.tab[i] == x {
		s.tab[i] = x.next
	} else {
		for e := s.tab[i]; e != nil; e = e.next {
			if e.next == x {
				e.next = x.next
				break
			}
		}
	}
	s.n--
}

// first is iterator().next().
func (s *jset) first() BlockPos {
	for s.tab[s.hint] == nil {
		s.hint++
	}
	return s.tab[s.hint].p
}

// list returns the elements in iteration order.
func (s *jset) list() []BlockPos {
	out := make([]BlockPos, 0, s.n)
	for _, e := range s.tab {
		for ; e != nil; e = e.next {
			out = append(out, e.p)
		}
	}
	return out
}
