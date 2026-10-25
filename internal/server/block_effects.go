package server

import (
	"mc-go/api/registry"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// encodeLevelEvent projects the break-effect state through the captured Java
// profile. Other effect data are not block handles and are left untouched.
func (p *javaProfile) encodeLevelEvent(event int32, pos world.Pos, data int32) ([]byte, error) {
	if event == 2001 {
		if data < 0 || p.blocks == nil {
			return nil, registry.ErrUnknownBlockState
		}
		wire, err := p.blocks.Encode(world.Block(data))
		if err != nil {
			return nil, err
		}
		data = wire
	}
	if event == 1010 {
		wire, err := p.projectRegistryID("minecraft:jukebox_song", data)
		if err != nil {
			return nil, err
		}
		data = wire
	}
	var b protocol.Buffer
	b.Int(event)
	b.Position(int32(pos.X), int32(pos.Y), int32(pos.Z))
	b.Int(data)
	b.Bool(false)
	return b.Bytes(), nil
}
