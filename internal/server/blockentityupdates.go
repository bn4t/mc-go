package server

import (
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// Measured26.2 getUpdatePacket overrides, including inherited hanging signs.
// Save/update-tag support alone does not imply an update packet: ordinary
// containers and command blocks inherit BlockEntity's null implementation.
var blockEntityPacketTypes = map[string]bool{
	"banner": true, "beacon": true, "brushable_block": true, "campfire": true, "conduit": true,
	"copper_golem_statue": true, "creaking_heart": true, "decorated_pot": true, "jigsaw": true,
	"shelf": true, "sign": true, "hanging_sign": true, "skull": true, "mob_spawner": true,
	"structure_block": true, "test_block": true, "test_instance_block": true, "end_gateway": true,
	"trial_spawner": true, "vault": true,
}

func (s *Server) blockEntityUpdateAt(d *dimension, pos world.Pos) (blockEntitySpec, []byte, bool) {
	state := d.w.GetBlock(pos)
	spec, ok := blockEntityOf(state)
	if !ok {
		return blockEntitySpec{}, nil, false
	}
	typ := strings.TrimPrefix(spec.name, "minecraft:")
	if !blockEntityPacketTypes[typ] {
		return blockEntitySpec{}, nil, false
	}
	if _, sign := signSpecOf(state); sign {
		s.ensureSign(d, pos)
		if value, ok := s.signAt(d, pos); ok {
			return spec, signNBT(value), true
		}
		return blockEntitySpec{}, nil, false
	}
	fields := genBlockEntityDefaults(typ)
	if save, ok := s.blockEntitySaveMap(d, pos); ok {
		fields = save
		for _, key := range []string{"id", "x", "y", "z", "keepPacked"} {
			delete(fields, key)
		}
	}
	payload := blockEntityUpdateTag(typ, fields)
	if payload == nil {
		// Chunk entries omit empty tags; update packets carry the compound.
		payload = []byte{nbt.TagCompound, nbt.TagEnd}
	}
	return spec, payload, true
}
