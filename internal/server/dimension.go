package server

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"strings"
	"sync"

	"mc-go/internal/event"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// biomeAt resolves the gameplay biome at pos: a /fillbiome quart
// override when one exists, else the dimension's base biome.
func (s *Server) biomeAt(d *dimension, pos world.Pos) string {
	key := [3]int{pos.X >> 2, pos.Y >> 2, pos.Z >> 2}
	d.biomesMu.RLock()
	defer d.biomesMu.RUnlock()
	if b, ok := d.biomes[key]; ok {
		return b
	}
	return d.biome
}

// Dimension names and their dimension_type registry ids, in the
// captured registry's order (pinned by TestDimensionTypes).
const (
	dimOverworld = "minecraft:overworld"
	dimEnd       = "minecraft:the_end"
	dimNether    = "minecraft:the_nether"
)

// dimension is one loaded dimension: the sharded world plus every
// parameter the client needs to decode its chunks (height, section
// count, skylight) and to place the player on respawn (sea level,
// flat flag, coordinate scale). Block-entity stores are per-dimension
// because vanilla keeps them on the level, not the server.
type dimension struct {
	name       string
	typeID     int32
	w          *world.World
	seaLevel   int32
	flat       bool
	coordScale float64
	minY       int
	height     int
	skylight   bool
	biome      string
	// biomes overrides the dimension's single biome at quart
	// granularity (4x4x4 cells), written by /fillbiome — chunk encode
	// still ships the base palette; gameplay reads go through
	// s.biomeAt.
	biomes   map[[3]int]string
	biomesMu sync.RWMutex
	// ultraWarm marks a dimension type with the ultra_warm flag (the
	// nether): lava spreads like water there and flows ten times
	// faster, and water evaporates.
	ultraWarm bool
	// arrivalY is the fallback portal platform height when a column is
	// all air (the end's void) or ground cannot be found.
	arrivalY int
	// Block-entity stores, per level.
	signs      signStore
	chests     chestStore
	furnaces   furnaceStore
	hoppers    hopperStore
	containers containerStore
	beacons    map[world.Pos]*beaconEnt
	// bees is a nest or hive's occupant count — the block entity's
	// Bees list length, tracked only as a count because the trigger
	// that reads it (bee_nest_destroyed's num_bees_inside) sees no
	// more than that.
	bees map[world.Pos]int32
	// claimedPOIs is the PoiManager's occupied set — village POI
	// positions (beds, bells) a villager claimed, keyed to the
	// claimant's entity id. Raids require occupied POIs nearby.
	claimedPOIs map[world.Pos]int32
	// rsSilenceWire is RedStoneWireBlock.shouldSignal=false: held
	// while a wire recomputes its target so getBlockSignal ignores
	// dust emitters.
	rsSilenceWire bool
	// rsWriteDepth/rsPending are the neighbor-updater queue: inside a
	// block write, updateNeighborsAt fan-outs collect instead of
	// running synchronously (CollectingNeighborUpdater runs a write's
	// chained updates only after its own neighbour pass and shape
	// fold), and the outermost write drains them FIFO.
	rsWriteDepth int
	rsPending    []world.Pos
	// raids is the dimension's active raid map (Raids.raidMap); the
	// next id is a per-dimension counter like vanilla's getUniqueId.
	raids    map[int]*raid
	raidNext int
}

// sections is the chunk section count for this dimension.
func (d *dimension) sections() int { return d.height / 16 }

// netherGen is the built-in nether stand-in terrain: a bedrock floor
// and ceiling around a netherrack slab, air elsewhere. Vanilla flat
// worlds keep the real nether generator; replacing this with vanilla
// noise terrain is the tracked world-generation milestone.
func netherGen(p world.Pos) world.Block {
	switch {
	case p.Y == 0:
		return world.BlockStateMust("bedrock")
	case p.Y >= 1 && p.Y <= 32:
		return world.BlockStateMust("netherrack")
	case p.Y == 127:
		return world.BlockStateMust("bedrock")
	default:
		return world.Air
	}
}

// endGen is the built-in end stand-in terrain: a circular endstone
// island around the origin, void everywhere else.
func endGen(p world.Pos) world.Block {
	const radius = 48
	if p.Y >= 60 && p.Y <= 63 &&
		int64(p.X)*int64(p.X)+int64(p.Z)*int64(p.Z) <= radius*radius {
		return world.BlockStateMust("end_stone")
	}
	return world.Air
}

// newDimWorld builds a stand-in dimension world from a pure generator
// and pins the single biome its chunks carry.
func newDimWorld(bus *event.Bus, gen func(world.Pos) world.Block, biome string) *world.World {
	w := world.NewGen(bus, 8, gen)
	w.SetBiome(biome)
	return w
}

// initDimensions builds the three vanilla dimensions. The overworld
// keeps the flat preset; the nether and end get the stand-in
// generators above until the noise-terrain milestone lands.
func (s *Server) initDimensions() {
	// The overworld generates in the_void: its empty spawn list keeps
	// chunk generation from placing animals, matching the comparison
	// world's generator-settings.
	s.world.SetBiome("minecraft:the_void")
	s.dims = map[string]*dimension{
		dimOverworld: {
			name:       dimOverworld,
			typeID:     0,
			w:          s.world,
			seaLevel:   -63,
			flat:       true,
			coordScale: 1,
			minY:       MinY,
			height:     Height,
			skylight:   true,
			biome:      "minecraft:the_void",
			arrivalY:   -60,
		},
		dimNether: {
			name:       dimNether,
			typeID:     3,
			w:          newDimWorld(s.bus, netherGen, "minecraft:nether_wastes"),
			seaLevel:   32,
			flat:       true,
			coordScale: 8,
			minY:       0,
			height:     256,
			skylight:   false,
			biome:      "minecraft:nether_wastes",
			ultraWarm:  true,
			arrivalY:   33,
		},
		dimEnd: {
			name:       dimEnd,
			typeID:     2,
			w:          newDimWorld(s.bus, endGen, "minecraft:the_end"),
			seaLevel:   0,
			flat:       true,
			coordScale: 1,
			minY:       0,
			height:     256,
			skylight:   false,
			biome:      "minecraft:the_end",
			arrivalY:   64,
		},
	}
}

