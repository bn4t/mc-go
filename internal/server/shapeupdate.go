package server

import (
	"math/rand"

	"mc-go/internal/data"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Block.updateShape for the block classes whose shape follows a
// neighbour change through canSurvive or a partner cell, measured from
// the 26.2 jar (each class's updateShape). Families with their own
// handler in checkSurvival (connections, vines, leaves, wire, diodes,
// coral, scaffolding, dripstone, ...) never reach this table.
//
// An updateShape that answers AIR destroys the block
// (Block.updateOrDestroy -> Level.destroyBlock): level event 2001, the
// cell's own fluid left behind (a waterlogged block leaves water) and
// its loot dropped with an empty tool. A tick-scheduling family pops
// the same way when its scheduled tick finds it unable to survive; the
// pop is applied at once here, as checkSurvival does for the rest.

// shapeResult is an updateShape outcome.
type shapeResult int

const (
	shapeKeep    shapeResult = iota // state unchanged
	shapeSet                        // state rewritten (silently)
	shapeDestroy                    // the block is destroyed
)

type shapeFunc func(s *Server, d *dimension, pos world.Pos, dir pdir, st, neighbor world.Block) (world.Block, shapeResult)

var shapeByClass map[string]shapeFunc

// dirToward names the direction from pos to an adjacent q.
func dirToward(pos, q world.Pos) (pdir, bool) {
	switch {
	case q.Y == pos.Y-1 && q.X == pos.X && q.Z == pos.Z:
		return pDown, true
	case q.Y == pos.Y+1 && q.X == pos.X && q.Z == pos.Z:
		return pUp, true
	case q.Z == pos.Z-1 && q.X == pos.X && q.Y == pos.Y:
		return pNorth, true
	case q.Z == pos.Z+1 && q.X == pos.X && q.Y == pos.Y:
		return pSouth, true
	case q.X == pos.X-1 && q.Y == pos.Y && q.Z == pos.Z:
		return pWest, true
	case q.X == pos.X+1 && q.Y == pos.Y && q.Z == pos.Z:
		return pEast, true
	}
	return 0, false
}

func facingOf(st world.Block) pdir {
	f, _ := world.StateProperty(st, "facing")
	d, _ := pdirFromName(f)
	return d
}

func init() {
	// surviveIf: AIR when cond holds and canSurvive fails.
	surviveIf := func(cond func(dir pdir, st world.Block) bool) shapeFunc {
		return func(s *Server, d *dimension, pos world.Pos, dir pdir, st, _ world.Block) (world.Block, shapeResult) {
			if cond(dir, st) && !blockCanSurvive(s, d, st, pos) {
				return 0, shapeDestroy
			}
			return st, shapeKeep
		}
	}
	always := func(pdir, world.Block) bool { return true }
	down := func(dir pdir, _ world.Block) bool { return dir == pDown }
	up := func(dir pdir, _ world.Block) bool { return dir == pUp }
	behind := func(dir pdir, st world.Block) bool { return dir.opp() == facingOf(st) }
	vegetation := surviveIf(always)
	shapeByClass = map[string]shapeFunc{
		"BaseTorchBlock":          surviveIf(down),
		"WallTorchBlock":          surviveIf(behind),
		"RedstoneWallTorchBlock":  surviveIf(behind),
		"WallSignBlock":           surviveIf(behind),
		"WallBannerBlock":         surviveIf(behind),
		"TripWireHookBlock":       surviveIf(behind),
		"LadderBlock":             surviveIf(behind),
		"AmethystClusterBlock":    surviveIf(behind),
		"BaseCoralWallFanBlock":   surviveIf(behind),
		"BannerBlock":             surviveIf(down),
		"StandingSignBlock":       surviveIf(down),
		"BasePressurePlateBlock":  surviveIf(down),
		"CakeBlock":               surviveIf(down),
		"CandleCakeBlock":         surviveIf(down),
		"FlowerPotBlock":          surviveIf(down),
		"BigDripleafBlock":        surviveIf(down),
		"BaseCoralPlantTypeBlock": surviveIf(down),
		"CeilingHangingSignBlock": surviveIf(up),
		"HangingRootsBlock":       surviveIf(up),
		"SporeBlossomBlock":       surviveIf(up),
		"CocoaBlock":              surviveIf(func(dir pdir, st world.Block) bool { return dir == facingOf(st) }),
		"VegetationBlock":         vegetation,
		"CarpetBlock":             vegetation,
		"SeaPickleBlock":          vegetation,
		"SnowLayerBlock":          vegetation,
		"CactusBlock":             vegetation,
		"SugarCaneBlock":          vegetation,
		"BambooStalkBlock":        vegetation,
		"ChorusPlantBlock":        nil, // handled in checkSurvival
		"BigDripleafStemBlock":    surviveIf(func(dir pdir, _ world.Block) bool { return dir == pDown || dir == pUp }),
		"WallHangingSignBlock":    surviveIf(func(dir pdir, st world.Block) bool { return dir.axis() == facingOf(st).cw().axis() }),
		"MangrovePropaguleBlock":  nil, // handled in checkSurvival
		"FrogspawnBlock":          nil,
		"SoulFireBlock":           vegetation,
	}
	shapeByClass["FaceAttachedHorizontalDirectionalBlock"] = surviveIf(func(dir pdir, st world.Block) bool {
		conn := facingOf(st)
		switch f, _ := world.StateProperty(st, "face"); f {
		case "floor":
			conn = pUp
		case "ceiling":
			conn = pDown
		}
		return dir == conn.opp()
	})
	shapeByClass["LanternBlock"] = surviveIf(func(dir pdir, st world.Block) bool {
		conn := pUp
		if propIs(st, "hanging", "true") {
			conn = pDown
		}
		return dir == conn.opp()
	})
	shapeByClass["BambooSaplingBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, n world.Block) (world.Block, shapeResult) {
		if !blockCanSurvive(s, d, st, pos) {
			return 0, shapeDestroy
		}
		if dir == pUp && bstate(n) == "bamboo" {
			b, _ := world.BlockState("bamboo")
			return b, shapeSet
		}
		return st, shapeKeep
	}
	shapeByClass["DoorBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, n world.Block) (world.Block, shapeResult) {
		lower := propIs(st, "half", "lower")
		half, _ := world.StateProperty(st, "half")
		if dir.axis() == 'y' && lower == (dir == pUp) {
			if hasClassName(bstate(n), "DoorBlock") && !propIs(n, "half", half) {
				nb := with(n, "half", half)
				if nb == st {
					return st, shapeKeep
				}
				return nb, shapeSet
			}
			return 0, shapeDestroy
		}
		if lower && dir == pDown && !blockCanSurvive(s, d, st, pos) {
			return 0, shapeDestroy
		}
		return st, shapeKeep
	}
	doublePlant := func(s *Server, d *dimension, pos world.Pos, dir pdir, st, n world.Block) (world.Block, shapeResult) {
		lower := propIs(st, "half", "lower")
		half, _ := world.StateProperty(st, "half")
		if dir.axis() == 'y' && lower == (dir == pUp) && (bstate(n) != bstate(st) || propIs(n, "half", half)) {
			return 0, shapeDestroy
		}
		if lower && dir == pDown && !blockCanSurvive(s, d, st, pos) {
			return 0, shapeDestroy
		}
		return vegetation(s, d, pos, dir, st, n)
	}
	shapeByClass["DoublePlantBlock"] = doublePlant
	shapeByClass["PitcherCropBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, n world.Block) (world.Block, shapeResult) {
		// PitcherCropBlock.isDouble: age >= 3.
		if propInt(st, "age") >= 3 {
			return doublePlant(s, d, pos, dir, st, n)
		}
		if !blockCanSurvive(s, d, st, pos) {
			return 0, shapeDestroy
		}
		return st, shapeKeep
	}
	shapeByClass["BedBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, n world.Block) (world.Block, shapeResult) {
		f := facingOf(st)
		toward := f // the foot's partner sits ahead of it
		if propIs(st, "part", "head") {
			toward = f.opp()
		}
		if dir != toward {
			return st, shapeKeep
		}
		part, _ := world.StateProperty(st, "part")
		if bstate(n) == bstate(st) && !propIs(n, "part", part) {
			occ, _ := world.StateProperty(n, "occupied")
			if nb := with(st, "occupied", occ); nb != st {
				return nb, shapeSet
			}
			return st, shapeKeep
		}
		return 0, shapeDestroy
	}
	shapeByClass["NoteBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, _ world.Block) (world.Block, shapeResult) {
		if dir.axis() != 'y' {
			return st, shapeKeep
		}
		if nb := noteBlockInstrument(d, pos, st); nb != st {
			return nb, shapeSet
		}
		return st, shapeKeep
	}
	shapeByClass["CampfireBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, n world.Block) (world.Block, shapeResult) {
		if dir != pDown {
			return st, shapeKeep
		}
		if nb := withBool(st, "signal_fire", bstate(n) == "hay_block"); nb != st {
			return nb, shapeSet
		}
		return st, shapeKeep
	}
	shapeByClass["FenceGateBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, n world.Block) (world.Block, shapeResult) {
		if dir.axis() != facingOf(st).cw().axis() {
			return st, shapeKeep
		}
		inWall := worldgen.InBlockTag(n, "walls") || worldgen.InBlockTag(d.w.GetBlock(dir.opp().rel(pos)), "walls")
		if nb := withBool(st, "in_wall", inWall); nb != st {
			return nb, shapeSet
		}
		return st, shapeKeep
	}
	shapeByClass["BellBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, n world.Block) (world.Block, shapeResult) {
		att, _ := world.StateProperty(st, "attachment")
		conn := facingOf(st).opp()
		switch att {
		case "floor":
			conn = pUp
		case "ceiling":
			conn = pDown
		}
		toward := conn.opp()
		if toward == dir && !blockCanSurvive(s, d, st, pos) && att != "double_wall" {
			return 0, shapeDestroy
		}
		if dir.axis() == facingOf(st).axis() {
			if att == "double_wall" && !sturdy(n, dir.opp()) {
				return with(with(st, "attachment", "single_wall"), "facing", dir.opp().String()), shapeSet
			}
			if att == "single_wall" && toward.opp() == dir && sturdy(n, facingOf(st)) {
				return with(st, "attachment", "double_wall"), shapeSet
			}
		}
		return st, shapeKeep
	}
	shapeByClass["AttachedStemBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, n world.Block) (world.Block, shapeResult) {
		name := bstate(st)
		fruit := map[string]string{"attached_pumpkin_stem": "pumpkin", "attached_melon_stem": "melon"}[name]
		if bstate(n) != fruit && dir == facingOf(st) {
			stem, _ := world.BlockState(name[len("attached_"):])
			return with(stem, "age", "7"), shapeSet
		}
		return vegetation(s, d, pos, dir, st, n)
	}
	shapeByClass["CandleBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, _ world.Block) (world.Block, shapeResult) {
		return st, shapeKeep
	}
	shapeByClass["MossyCarpetBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, _ world.Block) (world.Block, shapeResult) {
		if !blockCanSurvive(s, d, st, pos) {
			return 0, shapeDestroy
		}
		pc := &placeCtx{s: s, d: d, pos: pos}
		nb := mossyCarpetUpdated(pc, st, false)
		faces := propIs(nb, "bottom", "true")
		for _, f := range []string{"north", "east", "south", "west"} {
			faces = faces || !propIs(nb, f, "none")
		}
		if !faces {
			return 0, shapeDestroy
		}
		if nb != st {
			return nb, shapeSet
		}
		return st, shapeKeep
	}
	shapeByClass["DirtPathBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, _ world.Block) (world.Block, shapeResult) {
		// The scheduled tick turns an unsurvivable path or farmland
		// to dirt.
		if dir == pUp && !blockCanSurvive(s, d, st, pos) {
			dirt, _ := world.BlockState("dirt")
			return dirt, shapeSet
		}
		return st, shapeKeep
	}
	shapeByClass["FarmlandBlock"] = shapeByClass["DirtPathBlock"]
	shapeByClass["PotentSulfurBlock"] = func(s *Server, d *dimension, pos world.Pos, dir pdir, st, _ world.Block) (world.Block, shapeResult) {
		pc := &placeCtx{s: s, d: d, pos: pos}
		if nb := potentSulfurState(pc, st); nb != st {
			return nb, shapeSet
		}
		return st, shapeKeep
	}
}

// classUpdateShape runs the class table for a neighbour change at
// changed; false when the block's class has no entry (the caller's
// legacy handlers then apply).
func (s *Server) classUpdateShape(d *dimension, pos, changed world.Pos, st world.Block) bool {
	if changed == pos {
		return false
	}
	dir, ok := dirToward(pos, changed)
	if !ok {
		return false
	}
	name := bstate(st)
	for _, cls := range blockClassOf(name) {
		f, has := shapeByClass[cls]
		if !has {
			continue
		}
		if f == nil {
			return false
		}
		nb, res := f(s, d, pos, dir, st, d.w.GetBlock(changed))
		switch res {
		case shapeSet:
			s.setNaturalBlock(d, pos, nb)
		case shapeDestroy:
			s.destroyBlockNatural(d, pos, st)
		}
		return true
	}
	return false
}

// destroyBlockNatural is Level.destroyBlock(pos, true) with no
// breaker: level event 2001 (not for fire), the cell's fluid left in
// place, and the block's loot dropped with an empty tool.
func (s *Server) destroyBlockNatural(d *dimension, pos world.Pos, st world.Block) {
	name := bstate(st)
	if !hasClassName(name, "BaseFireBlock") {
		s.broadcastLevelEvent(d.name, 2001, pos, int32(st))
	}
	repl := world.Air
	if propIs(st, "waterlogged", "true") {
		repl, _ = world.BlockState("water")
	}
	s.setNaturalBlock(d, pos, repl)
	if _, ok := data.LootTables()["blocks/"+name]; ok {
		ctx := &lootCtx{block: name, state: st, pos: pos, dim: d, rng: rand.New(rand.NewSource(rand.Int63()))}
		for _, it := range rollLootDepth(ctx, "blocks/"+name, 0) {
			s.spawnItemDrop(d.name, pos, it)
		}
	}
}
