package server

import (
	"math"
	"math/rand"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
	"strings"
)

func (s *Server) runPlaceStructure(player string, args []string) reply {
	if len(args) < 1 {
		return errTokenEnd("command.unknown.command", 2)
	}
	name := normalizeID(args[0])
	short := strings.TrimPrefix(name, "minecraft:")
	if !structureNames[short] {
		return componentReply(commandError("commands.place.structure.invalid", name))
	}
	x, y, z := s.baseBlockPos(player)
	position := [3]int{x, y, z}
	if len(args) > 1 {
		if len(args) < 4 {
			return errToken("argument.pos3d.incomplete", 3)
		}
		for axis := 0; axis < 3; axis++ {
			value, err := parseCoord(args[axis+1], position[axis])
			if err != nil {
				return errToken("parsing.int.expected", axis+3)
			}
			position[axis] = value
		}
	}
	if len(args) > 4 {
		return errToken("command.unknown.argument", 6)
	}
	d := s.dimOf(player)
	if d == nil {
		return componentReply(commandError("commands.place.structure.failed"))
	}
	if position[1] < d.minY || position[1] >= d.minY+d.height || position[0] < -30000000 || position[0] >= 30000000 || position[2] < -30000000 || position[2] >= 30000000 {
		return componentReply(commandError("argument.pos.outofworld"))
	}
	sender := s.findSessionByName(player)
	if !s.blockPosLoaded(d, sender, position[0], position[1], position[2]) {
		return componentReply(commandError("argument.pos.unloaded"))
	}
	seed := s.worldSeed()
	var baseHeight func(int32, int32, int) int32
	var source worldgen.CommandStructureSource
	if d.flat {
		if d.name == dimOverworld {
			preset := s.FlatPreset
			if preset == nil {
				preset = world.DefaultFlatPreset()
			}
			gen, _ := world.FlatGen(preset, d.minY)
			source.FlatBiome = d.biome
			source.BaseBlock = func(x, y, z int32) world.Block { return gen(world.Pos{X: int(x), Y: int(y), Z: int(z)}) }
		}
		top := int32(d.flatTop)
		baseHeight = func(int32, int32, int) int32 { return top }
	} else if d.terrain != nil {
		baseHeight = d.terrain.g.BaseHeight
		if short == "ruined_portal_ocean" && d.name == dimOverworld && s.LevelType == LevelTypeNormal {
			// Normal terrain uses the same overworld noise/biome settings as
			// the detached planner. Read its actual seed, including zero;
			// worldSeed's fixed flat fallback does not describe this source.
			source.NormalOverworld = true
			seed = d.terrain.g.RS.Seed
		}
	}
	plan, err := worldgen.PlanCommandStructure(s.jigsawRuntimeData(), seed, short, int32(position[0]>>4), int32(position[2]>>4), int32(d.minY), int32(d.height), int32(d.seaLevel), baseHeight, source)
	if err != nil || plan == nil {
		return componentReply(commandError("commands.place.structure.failed"))
	}
	bounds := plan.Bounds()
	for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
		for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
			if !s.blockPosLoaded(d, sender, int(cx*16), d.minY, int(cz*16)) {
				return componentReply(commandError("argument.pos.unloaded"))
			}
		}
	}
	chunks := map[[2]int32]*worldgen.ProtoChunk{}
	for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
		for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
			chunk := worldgen.NewProtoChunk(cx, cz, int32(d.minY), int32(d.height))
			for y := d.minY; y < d.minY+d.height; y++ {
				for z := 0; z < 16; z++ {
					for x := 0; x < 16; x++ {
						// Set maintains the WORLD_SURFACE_WG/OCEAN_FLOOR_WG
						// maps; template pieces sample them while placing.
						chunk.Set(int32(x), int32(y), int32(z), d.w.GetBlock(world.Pos{X: int(cx)*16 + x, Y: y, Z: int(cz)*16 + z}))
					}
				}
			}
			chunks[[2]int32{cx, cz}] = chunk
		}
	}
	var retained worldgen.CommandWGHeightmaps
	var identities map[[2]int]*world.BaseChunk
	if source.NormalOverworld {
		retained, identities = prepareCommandWG(d, bounds)
	}
	writes, entities, blockEntities := plan.Place(chunks, worldgen.NewLegacy(int64(s.rng.next())), retained)
	for _, write := range writes {
		p := write.Position
		s.setSilentBlock(d, world.Pos{X: int(p[0]), Y: int(p[1]), Z: int(p[2])}, write.State)
	}
	for _, be := range blockEntities {
		s.installGenBlockEntity(d, world.Pos{X: int(be.Pos[0]), Y: int(be.Pos[1]), Z: int(be.Pos[2])}, be.Data)
	}
	for _, entity := range entities {
		if entity.Type == "cat" {
			s.spawnPlacedStructureCat(d, entity)
		} else {
			s.spawnGenEntities(d, []worldgen.GenEntity{entity})
		}
	}
	if source.NormalOverworld {
		commitCommandWG(d, retained, identities)
	}
	response := opsEchoReply(translatable("commands.place.structure.success", nestedCompound{key: "", value: name}, nestedCompound{key: "", value: int32(position[0])}, nestedCompound{key: "", value: int32(position[1])}, nestedCompound{key: "", value: int32(position[2])}))
	response.result, response.hasResult = 1, true
	return response
}

// Explicit placement does not register a new natural structure start. Cat
// variant conditions therefore observe existing terrain starts and moon phase,
// rather than treating every detached hut blueprint as a registered black-cat site.
func (s *Server) spawnPlacedStructureCat(d *dimension, descriptor worldgen.GenEntity) {
	entity, ok := s.summonMobRaw(d, "cat", descriptor.X, descriptor.Y, descriptor.Z, false, false)
	if !ok {
		return
	}
	entity.PersistenceRequired = true
	entity.Yaw, entity.HeadYaw, entity.LookYaw = descriptor.Yaw, descriptor.Yaw, descriptor.Yaw
	blackStructure := false
	if d.terrain != nil {
		at := worldgen.BlockPos{int32(math.Floor(descriptor.X)), int32(math.Floor(descriptor.Y)), int32(math.Floor(descriptor.Z))}
		for _, start := range d.terrain.g.StructuresIntersecting(at[0]>>4, at[2]>>4) {
			if start.Structure == "swamp_hut" {
				for _, piece := range start.Pieces {
					if piece.BoundingBox().IsInside(at) {
						blackStructure = true
					}
				}
			}
		}
	}
	if blackStructure {
		entity.VariantName = "all_black"
	} else {
		// The public cat-variant registry gives all_black a moon-brightness minimum
		// of .9; only the full-moon phase reaches that threshold.
		phase := ((s.dayTime() % 192000) + 192000) % 192000 / 24000
		var choices []string
		for _, variant := range spawnVariantTables["cat"] {
			if variant.name != "all_black" || phase == 0 {
				choices = append(choices, variant.name)
			}
		}
		entity.VariantName = choices[rand.Intn(len(choices))]
	}
	entity.SoundVariant = rollSoundVariant("cat")
	s.mobDefaultEquip(entity)
	if !entity.Dormant {
		s.broadcastFreshMob(entity)
	}
}
