package worldgen

// javaHashSetOrder returns positions (distinct, in insertion order) in
// the order a java.util.HashSet<BlockPos> built by adding them in that
// order iterates them, using the jset HashMap model (Vec3i hash spread,
// order-preserving resize splits, red-black tree bins). Vegetation
// patches feed it sloped, strongly colliding coordinates, so tree bins
// do occur.
func javaHashSetOrder(ps []BlockPos) []BlockPos {
	s := newJSet()
	for _, p := range ps {
		s.add(p)
	}
	return s.list()
}
