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
	minY          int
	biomes        []int32 // per section, 64 quarts x-fastest then z then y
	states        []world.Block
	blockEntities []chunkSnapshotBlockEntity
	light         []byte
	mapping       *registry.BlockMapping
	catalog       *registry.BlockCatalog
	projectionErr error
	bedType       int32
	bedStates     []bool // Shared immutable canonical-state membership.
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
	profile, _ := javaProfileFor(776)
	return captureProjectedChunkSnapshot(d, cx, cz, blockEntities, mapping, profile)
}

// captureChunkSnapshotForProfile resolves all non-block registry IDs on the
// simulation owner. Workers receive only detached payloads and immutable block
// projections, never a mutable session or a live configuration registry.
func captureChunkSnapshotForProfile(d *dimension, cx, cz int, blockEntities []clientBlockEntity, profile *javaProfile) *chunkSnapshot {
	return captureProjectedChunkSnapshot(d, cx, cz, blockEntities, profile.blocks, profile)
}

func captureProjectedChunkSnapshot(d *dimension, cx, cz int, blockEntities []clientBlockEntity, mapping *registry.BlockMapping, profile *javaProfile) *chunkSnapshot {
	snapshot := &chunkSnapshot{
		cx: cx, cz: cz, height: d.height, minY: d.minY, mapping: mapping, catalog: world.BlockCatalog(), bedType: -1,
	}
	if profile.version.Protocol == 775 {
		var err error
		snapshot.bedType, err = profile.builtinRegistryID("minecraft:block_entity_type", "minecraft:bed")
		if err != nil {
			snapshot.projectionErr = err
			return snapshot
		}
		snapshot.bedStates = legacyBedStates()
	}
	snapshot.biomes = make([]int32, d.height/16*64)
	ids := map[string]int32{}
	for i := range snapshot.biomes {
		sy, q := i/64, i%64
		name := d.quartBiome(cx*4+q&3, d.minY>>2+sy*4+q>>4, cz*4+(q>>2)&3)
		id, ok := ids[name]
		if !ok {
			id = profile.registryEntryID("minecraft:worldgen/biome", name)
			if id < 0 {
				snapshot.projectionErr = fmt.Errorf("Java protocol %d has no biome %q", profile.version.Protocol, name)
				return snapshot
			}
			ids[name] = id
		}
		snapshot.biomes[i] = id
	}
	snapshot.blockEntities = make([]chunkSnapshotBlockEntity, len(blockEntities))
	for i, be := range blockEntities {
		wire, err := profile.projectBuiltinRegistryID("minecraft:block_entity_type", be.spec.id)
		if err != nil {
			snapshot.projectionErr = err
			return snapshot
		}
		snapshot.blockEntities[i] = chunkSnapshotBlockEntity{
			localXZ: byte(be.pos.X&15)<<4 | byte(be.pos.Z&15), y: int16(be.pos.Y),
			typeID: wire, nbt: make([]byte, len(be.nbt)),
		}
		copy(snapshot.blockEntities[i].nbt, be.nbt)
	}
	snapshot.states = append([]world.Block(nil), d.chunkBlocks(cx, cz)...)
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
	if snapshot.projectionErr != nil {
		return nil, snapshot.projectionErr
	}
	if snapshot.mapping == nil {
		return nil, fmt.Errorf("chunk snapshot has no block mapping")
	}
	if snapshot.mapping.Catalog() != snapshot.catalog {
		return nil, fmt.Errorf("chunk snapshot block mapping belongs to another catalog")
	}
	air, ok := snapshot.mapping.Lookup(world.Air)
	if !ok {
		return nil, fmt.Errorf("chunk block mapping lacks an exact air state")
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
	uniform := make([]int32, 64) // a snapshot without biomes is biome id 0
	for sy := 0; sy < snapshot.height/16; sy++ {
		biomes := uniform
		if len(snapshot.biomes) >= (sy+1)*64 {
			biomes = snapshot.biomes[sy*64 : (sy+1)*64]
		}
		var blockCount int16
		for i, block := range snapshot.states[sy*4096 : (sy+1)*4096] {
			wire, err := snapshot.mapping.Encode(block)
			if err != nil {
				return nil, fmt.Errorf("encode chunk %d,%d: %w", snapshot.cx, snapshot.cz, err)
			}
			vals[i] = wire
			// Section counts describe the encoded presentation. A visual
			// fallback may turn an authoritative solid into target air.
			// Heightmaps above continue to describe authoritative geometry.
			if wire != air {
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
	bedCount := 0
	if snapshot.bedType >= 0 {
		for _, state := range snapshot.states {
			if int(state) < len(snapshot.bedStates) && snapshot.bedStates[state] {
				bedCount++
			}
		}
	}
	b.VarInt(int32(len(snapshot.blockEntities) + bedCount))
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
	if bedCount != 0 {
		for index, state := range snapshot.states {
			if int(state) >= len(snapshot.bedStates) || !snapshot.bedStates[state] {
				continue
			}
			x, z := index%16, (index/16)%16
			b.Byte(byte(x<<4 | z))
			b.Short(int16(index/256 + snapshot.minY))
			b.VarInt(snapshot.bedType)
			b.Byte(0)
		}
	}
	b.Raw(snapshot.light)
	return b.Bytes(), nil
}