// dimensionByName resolves "name", "minecraft:name" to a dimension.
func (s *Server) dimensionByName(name string) *dimension {
	n := name
	if !strings.Contains(n, ":") {
		n = "minecraft:" + n
	}
	return s.dims[n]
}

// dimension returns the session's current dimension (overworld until
// play starts).
func (sess *Session) dimension() *dimension {
	if d := sess.dim.Load(); d != nil {
		return d
	}
	return sess.server.dims[dimOverworld]
}

// dimName is the dimension's registry name.
func (sess *Session) dimName() string { return sess.dimension().name }

// world returns the world the session currently occupies.
func (sess *Session) world() *world.World { return sess.dimension().w }

// dimWorld resolves a dimension name to its world; nil for unknown.
func (s *Server) dimWorld(name string) *world.World {
	if d := s.dims[name]; d != nil {
		return d.w
	}
	return nil
}

// sendRespawn writes the 776 Respawn body for a target dimension:
// dimension type id, dimension name, hashed seed, game modes, level
// flags, optional death location, portal cooldown, sea level, and the
// data-kept bitmask. Shared by death respawn and dimension travel.
func (s *Server) sendRespawn(sess *Session, d *dimension, portalCooldown int32, dataKept byte) error {
	var b protocol.Buffer
	b.VarInt(d.typeID)
	b.String(d.name)
	b.Long(hashedSeed(s.worldSeed())) // obfuscated world seed
	b.Byte(sess.mode())
	b.Byte(255) // previous game mode: none
	b.Bool(false)
	b.Bool(d.flat)
	b.Bool(false) // no death location
	b.VarInt(portalCooldown)
	b.VarInt(d.seaLevel)
	b.Byte(dataKept)
	return sess.write(sess.ids().PlayRespawn, b.Bytes())
}

// changeDimension moves a player across dimensions the way vanilla
// does: the client's level is reset with a Respawn packet, then the
// new dimension's chunk window and entities stream in. Entity fan-out
// follows: the traveler despawns for the old dimension's residents and
// spawns for the new one's.
func (s *Server) changeDimension(sess *Session, dst *dimension, x, y, z float64, portalCooldown int32) {
	old := sess.dimension()
	if old == dst {
		s.teleportWithin(sess, x, y, z)
		return
	}
	eid := sess.entityID.Load()
	from := old.name
	sess.dim.Store(dst)
	sess.setPos(x, y, z, 0, 0, false)
	s.fireChangedDimension(sess, from, dst.name)
	s.entities.Update(eid, x, y, z, 0, 0, false)
	s.entities.SetDimension(eid, dst.name)
	// Old-dimension residents drop the traveler; the client's own
	// tracker resets with Respawn.
	s.broadcastEntityRemoveIn(old.name, sess, []int32{eid})

	// Vanilla reports a 10-tick client-side portal cooldown after
	// travel (measured on the real server in creative) and keeps
	// attributes + metadata across the crossing (dataKept 0b11).
	if portalCooldown < 0 {
		portalCooldown = 10
	}
	_ = s.sendRespawn(sess, dst, portalCooldown, 3)
	_ = s.sendSyncPos(sess, x, y, z)
	_ = s.sendHealth(sess)

	// Restream: drop the old window, center and fill the new one.
	sess.resetChunks()
	sess.sentMu.Lock()
	sess.centerSet = false
	sess.sentMu.Unlock()
	scx, scz := int(math.Floor(x))>>4, int(math.Floor(z))>>4
	s.sendCenterChunk(sess, scx, scz)
	_ = s.sendWindow(sess, scx, scz)

	// New-dimension entities (players and mobs) spawn for the
	// traveler; the traveler spawns for them.
	_ = s.sendEntitySpawnsTo(sess)
	if e, ok := playerEntity(sess); ok {
		s.broadcastEntitySpawnIn(dst.name, sess, e)
	}
}

// teleportWithin moves a player inside their current dimension.
func (s *Server) teleportWithin(sess *Session, x, y, z float64) {
	sess.setPos(x, y, z, 0, 0, false)
	if eid := sess.entityID.Load(); eid != 0 {
		s.entities.Update(eid, x, y, z, 0, 0, false)
		s.broadcastEntityTeleport(sess, eid, x, y, z, 0, 0, false)
	}
	_ = s.sendSyncPos(sess, x, y, z)
	scx, scz := int(math.Floor(x))>>4, int(math.Floor(z))>>4
	_ = s.sendWindow(sess, scx, scz)
	s.sendCenterChunk(sess, scx, scz)
}

// hashedSeed is vanilla's BiomeManager.obfuscateSeed: sha256 of the
// little-endian seed, first eight bytes read little-endian. The wire
// never carries the raw seed (verified against the real server).
func hashedSeed(seed int64) int64 {
	var in [8]byte
	binary.LittleEndian.PutUint64(in[:], uint64(seed))
	digest := sha256.Sum256(in[:])
	return int64(binary.LittleEndian.Uint64(digest[:8]))
}
