package server

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"strings"
	"sync"

	"mc-go/internal/event"
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
	worldviewRevision uint64                     // serialized finite-volume block commit watermark
	commandWG         map[[2]int]*commandWGEntry // owner-only, resident-identity-scoped; never saved
	name              string
	typeID            int32
	w                 *world.World
	seaLevel          int32
	flat              bool
	// flatTop is the flat generator's first free y (ChunkGenerator
	// getBaseHeight over its layers); the command structure planner uses
	// it in place of the noise column.
	flatTop    int
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
	// biomesRev counts writes to biomes (under biomesMu) so derived
	// indexes know when to rebuild.
	biomesRev uint64
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
	// maps is the dimension's MapIdDataStorage — the map_id data
	// entries a filled_map's component points at.
	maps    mapStore
	beacons map[world.Pos]*beaconEnt
	// comparatorOut is ComparatorBlockEntity.output by position —
	// the analog level a powered comparator last published.
	comparatorOut map[world.Pos]int
	// bees is a nest or hive's occupant count — the block entity's
	// Bees list length, tracked only as a count because the trigger
	// that reads it (bee_nest_destroyed's num_bees_inside) sees no
	// more than that.
	bees map[world.Pos]int32
	// sculk is every sculk sensor block entity's vibration state —
	// the VibrationSystem$Data plus the saved last frequency. Entries
	// are created when the sensor block is written and die with it.
	sculk map[world.Pos]*sculkSensor
	// genericBE holds the block entities mc-go keeps only as their
	// saved fields (spawners, gateways, hives, catalysts ...), and
	// genBEDone the generated chunks whose block entities are installed
	// (genblockentity.go). Simulation goroutine only.
	genericBE map[world.Pos]*genericBlockEntity
	genBEDone map[[2]int]bool
	// jukeboxes is every loaded jukebox block entity — the held
	// record plus the JukeboxSongPlayer's ticking state. Entries are
	// created on disc insert and die on eject/break/overwrite.
	jukeboxes map[world.Pos]*jukeboxEnt
	// beNames is every block entity's CustomName — the raw serialized
	// text component ("{\"text\":\"Bob\"}") a setblock tag or a named
	// placed item left behind (Nameable.setCustomName). Loot
	// copy_components[custom_name] and /data get block read it; the
	// entry dies with the block write.
	beNames map[world.Pos]string
	// claimedPOIs is the PoiManager's occupied set — village POI
	// positions (beds, bells) a villager claimed, keyed to the
	// claimant's entity id. Raids require occupied POIs nearby.
	claimedPOIs map[world.Pos]int32
	// blockLight is the per-level block-light grid, nonzero cells
	// only — the value vanilla's BlockLightSection stores per cell.
	// lightDirty accumulates chunk -> sections whose block light
	// changed mid-write; emitLightUpdate drains it into update_light
	// packets at the emit point vanilla uses (after the caller's ack).
	// blkLayered tracks which sections hold a materialized block-light
	// layer — the same eligibility set as sky — and blkStoring gates
	// every write.
	blockLight blockLightGrid
	// terrain is the generated dimension's chunk source (nil for the
	// flat and stand-in worlds); chunk-generation spawns read its existence.
	terrain *terrainSource
	// genEmit caches a provided chunk's generated emitters; genLit
	// marks chunks whose generated emitters were spread.
	genEmit    map[[2]int][]genEmitter
	genLit     map[[2]int]bool
	lightDirty map[[2]int]map[int]bool
	blkLayered map[[2]int]map[int]bool
	// blkWritten tracks which layered block-light sections have ever
	// had a cell written — vanilla's DataLayer data!=null state, which
	// ships its array on the data mask even when every nibble is
	// zero (e.g. right after an erase flood). Sticky: survives dels.
	blkWritten map[[2]int]map[int]bool
	// skyWritten is the same bit for sky layers; non-virgin
	// materializations (the all-15 default above the top and the
	// repeatFirstLayer replica) count as written at creation.
	skyWritten map[[2]int]map[int]bool
	// skyLayers holds one 4096-nibble array per materialized sky-light
	// section; the set is driven by block-section non-emptiness
	// (vanilla's updateSectionStatus: a layer exists while the
	// section or one of its 26 neighbours is non-empty). Cells in
	// unlayered sections never store — reads virtualize (walk up to
	// the first layer's bottom row, or 15 above the data top).
	// lightLayered is the shared eligibility set; nonEmptyCnt caches
	// each chunk's per-section non-air counts; skyLowestSec caches
	// the storage-wide lowest materialized section.
	skyLayers    map[[2]int]map[int]*[4096]byte
	lightLayered map[[2]int]map[int]bool
	nonEmptyCnt  map[[2]int]map[int]int
	skyLowestSec int
	skyLowestSet bool
	// skyTops caches the sky heightmap per chunk (highest
	// light-occluding cell per column); skyDirty accumulates
	// chunk -> marked sky sections for the next update_light.
	skyTops  map[[2]int]*[256]int32
	skyDirty map[[2]int]map[int]bool
	// lightMu serializes every read and write of the sectioned light
	// state: floods run on session goroutines, the tick-end flush and
	// the chunk-stream encoders read on other goroutines.
	lightMu sync.Mutex
	// skyGen marks chunks whose sky layers + steady-state values have
	// been lazily generated.
	skyGen map[[2]int]bool
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
	rsPending    []rsUpdate
	// rsTorchToggles is RedstoneTorchBlock.RECENT_TOGGLES — one shared
	// list per level, pruned lazily from the front (oldest first)
	// whenever a torch tick runs.
	rsTorchToggles []rsTorchToggle
	// raids is the dimension's active raid map (Raids.raidMap); the
	// next id is a per-dimension counter like vanilla's getUniqueId.
	raids    map[int]*raid
	raidNext int
	// fight is the dimension's EnderDragonFight (generated End only).
	fight *dragonFight
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
			flatTop:    -60,
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
			flatTop:    -60,
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
			flatTop:    -60,
			coordScale: 1,
			minY:       0,
			height:     256,
			// 26.2's the_end dimension type has has_skylight: true, and
			// its chunks ship sky light arrays.
			skylight: true,
			biome:    "minecraft:the_end",
			arrivalY: 64,
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
	profile, err := sess.javaProfile()
	if err != nil {
		return err
	}
	dimensionType, err := profile.projectRegistryID("minecraft:dimension_type", d.typeID)
	if err != nil {
		return err
	}
	join := javaJoinGame{DimensionType: dimensionType, Dimension: d.name, HashedSeed: hashedSeed(s.worldSeed()),
		GameMode: sess.mode(), PreviousGameMode: 255, Flat: d.flat, PortalCooldown: portalCooldown, SeaLevel: d.seaLevel}
	return sess.write(sess.ids().PlayRespawn, profile.encodeRespawn(join, dataKept))
}

