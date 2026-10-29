package server

import (
	"math"
	"strconv"
	"strings"

	"mc-go/internal/worldgen"
)

// locateStructure is LocateCommand.locateStructure in a generated
// dimension: ChunkGenerator.findNearestMapStructure from the source's
// block position within 100 placement cells, reported at the
// structure's locate position with "~" for y and the horizontal
// distance (Mth.floor of the float sqrt of the squared x/z offsets).
// A tag prints as "#tag (structure)". Without a generator (the flat
// world) or with structures off there is nothing to find.
func (s *Server) locateStructure(player, short string, tag bool, printable string) reply {
	sender := s.findSessionByName(player)
	d := s.dims[dimOverworld]
	var x, y, z float64
	if sender != nil {
		d = s.dims[sender.dimName()]
		x, y, z, _, _, _ = sender.Pos()
		if p := sender.execPos; p != nil {
			x, y, z = p[0], p[1], p[2]
		} else if e := sender.execEnt; e != nil && e.mob != nil {
			x, y, z = e.mob.X, e.mob.Y, e.mob.Z
		}
	} else {
		x, y, z = s.WorldSpawn()
	}
	notFound := componentReply(commandError("commands.locate.structure.not_found", printable))
	if d == nil || d.terrain == nil {
		return notFound
	}
	g := d.terrain.g
	names := []string{short}
	if tag {
		var ok bool
		if names, ok = g.StructureTag(short); !ok {
			return notFound
		}
	}
	src := worldgen.BlockPos{int32(math.Floor(x)), int32(math.Floor(y)), int32(math.Floor(z))}
	at, name, ok := g.FindNearestMapStructure(names, src, 100)
	if !ok {
		return notFound
	}
	if tag {
		printable += " (minecraft:" + strings.TrimPrefix(name, "minecraft:") + ")"
	}
	dx, dz := at[0]-src[0], at[2]-src[2]
	dist := int32(math.Floor(float64(float32(math.Sqrt(float64(float32(dx*dx + dz*dz)))))))
	return componentReply(translatable("commands.locate.structure.success",
		nestedCompound{key: "", value: printable},
		locatePosComp(at[0], "~", at[2]),
		nestedCompound{key: "", value: dist}))
}

// locateBiome is LocateCommand.locateBiome in a generated dimension:
// ServerLevel.findClosestBiome3d from the source's block position
// (radius 6400, 32-block columns, 64-block heights), reported with its
// y and the floored 3D distance. A tag prints as "#tag (biome)".
func (s *Server) locateBiome(sender *Session, d *dimension, printable string, tag bool, want func(string) bool) reply {
	x, y, z, _, _, _ := sender.Pos()
	if p := sender.execPos; p != nil {
		x, y, z = p[0], p[1], p[2]
	} else if e := sender.execEnt; e != nil && e.mob != nil {
		x, y, z = e.mob.X, e.mob.Y, e.mob.Z
	}
	src := worldgen.BlockPos{int32(math.Floor(x)), int32(math.Floor(y)), int32(math.Floor(z))}
	at, name, ok := d.terrain.g.FindClosestBiome3d(src, 6400, 32, 64, want)
	if !ok {
		return componentReply(commandError("commands.locate.biome.not_found", printable))
	}
	if tag {
		printable += " (minecraft:" + name + ")"
	}
	dx, dy, dz := float64(at[0]-src[0]), float64(at[1]-src[1]), float64(at[2]-src[2])
	dist := int32(math.Floor(float64(float32(math.Sqrt(float64(float32(dx*dx + dy*dy + dz*dz)))))))
	return componentReply(translatable("commands.locate.biome.success",
		nestedCompound{key: "", value: printable},
		locatePosComp(at[0], strconv.Itoa(int(at[1])), at[2]),
		nestedCompound{key: "", value: dist}))
}
