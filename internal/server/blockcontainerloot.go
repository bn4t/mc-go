package server

import "mc-go/internal/world"

// Native block-container reads, slot writes and menu creation unpack the
// armed table before accessing storage. The location is the owning block,
// independently of the command source or player credited with opening it.
func (s *Server) unpackBlockContainerLoot(d *dimension, pos world.Pos, slots []ItemStack, table *string, seed *int64, player *Session) {
	if *table == "" {
		return
	}
	name, savedSeed := *table, *seed
	*table, *seed = "", 0
	old := append([]ItemStack(nil), slots...)
	biome := ""
	if d != nil {
		biome = s.spawnBiomeAt(d, pos)
	}
	unpackLootIntoInBiome(slots, name, savedSeed, biome)
	for slot, previous := range old {
		if !menuRemoteStackEqual(previous, slots[slot]) {
			s.queueMenuCommandSlot(d, pos, slot, previous, 0)
		}
	}
	if player != nil {
		s.firePlayerGeneratesContainerLoot(player, name)
	}
}
