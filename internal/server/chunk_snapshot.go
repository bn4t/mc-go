package server

import (
	"fmt"

	"mc-go/api/registry"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// chunkSnapshot contains detached, privately owned data and an immutable wire
// mapping. Capturing requires simulation ownership; encoding does not. Workers
// must never receive a dimension, world, session, or live block-entity pointer.
type chunkSnapshot struct {
	cx, cz        int
	height        int
	biome         int32
	states        []world.Block
	blockEntities []chunkSnapshotBlockEntity
	light         []byte
	mapping       *registry.BlockMapping
	catalog       *registry.BlockCatalog
}

type chunkSnapshotBlockEntity struct {
	localXZ byte
	y       int16
	typeID  int32
	nbt     []byte
}

// chunkSnapshotUpperBound reserves snapshot memory before allocating block and
// light arrays. Runtime block IDs are uint32; light has at most one 2048-byte
// sky and block array per section. Metadata/NBT are copied, never borrowed.
func chunkSnapshotUpperBound(d *dimension, blockEntities []clientBlockEntity) int64 {
	n := int64(d.height)*256*4 + int64(d.sections()+2)*4096 + 512
	for _, be := range blockEntities {
		n += int64(len(be.nbt)) + 128
	}
	return n
}

func captureChunkSnapshot(d *dimension, cx, cz int, blockEntities []clientBlockEntity, mapping *registry.BlockMapping) *chunkSnapshot {
	snapshot := &chunkSnapshot{
		cx: cx, cz: cz, height: d.height, biome: biomeID(d.biome), mapping: mapping, catalog: world.BlockCatalog(),
		states:        make([]world.Block, 16*d.height*16),
		blockEntities: make([]chunkSnapshotBlockEntity, len(blockEntities)),
	}
	d.w.FillBox(
		world.Pos{X: cx * 16, Y: d.minY, Z: cz * 16},
		world.Pos{X: cx*16 + 15, Y: d.minY + d.height - 1, Z: cz*16 + 15}, snapshot.states,
	)
	for i, be := range blockEntities {
		snapshot.blockEntities[i] = chunkSnapshotBlockEntity{
			localXZ: byte(be.pos.X&15)<<4 | byte(be.pos.Z&15), y: int16(be.pos.Y),
			typeID: be.spec.id, nbt: make([]byte, len(be.nbt)),
		}
		copy(snapshot.blockEntities[i].nbt, be.nbt)
	}
	light := encodeLightDataFor(d, cx, cz)
	snapshot.light = make([]byte, len(light))
	copy(snapshot.light, light)
	return snapshot
}

// retainedBytes accounts the detached arrays retained by a queued encoder job.
// The small struct/slice headers are conservatively included in the fixed and
// per-entry allowances, shared immutable mapping tables are not job-owned.
func (snapshot *chunkSnapshot) retainedBytes() int64 {
	n := int64(len(snapshot.states))*4 + int64(cap(snapshot.light)) + 256
	for _, be := range snapshot.blockEntities {
		n += int64(cap(be.nbt)) + 128
	}
	return n
}

// encodeChunkSnapshot is a pure transform over the snapshot. Its output is the
// same Chunk Data and Update Light body as the synchronous encoder; it never
// consults current blocks, lighting, registries, or a player's current dimension.
func encodeChunkSnapshot(snapshot *chunkSnapshot) ([]byte, error) {
	if snapshot.mapping == nil {
		return nil, fmt.Errorf("chunk snapshot has no block mapping")
	}
	if snapshot.mapping.Catalog() != snapshot.catalog {
		return nil, fmt.Errorf("chunk snapshot block mapping belongs to another catalog")
	}
	var b protocol.Buffer
	b.Int(int32(snapshot.cx))
	b.Int(int32(snapshot.cz))
	b.VarInt(int32(len(heightmapTypes)))
	heightVals := packPadded(heightmapValues(snapshot.states, snapshot.height), heightmapBits)
	for _, typ := range heightmapTypes {
		b.VarInt(typ)
		b.VarInt(int32(len(heightVals)))
		writeLongs(&b, heightVals)
	}
	var data protocol.Buffer
	vals := make([]int32, 4096)
	biomes := make([]int32, 64)
	for i := range biomes {
		biomes[i] = snapshot.biome
	}
	for sy := 0; sy < snapshot.height/16; sy++ {
		var blockCount int16
		for i, block := range snapshot.states[sy*4096 : (sy+1)*4096] {
			wire, err := snapshot.mapping.Encode(block)
			if err != nil {
				return nil, fmt.Errorf("encode chunk %d,%d: %w", snapshot.cx, snapshot.cz, err)
			}
			vals[i] = wire
			if block != world.Air {
				blockCount++
			}
		}
		data.Short(blockCount)
		data.Short(0) // Existing vanilla-parity fluid-count representation.
		encodePalettedContainer(&data, vals, 4096, 4)
		encodePalettedContainer(&data, biomes, 64, 1)
	}
	b.VarInt(int32(data.Len()))
	b.Raw(data.Bytes())
	b.VarInt(int32(len(snapshot.blockEntities)))
	for _, be := range snapshot.blockEntities {
		b.Byte(be.localXZ)
		b.Short(be.y)
		b.VarInt(be.typeID)
		if len(be.nbt) == 0 {
			b.Byte(0)
		} else {
			b.Raw(be.nbt)
		}
	}
	b.Raw(snapshot.light)
	return b.Bytes(), nil
}
