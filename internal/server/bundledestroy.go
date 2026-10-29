package server

// spillDestroyedBundle is the measured BundleItem.onDestroyed callback. The
// source contents clear before spawning independent item entities at exactly
// the destroyed item's position. ItemUtils.onContainerDestroyed uses fresh
// ItemEntity defaults: no owner/thrower/delay/gravity override and a new toss.
// Source: official26.2 jar, Temurin25 javap BundleItem, ItemUtils and ItemEntity.
func (s *Server) spillDestroyedBundle(source *Entity) {
	if source == nil || !isBundle(source.Item) {
		return
	}
	items := bundleItems(source.Item)
	bundleSetItems(&source.Item, nil)
	for _, item := range items {
		if item.Empty() {
			continue
		}
		vx, vz := s.rng.nextDouble()*0.2-0.1, s.rng.nextDouble()*0.2-0.1
		child := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: source.Dim, X: source.X, Y: source.Y, Z: source.Z, Item: item, ItemHP: 5, MotionX: vx, MotionY: 0.2, MotionZ: vz, VX: int16(vx * 8000), VY: 1600, VZ: int16(vz * 8000), Yaw: float32(s.rng.next()>>40) * (1.0 / 16777216.0) * 360})
		s.broadcastEntitySpawn(nil, *child)
		s.broadcastEntityMetadata(child.ID, encodeItemMetadata(item))
	}
}
