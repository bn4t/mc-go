package worldgen

// javaHashSetOrder returns positions (distinct, in insertion order) in
// the order a java.util.HashSet<BlockPos> built by adding them in that
// order iterates them. It models java.util.HashMap as specified by the
// JDK: the spread Vec3i hash ((y + z*31)*31 + x, h ^ h>>>16), doubling
// resizes with order-preserving bin splits, the resize a long bin
// forces while the table is under 64 buckets, and red-black tree bins
// (which reorder their chain: the root moves to the front and new
// entries are linked after their tree parent). Vegetation patches feed
// it sloped, strongly colliding coordinates, so tree bins do occur.
func javaHashSetOrder(ps []BlockPos) []BlockPos {
	m := &jhashMap{}
	for _, p := range ps {
		m.put(p)
	}
	out := make([]BlockPos, 0, m.size)
	for _, b := range m.tab {
		for e := b; e != nil; e = e.next {
			out = append(out, e.key)
		}
	}
	return out
}

const (
	jTreeifyThreshold   = 8
	jUntreeifyThreshold = 6
	jMinTreeifyCapacity = 64
)

type jnode struct {
	hash int32
	key  BlockPos
	next *jnode
	// tree bin fields
	tree                      bool
	parent, left, right, prev *jnode
	red                       bool
}

type jhashMap struct {
	tab       []*jnode
	size      int
	threshold int
}

func jhash(p BlockPos) int32 {
	h := (p[1]+p[2]*31)*31 + p[0]
	return h ^ int32(uint32(h)>>16)
}

func (m *jhashMap) put(k BlockPos) {
	h := jhash(k)
	if len(m.tab) == 0 {
		m.resize()
	}
	n := int32(len(m.tab))
	i := (n - 1) & h
	p := m.tab[i]
	switch {
	case p == nil:
		m.tab[i] = &jnode{hash: h, key: k}
	case p.hash == h && p.key == k:
		return
	case p.tree:
		if !m.putTreeVal(p, h, k) {
			return
		}
	default:
		for bin := 0; ; bin++ {
			e := p.next
			if e == nil {
				p.next = &jnode{hash: h, key: k}
				if bin >= jTreeifyThreshold-1 {
					m.treeifyBin(h)
				}
				break
			}
			if e.hash == h && e.key == k {
				return
			}
			p = e
		}
	}
	m.size++
	if m.size > m.threshold {
		m.resize()
	}
}

func (m *jhashMap) resize() {
	old := m.tab
	oldCap := len(old)
	var newCap int
	if oldCap > 0 {
		newCap = oldCap << 1
		m.threshold <<= 1
	} else {
		newCap = 16
		m.threshold = 12
	}
	tab := make([]*jnode, newCap)
	m.tab = tab
	for j, e := range old {
		if e == nil {
			continue
		}
		switch {
		case e.next == nil:
			tab[int(e.hash)&(newCap-1)] = e
		case e.tree:
			m.split(e, j, oldCap)
		default:
			var loH, loT, hiH, hiT *jnode
			for ; e != nil; e = e.next {
				if int(e.hash)&oldCap == 0 {
					if loT == nil {
						loH = e
					} else {
						loT.next = e
					}
					loT = e
				} else {
					if hiT == nil {
						hiH = e
					} else {
						hiT.next = e
					}
					hiT = e
				}
			}
			if loT != nil {
				loT.next = nil
				tab[j] = loH
			}
			if hiT != nil {
				hiT.next = nil
				tab[j+oldCap] = hiH
			}
		}
	}
}

func (m *jhashMap) treeifyBin(h int32) {
	n := len(m.tab)
	if n < jMinTreeifyCapacity {
		m.resize()
		return
	}
	idx := int(h) & (n - 1)
	var hd, tl *jnode
	for e := m.tab[idx]; e != nil; e = e.next {
		e.tree = true
		e.prev = tl
		if tl == nil {
			hd = e
		}
		tl = e
	}
	if hd != nil {
		m.treeify(hd)
	}
}

