package server

import (
	"strings"

	"mc-go/internal/world"
)

// runClone implements /clone:
//
//	clone [from <sourceDimension>] <begin> <end> [to <targetDimension>]
//	    <destination> [strict] [replace|masked|filtered <block>]
//	    [normal|force|move]
//
// strict is the position literal the dedicated server parses ahead of the
// mask literal; move is only legal after a mask, filtered takes a block
// predicate (name, [props], or #tag), and the overlap guard is skipped
// for force.
func (s *Server) runClone(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	sender := s.findSessionByName(player)
	srcDim := s.dimOf(player)
	dstDim := srcDim
	var srcDimName, dstDimName string
	i := 0
	if args[0] == "from" {
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		srcDimName = normalizeID(args[1])
		i = 2
	}
	if len(args)-i == 0 {
		return errTokenEnd("command.unknown.command", i+1)
	}
	begin, r := s.parseBlockPos(sender, args[i:], i+1)
	if r != nil {
		return *r
	}
	i += 3
	if len(args)-i == 0 {
		return errTokenEnd("command.unknown.command", i+1)
	}
	end, r := s.parseBlockPos(sender, args[i:], i+1)
	if r != nil {
		return *r
	}
	i += 3
	if args[0] == "from" {
		if i >= len(args) {
			return errTokenEnd("command.unknown.command", i+1)
		}
		if args[i] == "to" {
			i++
			if i >= len(args) {
				return errTokenEnd("command.unknown.command", i+1)
			}
			dstDimName = normalizeID(args[i])
			i++
		}
	}
	if len(args)-i == 0 {
		return errTokenEnd("command.unknown.command", i+1)
	}
	dst, r := s.parseBlockPos(sender, args[i:], i+1)
	if r != nil {
		return *r
	}
	i += 3
	strict := false
	if i < len(args) && args[i] == "strict" {
		strict = true
		i++
	}
	mask := 0 // 0 replace, 1 masked, 2 filtered
	var filter func(*Server, *dimension, world.Pos) bool
	if i < len(args) {
		switch args[i] {
		case "replace":
			i++
		case "masked":
			mask = 1
			i++
		case "filtered":
			mask = 2
			i++
			if i >= len(args) {
				return errTokenEnd("command.unknown.command", i+1)
			}
			tok := args[i]
			if !strings.HasPrefix(tok, "#") {
				spec := tok
				if k := strings.IndexByte(spec, '{'); k >= 0 {
					spec = spec[:k]
				}
				var name string
				if j := strings.IndexByte(spec, '['); j >= 0 {
					name = spec[:j]
				} else {
					name = spec
				}
				name = strings.ToLower(name)
				if !strings.HasPrefix(name, "minecraft:") {
					name = "minecraft:" + name
				}
				if world.BlockPropNames(strings.TrimPrefix(name, "minecraft:")) == nil {
					return errToken("argument.block.id.invalid", i+1, name)
				}
				if j := strings.IndexByte(spec, '['); j >= 0 {
					body := spec[j+1:]
					if k := strings.IndexByte(body, ']'); k >= 0 {
						body = body[:k]
					}
					prop := strings.TrimSpace(strings.SplitN(body, "=", 2)[0])
					found := false
					for _, p := range world.BlockPropNames(strings.TrimPrefix(name, "minecraft:")) {
						if p == prop {
							found = true
							break
						}
					}
					if !found {
						return errTokenOff("argument.block.property.unknown", i+1, j+1, name, prop)
					}
				}
			}
			var err *reply
			filter, err = parseBlockFilterPredicate(tok, i+1)
			if err != nil {
				return *err
			}
			i++
		default:
			return errToken("command.unknown.argument", i+1)
		}
	}
	mode := 0 // 0 normal, 1 force, 2 move
	if i < len(args) {
		switch args[i] {
		case "normal":
			i++
		case "force":
			mode = 1
			i++
		case "move":
			mode = 2
			i++
		default:
			return errToken("command.unknown.argument", i+1)
		}
	}
	if i < len(args) {
		return errToken("command.unknown.argument", i+1)
	}

	if srcDimName != "" {
		d, ok := s.dims[srcDimName]
		if !ok {
			return componentReply(commandError("argument.dimension.invalid",
				srcDimName))
		}
		srcDim = d
	}
	if dstDimName != "" {
		d, ok := s.dims[dstDimName]
		if !ok {
			return componentReply(commandError("argument.dimension.invalid",
				dstDimName))
		}
		dstDim = d
	}
	minX, maxX := minmax(begin[0], end[0])
	minY, maxY := minmax(begin[1], end[1])
	minZ, maxZ := minmax(begin[2], end[2])
	if !s.blockPosLoaded(srcDim, sender, begin[0], begin[1], begin[2]) ||
		!s.blockPosLoaded(srcDim, sender, end[0], end[1], end[2]) {
		return componentReply(commandError("argument.pos.unloaded"))
	}
	if !s.blockPosLoaded(dstDim, sender, dst[0], dst[1], dst[2]) ||
		!s.blockPosLoaded(dstDim, sender, dst[0]+maxX-minX, dst[1]+maxY-minY, dst[2]+maxZ-minZ) {
		return componentReply(commandError("argument.pos.unloaded"))
	}
	vol := (maxX - minX + 1) * (maxY - minY + 1) * (maxZ - minZ + 1)
	if limit := s.gameRuleInt("minecraft:max_block_modifications"); vol > limit {
		return componentReply(commandError("commands.clone.toobig",
			int32(limit), int32(vol)))
	}
	if mode == 0 &&
		dst[0] <= maxX && dst[0]+maxX-minX >= minX &&
		dst[1] <= maxY && dst[1]+maxY-minY >= minY &&
		dst[2] <= maxZ && dst[2]+maxZ-minZ >= minZ && srcDim == dstDim {
		return componentReply(commandError("commands.clone.overlap"))
	}

	// Snapshot the source cells first so move-mode self-overlap
	// clones the pre-erase state.
	type cell struct {
		rel [3]int
		blk world.Block
		tag map[string]any
	}
	var cells []cell
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				blk := srcDim.w.GetBlock(world.Pos{X: x, Y: y, Z: z})
				switch mask {
				case 1:
					if blk == world.Air {
						continue
					}
				case 2:
					ok := filter(s, srcDim, world.Pos{X: x, Y: y, Z: z})
					if !ok {
						continue
					}
				}
				cells = append(cells, cell{[3]int{x - minX, y - minY, z - minZ}, blk, s.cloneNativeTag(srcDim, world.Pos{X: x, Y: y, Z: z})})
			}
		}
	}
	if len(cells) == 0 {
		return componentReply(commandError("commands.clone.failed"))
	}
	inDst := func(x, y, z int) bool {
		return dstDim == srcDim &&
			x >= dst[0] && x <= dst[0]+maxX-minX &&
			y >= dst[1] && y <= dst[1]+maxY-minY &&
			z >= dst[2] && z <= dst[2]+maxZ-minZ
	}
	for _, c := range cells {
		// Clone writes notify neighbours with the NEW state
		// (updateNeighboursOnBlockSet).
		s.clonePlaceNativeCell(dstDim, world.Pos{
			X: dst[0] + c.rel[0],
			Y: dst[1] + c.rel[1],
			Z: dst[2] + c.rel[2]}, c.blk, c.tag, strict)
	}
	if mode == 2 {
		for _, c := range cells {
			x, y, z := minX+c.rel[0], minY+c.rel[1], minZ+c.rel[2]
			if !inDst(x, y, z) {
				pos := world.Pos{X: x, Y: y, Z: z}
				if strict {
					s.setSilentBlockWithPolicy(srcDim, pos, world.Air, true, true)
				} else {
					s.structureRetireBlockEntity(srcDim, pos, srcDim.w.GetBlock(pos), world.Air)
					s.setNaturalBlockNotify(srcDim, pos, world.Air, world.Air)
				}
			}
		}
	}
	if !strict {
		// Ordinary clone notifies only after every copied block entity is
		// hydrated. The earlier writes provide shape updates, not this
		// final neighborChanged pass (which can consume native BE data).
		for i := len(cells) - 1; i >= 0; i-- {
			c := cells[i]
			s.rsNeighborFanOut(dstDim, world.Pos{X: dst[0] + c.rel[0], Y: dst[1] + c.rel[1], Z: dst[2] + c.rel[2]})
		}
	}
	return opsEchoReply(translatable("commands.clone.success", int32(len(cells))))
}
