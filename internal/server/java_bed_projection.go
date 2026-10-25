package server

import (
	"strings"
	"sync"

	"mc-go/api/registry"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// 26.1 chunk captures contain one bed block entity for each head and foot,
// type 25, with a single TAG_End payload. 26.2 renders beds without that type.
// The shared membership table is immutable and avoids allocating per-bed input
// records outside the existing snapshot reservation.
var javaLegacyBeds struct {
	sync.Once
	states []bool
}

func legacyBedStates() []bool {
	javaLegacyBeds.Do(func() {
		catalog := world.BlockCatalog()
		javaLegacyBeds.states = make([]bool, catalog.Len())
		for i := range javaLegacyBeds.states {
			state, _ := catalog.State(registry.BlockStateID(i))
			javaLegacyBeds.states[i] = strings.HasPrefix(state.Name(), "minecraft:") && strings.HasSuffix(state.Name(), "_bed")
		}
	})
	return javaLegacyBeds.states
}

// Live 775 bed updates carry an empty compound (0a00), unlike the chunk
// entry's optional-NBT TAG_End. Only old-profile viewers need this renderer
// record; the authoritative 776 world does not gain a synthetic block entity.
func (s *Server) broadcastLegacyBedData(d *dimension, pos world.Pos, state world.Block) {
	states := legacyBedStates()
	if uint64(state) >= uint64(len(states)) || !states[state] {
		return
	}
	s.sessions.Range(func(_, value any) bool {
		sess := value.(*Session)
		if sess.state.Load() != StatePlay || sess.dimName() != d.name || !sess.hasChunk(pos.X>>4, pos.Z>>4) {
			return true
		}
		profile, err := sess.javaProfile()
		if err != nil {
			sess.closeQueue()
			return true
		}
		if profile.version.Protocol != 775 {
			return true
		}
		typeID, err := profile.builtinRegistryID("minecraft:block_entity_type", "minecraft:bed")
		if err != nil {
			sess.closeQueue()
			return true
		}
		var b protocol.Buffer
		b.Position(int32(pos.X), int32(pos.Y), int32(pos.Z))
		b.VarInt(typeID)
		b.Byte(10)
		b.Byte(0)
		_ = sess.writeProjectedPacket(sess.ids().PlayBlockEntityData, b.Bytes())
		return true
	})
}