// changeDimension moves a player across dimensions the way vanilla
// does: the client's level is reset with a Respawn packet, then the
// new dimension's chunk window and entities stream in. Entity fan-out
// follows: the traveler despawns for the old dimension's residents and
// spawns for the new one's.
func (s *Server) changeDimension(sess *Session, dst *dimension, x, y, z float64, portalCooldown int32) {
	s.changeDimensionFacing(sess, dst, x, y, z, 0, 0, false, 0, portalCooldown)
}

// changeDimensionFacing is changeDimension arriving with a facing.
// relative marks a portal trip: the position sync carries the yaw
// turn as a relative rotation (the client keeps its own facing and
// motion) instead of an absolute one.
func (s *Server) changeDimensionFacing(sess *Session, dst *dimension, x, y, z float64, yaw, pitch float32, relative bool, turn float32, portalCooldown int32) {
	flags := int32(0)
	if relative {
		flags = syncRelRotDelta
	}
	s.changeDimensionSync(sess, dst, x, y, z, yaw, pitch, flags, turn, 0, portalCooldown)
}

// changeDimensionSync is the dimension change with the arrival sync's
// Relative flags spelled out: syncYaw/syncPitch are what the packet
// carries (deltas for the relative axes), yaw/pitch the server-side
// result.
func (s *Server) changeDimensionSync(sess *Session, dst *dimension, x, y, z float64, yaw, pitch float32, flags int32, syncYaw, syncPitch float32, portalCooldown int32) {
	old := sess.dimension()
	if old == dst {
		s.teleportWithin(sess, x, y, z)
		return
	}
	eid := sess.entityID.Load()
	from := old.name
	sess.setDimension(dst)
	sess.setPos(x, y, z, yaw, pitch, false)
	s.fireChangedDimension(sess, from, dst.name)
	s.entities.Update(eid, x, y, z, yaw, pitch, false)
	s.entities.SetDimension(eid, dst.name)
	// Old-dimension residents drop the traveler; the client's own
	// tracker resets with Respawn.
	s.broadcastEntityRemoveIn(old.name, sess, []int32{eid})
	sess.trackMu.Lock()
	sess.trackedEnts = nil
	sess.trackMu.Unlock()

	// Vanilla reports a 10-tick client-side portal cooldown after
	// travel (measured on the real server in creative) and keeps
	// attributes + metadata across the crossing (dataKept 0b11).
	if portalCooldown < 0 {
		portalCooldown = 10
	}
	_ = s.sendRespawn(sess, dst, portalCooldown, 3)
	if flags != 0 {
		_ = s.sendSyncPosFlags(sess, x, y, z, syncYaw, syncPitch, flags)
	} else {
		_ = s.sendSyncPos(sess, x, y, z)
	}
	_ = s.sendHealth(sess)

	// Restream: drop the old window, center and fill the new one.
	sess.resetChunks()
	sess.sentMu.Lock()
	sess.centerSet = false
	sess.sentMu.Unlock()
	scx, scz := int(math.Floor(x))>>4, int(math.Floor(z))>>4
	s.sendCenterChunk(sess, scx, scz)
	_ = s.sendWindow(sess, scx, scz)

	// Entities parked here wake: a traveler's presence tickets the
	// dimension's chunks, activating anything addDuringTeleport or a
	// cross-dim summon had parked (measured: a pig tp'd into an
	// empty nether sits dormant until a player arrives).
	s.wakeDormant(dst.name)
	// And the dimension they left unloads with them: everything still
	// live there goes dormant again (vanilla's chunk unload).
	s.reparkDim(from)

	// New-dimension entities (players and mobs) spawn for the
	// traveler; the traveler spawns for them.
	_ = s.sendEntitySpawnsTo(sess)
	if e, ok := playerEntity(sess); ok {
		s.broadcastEntitySpawnIn(dst.name, sess, e)
	}
}

