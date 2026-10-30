package server

// A structure-created cart is a fresh Entity, not a summon/load. Its native
// firstTick flag therefore saves as HasTicked=true until the first cart pass.
// Existing vehicle cargo owns the latent table/seed and unpacks only on use.
func (s *Server) spawnMineshaftCart(d *dimension, x, y, z float64, yaw float32, table string, seed int64) *Entity {
	fields := map[string]any{"HasTicked": int8(1)}
	if table != "" {
		fields["LootTable"] = table
		if seed != 0 {
			fields["LootTableSeed"] = seed
		}
	}
	entity := s.entities.Spawn(&Entity{Type: mustEntityType("chest_minecart"), Dim: d.name, X: x, Y: y, Z: z, Yaw: yaw, CartYawO: yaw, ExtraNBT: fields})
	s.broadcastEntitySpawn(nil, *entity)
	return entity
}