// treeDir is the descent direction for hash h at node p (hashes of
// distinct patch positions never tie, see javaHashSetOrder).
func treeDir(p *jnode, h int32) int {
	switch {
	case p.hash > h:
		return -1
	case p.hash < h:
		return 1
	}
	return -1
}

func (m *jhashMap) treeify(head *jnode) {
	var root *jnode
	for x := head; x != nil; {
		next := x.next
		x.left, x.right = nil, nil
		if root == nil {
			x.parent = nil
			x.red = false
			root = x
		} else {
			for p := root; ; {
				dir := treeDir(p, x.hash)
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
		x = next
	}
	m.moveRootToFront(root)
}

func (m *jhashMap) moveRootToFront(root *jnode) {
	if root == nil || len(m.tab) == 0 {
		return
	}
	idx := int(root.hash) & (len(m.tab) - 1)
	first := m.tab[idx]
	if root == first {
		return
	}
	m.tab[idx] = root
	rp := root.prev
	if rn := root.next; rn != nil {
		rn.prev = rp
	}
	if rp != nil {
		rp.next = root.next
	}
	if first != nil {
		first.prev = root
	}
	root.next = first
	root.prev = nil
}

func treeRoot(p *jnode) *jnode {
	for p.parent != nil {
		p = p.parent
	}
	return p
}

// putTreeVal adds k to the tree bin headed by first; false if present.
func (m *jhashMap) putTreeVal(first *jnode, h int32, k BlockPos) bool {
	root := treeRoot(first)
	for p := root; ; {
		if p.hash == h && p.key == k {
			return false
		}
		dir := treeDir(p, h)
		xp := p
		if dir <= 0 {
			p = p.left
		} else {
			p = p.right
		}
		if p == nil {
			xpn := xp.next
			x := &jnode{hash: h, key: k, next: xpn, tree: true}
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
			m.moveRootToFront(balanceInsertion(root, x))
			return true
		}
	}
}

func (m *jhashMap) split(b *jnode, index, bit int) {
	var loH, loT, hiH, hiT *jnode
	lc, hc := 0, 0
	for e := b; e != nil; {
		next := e.next
		e.next = nil
		if int(e.hash)&bit == 0 {
			e.prev = loT
			if loT == nil {
				loH = e
			} else {
				loT.next = e
			}
			loT = e
			lc++
		} else {
			e.prev = hiT
			if hiT == nil {
				hiH = e
			} else {
				hiT.next = e
			}
			hiT = e
			hc++
		}
		e = next
	}
	if loH != nil {
		if lc <= jUntreeifyThreshold {
			m.tab[index] = untreeify(loH)
		} else {
			m.tab[index] = loH
			if hiH != nil {
				m.treeify(loH)
			}
		}
	}
	if hiH != nil {
		if hc <= jUntreeifyThreshold {
			m.tab[index+bit] = untreeify(hiH)
		} else {
			m.tab[index+bit] = hiH
			if loH != nil {
				m.treeify(hiH)
			}
		}
	}
}

func untreeify(h *jnode) *jnode {
	for e := h; e != nil; e = e.next {
		e.tree = false
		e.parent, e.left, e.right, e.prev = nil, nil, nil, nil
	}
	return h
}

func rotateLeft(root, p *jnode) *jnode {
	if p == nil || p.right == nil {
		return root
	}
	r := p.right
	p.right = r.left
	if p.right != nil {
		p.right.parent = p
	}
	r.parent = p.parent
	switch pp := r.parent; {
	case pp == nil:
		root = r
		r.red = false
	case pp.left == p:
		pp.left = r
	default:
		pp.right = r
	}
	r.left = p
	p.parent = r
	return root
}

func rotateRight(root, p *jnode) *jnode {
	if p == nil || p.left == nil {
		return root
	}
	l := p.left
	p.left = l.right
	if p.left != nil {
		p.left.parent = p
	}
	l.parent = p.parent
	switch pp := l.parent; {
	case pp == nil:
		root = l
		l.red = false
	case pp.right == p:
		pp.right = l
	default:
		pp.left = l
	}
	l.right = p
	p.parent = l
	return root
}

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