// dimHasLiveSession reports whether any non-spectator play session
// stands in the named dimension — the proxy for "the dimension's
// chunks are ticketed" used by dormant-entity rules.
func (s *Server) dimHasLiveSession(dim string) bool {
	has := false
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() == StatePlay && p.mode() != GameModeSpectator {
			name := p.dimName()
			if prev := p.dimSwapPrev.Load(); prev != nil {
				// Inside an execute `in` fork dim reads as the forked
				// level — the player's chunks still ticket its real
				// dimension, so presence counts there, not here.
				name = prev.name
			}
			if name == dim {
				has = true
				return false
			}
		}
		return true
	})
	return has
}

// anyLiveSession reports whether any non-spectator play session
// exists anywhere on the server. Vanilla keeps an entity ticking in a
// chunk that is ticketed by something — the spawn chunks stay loaded
// even on an empty server — so an entity only parks when someone is
// online but NOT in its dimension: a mob spawned into a session-less
// world behaves like one inside the always-ticketed spawn chunks.
func (s *Server) anyLiveSession() bool {
	has := false
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() == StatePlay && p.mode() != GameModeSpectator {
			has = true
			return false
		}
		return true
	})
	return has
}

// shouldPark reports whether an entity landing in dim parks dormant:
// vanilla's unloaded-chunk semantics — someone is online, just not in
// this dimension, so nothing tickets the chunk.
func (s *Server) shouldPark(dim string) bool {
	return s.anyLiveSession() && !s.dimHasLiveSession(dim)
}

// reparkDim returns an emptied dimension's live entities to dormant —
// the last player leaving unloads the dimension's chunks on vanilla,
// parking everything in them again.
func (s *Server) reparkDim(dim string) {
	if !s.shouldPark(dim) {
		return
	}
	for _, e := range s.entities.All() {
		if e.presenceOnly || e.Dormant || e.Dying || e.Dim != dim {
			continue
		}
		if ptr, ok := s.entities.GetPtr(e.ID); ok {
			ptr.Dormant = true
		}
	}
}

// wakeDormant activates every entity parked in the named dimension —
// the way a player's arrival tickets the chunk vanilla had parked it
// in. All() snapshots copies, so the flag writes through GetPtr.
func (s *Server) wakeDormant(dim string) {
	for _, e := range s.entities.All() {
		if e.presenceOnly || !e.Dormant || e.Dim != dim {
			continue
		}
		if ptr, ok := s.entities.GetPtr(e.ID); ok {
			ptr.Dormant = false
		}
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
